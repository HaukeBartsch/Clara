package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// mustInstanceValue stores one value of instance n of a repeating instrument at
// its storage slot (REQ-DB-048): instance 1 under the empty name, a further
// instance under the instrument's.
func (e *env) mustInstanceValue(projectID int64, recordID, event, instrument string, instance int, field, value string) {
	e.t.Helper()
	slot, n := db.InstanceSlot(instrument, instance)
	if err := e.Store.AddDataValue(context.Background(), &db.DataValue{
		ProjectID: projectID, RecordID: recordID, UniqueEventName: event,
		RepeatingInstrument: slot, RepeatingInstanceNumber: n, FieldName: field, Value: value,
	}); err != nil {
		e.t.Fatalf("AddDataValue: %v", err)
	}
}

func repeatingURL(projectID, instrumentID int64) string {
	return "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instrumentID) + "/repeating"
}

func TestRepeatingIdentifierInstrumentRefusedAfterEmptyInstrument(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	ctx := context.Background()
	projectID := e.createProjectViaAPI(admin, "Repeating Identifier")
	instruments, err := e.Store.ListInstruments(ctx, projectID)
	if err != nil || len(instruments) != 1 {
		t.Fatalf("ListInstruments: %v, got %d", err, len(instruments))
	}
	arms, err := e.Store.ListArms(ctx, projectID)
	if err != nil || len(arms) != 1 {
		t.Fatalf("ListArms: %v, got %d", err, len(arms))
	}
	events, err := e.Store.ListEvents(ctx, projectID)
	if err != nil || len(events) != 1 {
		t.Fatalf("ListEvents: %v, got %d", err, len(events))
	}
	dataID, err := e.Store.AddInstrument(ctx, &db.Instrument{
		ProjectID: projectID, Name: "data", Position: 2,
	})
	if err != nil {
		t.Fatalf("AddInstrument(data): %v", err)
	}
	e.mustField(projectID, dataID, "study_key", "text", 1)
	if err := e.Store.SetInstrumentEventsForArm(ctx, projectID, arms[0].ID, []db.InstrumentEvent{
		{InstrumentID: dataID, EventID: events[0].ID},
	}); err != nil {
		t.Fatalf("SetInstrumentEventsForArm: %v", err)
	}

	if rec := e.do("PUT", repeatingURL(projectID, dataID), map[string]any{
		"events": []string{events[0].UniqueEventName},
	}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("repeat identifier instrument: %d %s", rec.Code, rec.Body.String())
	}
}

// instrumentByID reads one instrument object from the listing.
func (e *env) instrumentObjectOf(t *testing.T, projectID, instrumentID int64, actor *db.User) instrumentObject {
	t.Helper()
	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/instruments", nil, actor)
	var list []instrumentObject
	e.decode(rec, &list)
	for _, o := range list {
		if o.ID == instrumentID {
			return o
		}
	}
	t.Fatalf("instrument %d not listed: %s", instrumentID, rec.Body.String())
	return instrumentObject{}
}

// TestRepeatingDesignation covers PUT …/instruments/{iid}/repeating
// (REQ-API-154): only mapped events, never a survey or the first instrument,
// the set replaces the old one, it is listed and audited, and a mapping change
// keeps it for surviving pairs and drops it with an unmapped one.
func TestRepeatingDesignation(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Repeating Study")
	f := e.seedDesign(projectID) // intake (first) @ baseline; labs @ baseline + followup_arm_2
	base := "/api/v1/projects/" + itoa(projectID)

	// Every list starts empty — never null.
	if rec := e.do("GET", base+"/instruments", nil, admin); !strings.Contains(rec.Body.String(), `"repeating_events":[]`) {
		t.Fatalf("listing without repeating: %s", rec.Body.String())
	}

	// An event the instrument is not mapped to → 400; an unknown one too.
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentA), map[string]any{"events": []string{"followup_arm_2"}}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("unmapped event: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"nope_arm_1"}}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown event: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing events: %d %s", rec.Code, rec.Body.String())
	}
	// The first instrument holds the record identifier (GD-8) → 409.
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentA), map[string]any{"events": []string{"baseline_arm_1"}}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("first instrument: %d %s", rec.Code, rec.Body.String())
	}

	rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"followup_arm_2", "baseline_arm_1"}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("designate: %d %s", rec.Code, rec.Body.String())
	}
	var obj instrumentObject
	e.decode(rec, &obj)
	if strings.Join(obj.RepeatingEvents, ",") != "baseline_arm_1,followup_arm_2" {
		t.Fatalf("repeating events (arm order): %+v", obj)
	}
	if got := e.instrumentObjectOf(t, projectID, f.instrumentB, admin).RepeatingEvents; len(got) != 2 {
		t.Fatalf("listing: %v", got)
	}
	var details string
	if err := e.Store.DB.QueryRow(`SELECT details FROM audit_events WHERE event_type = 'instrument_updated' ORDER BY id DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, `"repeating_events"`) {
		t.Fatalf("audit details: %s", details)
	}

	// A repeating instrument cannot become a survey, nor the first instrument.
	if rec := e.do("PUT", base+"/instruments/"+itoa(f.instrumentB), map[string]any{"is_survey": true}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("repeating → survey: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", base+"/instruments/order", map[string]any{"order": []int64{f.instrumentB, f.instrumentA}}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("repeating instrument first: %d %s", rec.Code, rec.Body.String())
	}

	// The mapping matrix of arm 1 keeps the surviving pair's flag...
	rec = e.do("PUT", base+"/instrument-event-mapping", map[string]any{
		"arm_num": 1, "mapping": map[string][]string{"intake": {"baseline_arm_1"}, "labs": {"baseline_arm_1"}},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("remap: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.instrumentObjectOf(t, projectID, f.instrumentB, admin).RepeatingEvents; strings.Join(got, ",") != "baseline_arm_1,followup_arm_2" {
		t.Fatalf("after remapping arm 1: %v", got)
	}
	// ...and an unmapped pair loses it with its row (REQ-DB-047).
	rec = e.do("PUT", base+"/instrument-event-mapping", map[string]any{
		"arm_num": 2, "mapping": map[string][]string{"labs": {}},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("unmap: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.instrumentObjectOf(t, projectID, f.instrumentB, admin).RepeatingEvents; strings.Join(got, ",") != "baseline_arm_1" {
		t.Fatalf("after unmapping arm 2: %v", got)
	}

	// [] turns repeating off everywhere.
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("off: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.instrumentObjectOf(t, projectID, f.instrumentB, admin).RepeatingEvents; len(got) != 0 {
		t.Fatalf("after turning off: %v", got)
	}
}

// TestRepeatingSurveyRefused: a survey instrument cannot repeat (REQ-API-154).
func TestRepeatingSurveyRefused(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Repeating Survey")
	f := e.seedDesign(projectID)
	base := "/api/v1/projects/" + itoa(projectID)
	if rec := e.do("PUT", base+"/instruments/"+itoa(f.instrumentB), map[string]any{"is_survey": true}, admin); rec.Code != http.StatusOK {
		t.Fatalf("make survey: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"baseline_arm_1"}}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("survey repeating: %d %s", rec.Code, rec.Body.String())
	}
}

// TestRepeatingRenameMovesInstances: further instances are stored under the
// instrument's name (REQ-DB-048), so a rename carries them — live, and through
// a staging commit.
func TestRepeatingRenameMovesInstances(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Repeating Rename")
	f := e.seedDesign(projectID)
	e.mustRecord(projectID, "R1")
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"baseline_arm_1"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("designate: %d %s", rec.Code, rec.Body.String())
	}
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "labs", 2, "hb", "11")

	slotOf := func() string {
		var name string
		if err := e.Store.DB.QueryRow(`SELECT repeating_instrument FROM data WHERE project_id = ? AND repeating_instance_number = 2`, projectID).Scan(&name); err != nil {
			t.Fatal(err)
		}
		return name
	}
	instURL := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(f.instrumentB)
	if rec := e.do("PUT", instURL, map[string]any{"name": "bloods"}, admin); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	if got := slotOf(); got != "bloods" {
		t.Fatalf("live rename left the instance under %q", got)
	}
	if got := e.instrumentObjectOf(t, projectID, f.instrumentB, admin).RepeatingEvents; len(got) != 1 {
		t.Fatalf("designation lost on rename: %v", got)
	}

	// Through staging: rename, commit — the instance follows, the flag survives.
	e.mustProduction(projectID, admin)
	if rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin); rec.Code != http.StatusCreated {
		t.Fatalf("start staging: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", instURL, map[string]any{"name": "labs2"}, admin); rec.Code != http.StatusOK {
		t.Fatalf("staged rename: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging/commit", nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	if got := slotOf(); got != "labs2" {
		t.Fatalf("staged rename left the instance under %q", got)
	}
	if got := e.instrumentObjectOf(t, projectID, f.instrumentB, admin).RepeatingEvents; len(got) != 1 {
		t.Fatalf("designation lost through commit: %v", got)
	}
}

// TestRepeatingStaged: in production the designation is staged like any
// structure change, classifies as the non-breaking repeating_on, and reaches the
// live pair at commit; an event renamed in the same set keeps its pairs.
func TestRepeatingStaged(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Repeating Staged")
	f := e.seedDesign(projectID)
	e.mustProduction(projectID, admin)
	base := "/api/v1/projects/" + itoa(projectID)

	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"baseline_arm_1"}}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("production without a set: %d, want 409", rec.Code)
	}
	if rec := e.do("POST", base+"/staging", nil, admin); rec.Code != http.StatusCreated {
		t.Fatalf("start staging: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"baseline_arm_1"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("staged designate: %d %s", rec.Code, rec.Body.String())
	}
	// Rename the event in the same set: its mapping and designation follow.
	if rec := e.do("PUT", "/api/v1/events/"+itoa(f.event1), map[string]any{"event_name": "Screening"}, admin); rec.Code != http.StatusOK {
		t.Fatalf("staged event rename: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.do("GET", base+"/staging", nil, admin)
	if !strings.Contains(rec.Body.String(), `"repeating_on"`) || strings.Contains(rec.Body.String(), `"unmapped"`) {
		t.Fatalf("staging diff: %s", rec.Body.String())
	}
	// The live pair waits for the commit.
	pairs, err := e.Store.ListInstrumentEvents(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pairs {
		if p.IsRepeating {
			t.Fatalf("live pair repeats before commit: %+v", p)
		}
	}
	if rec := e.do("POST", base+"/staging/commit", nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.instrumentObjectOf(t, projectID, f.instrumentB, admin).RepeatingEvents; strings.Join(got, ",") != "Screening_arm_1" {
		t.Fatalf("after commit: %v", got)
	}
	if got := e.instrumentObjectOf(t, projectID, f.instrumentA, admin); got.ID == 0 {
		t.Fatal("first instrument lost")
	}
	rec = e.do("GET", base+"/instrument-event-mapping", nil, admin)
	var mapping []armMappingObject
	e.decode(rec, &mapping)
	if len(mapping[0].Mapping["intake"]) != 1 || len(mapping[0].Mapping["labs"]) != 1 {
		t.Fatalf("renamed event lost its pairs: %s", rec.Body.String())
	}
}

// recordStatusOf reads one record's row of the dashboard.
func (e *env) recordStatusOf(t *testing.T, projectID int64, record string, actor *db.User) recordStatusRow {
	t.Helper()
	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/record-status", nil, actor)
	if rec.Code != http.StatusOK {
		t.Fatalf("record-status: %d %s", rec.Code, rec.Body.String())
	}
	var rows []recordStatusRow
	e.decode(rec, &rows)
	for _, r := range rows {
		if r.RecordID == record {
			return r
		}
	}
	t.Fatalf("record %s not listed: %s", record, rec.Body.String())
	return recordStatusRow{}
}

func instrumentState(row recordStatusRow, event, instrument string) (recordStatusInstrument, bool) {
	for _, ev := range row.Events {
		if ev.UniqueEventName != event {
			continue
		}
		for _, in := range ev.Instruments {
			if in.Name == instrument {
				return in, true
			}
		}
	}
	return recordStatusInstrument{}, false
}

// TestRepeatingRecordStatusAndCompletion: the dashboard lists every instance of
// a repeating pair with its state, completion is held per instance, and turning
// repeating off hides — never deletes — the further instances (REQ-API-156,
// REQ-DB-048).
func TestRepeatingRecordStatusAndCompletion(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Repeating Status")
	f := e.seedDesign(projectID)
	e.mustRecord(projectID, "R1")
	e.mustValue(projectID, "R1", "baseline_arm_1", "", "record_id", "R1")
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"baseline_arm_1"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("designate: %d %s", rec.Code, rec.Body.String())
	}
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "labs", 1, "hb", "10")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "labs", 3, "hb", "12")

	// Instance 2 is finished without a value: listed too.
	path := completionPath(projectID, "R1", "baseline_arm_1", f.instrumentB)
	if rec := e.do("PUT", path, map[string]any{"state": "finished", "instance": 2}, admin); rec.Code != http.StatusOK {
		t.Fatalf("finish instance 2: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", path, map[string]any{"state": "finished", "instance": 0}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("instance 0: %d %s", rec.Code, rec.Body.String())
	}
	// A pair that does not repeat takes no further instance.
	if rec := e.do("PUT", completionPath(projectID, "R1", "baseline_arm_1", f.instrumentA), map[string]any{"state": "finished", "instance": 2}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("non-repeating instance 2: %d %s", rec.Code, rec.Body.String())
	}

	row := e.recordStatusOf(t, projectID, "R1", admin)
	labs, ok := instrumentState(row, "baseline_arm_1", "labs")
	if !ok || labs.State != StateSomeData {
		t.Fatalf("labs at baseline: %+v", labs)
	}
	got, _ := json.Marshal(labs.Instances)
	if string(got) != `[{"instance":1,"state":"some_data"},{"instance":2,"state":"finished"},{"instance":3,"state":"some_data"}]` {
		t.Fatalf("instances: %s", got)
	}
	// The first instance's finished state stays its own.
	var details string
	if err := e.Store.DB.QueryRow(`SELECT details FROM audit_events WHERE event_type = 'instrument_completed' ORDER BY id DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, `"instance":2`) {
		t.Fatalf("completion audit: %s", details)
	}

	// Off: the further instances are hidden, the first stays; nothing is deleted.
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("off: %d %s", rec.Code, rec.Body.String())
	}
	row = e.recordStatusOf(t, projectID, "R1", admin)
	labs, _ = instrumentState(row, "baseline_arm_1", "labs")
	if labs.State != StateSomeData || labs.Instances != nil {
		t.Fatalf("labs after turning off: %+v", labs)
	}
	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM data WHERE project_id = ? AND repeating_instrument = 'labs'`, projectID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hidden instance rows = %d (%v), want 1", n, err)
	}
	// On again: they are back.
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"baseline_arm_1"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("on again: %d %s", rec.Code, rec.Body.String())
	}
	row = e.recordStatusOf(t, projectID, "R1", admin)
	labs, _ = instrumentState(row, "baseline_arm_1", "labs")
	if len(labs.Instances) != 3 {
		t.Fatalf("labs after turning on again: %+v", labs)
	}
}

// TestRepeatingHistoryInstance: every history entry names its instance, and
// ?instance= narrows the history to one (REQ-API-156).
func TestRepeatingHistoryInstance(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Repeating History")
	e.seedDesign(projectID)
	e.mustRecord(projectID, "R1")
	for _, details := range []string{
		`{"action":"update","record_id":"R1","instrument":"labs","event":"baseline_arm_1","fields":[{"field":"hb","old":null,"new":"10"}]}`,
		`{"action":"update","record_id":"R1","instrument":"labs","event":"baseline_arm_1","instance":2,"fields":[{"field":"hb","old":null,"new":"11"}]}`,
	} {
		if err := e.Audit.Insert(context.Background(), audit.Entry{
			EventType: audit.RecordUpdated, Source: audit.SourceAPI, UserID: admin.ID, Email: admin.Email,
			ProjectID: projectID, TargetRecord: "R1", Details: json.RawMessage(details),
		}); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
	}
	read := func(query string) []historyEntry {
		rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/records/R1/history"+query, nil, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("history%s: %d %s", query, rec.Code, rec.Body.String())
		}
		var page struct {
			Entries []historyEntry `json:"entries"`
		}
		e.decode(rec, &page)
		return page.Entries
	}
	all := read("")
	if len(all) != 2 || all[0].Instance != 1 || all[1].Instance != 2 {
		t.Fatalf("history: %+v", all)
	}
	if only := read("?instance=2"); len(only) != 1 || only[0].Instance != 2 {
		t.Fatalf("instance filter: %+v", only)
	}
	if rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/records/R1/history?instance=0", nil, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("instance=0: %d", rec.Code)
	}
}
