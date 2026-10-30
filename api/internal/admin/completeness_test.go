package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// --- §4.13 record completeness (REQ-API-171) ---

func completenessURL(projectID int64) string {
	return "/api/v1/projects/" + itoa(projectID) + "/record-completeness"
}

// mustDesignField stores one field as given — branching, annotation and
// choices included — the way a designer write would have left it.
func (e *env) mustDesignField(f db.Field) {
	e.t.Helper()
	if _, err := e.Store.AddField(context.Background(), &f); err != nil {
		e.t.Fatalf("AddField %s: %v", f.FieldName, err)
	}
}

func logic(expr string) sql.NullString { return sql.NullString{String: expr, Valid: true} }

func (e *env) completenessOf(projectID int64, actor *db.User) []completenessRow {
	e.t.Helper()
	rec := e.do("GET", completenessURL(projectID), nil, actor)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("record-completeness: %d %s", rec.Code, rec.Body.String())
	}
	var rows []completenessRow
	e.decode(rec, &rows)
	return rows
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestRecordCompleteness covers what a cell counts (REQ-API-171): enterable
// fields only — never the identifier, a calculated, @READONLY or description
// field; a checkbox once; a field hidden by branching toward neither side,
// though it stores a value; a malformed expression as shown; an instrument
// hidden by its own logic toward nothing. A bare [field] reads the cell's own
// event (DEV-VAL-13), so one instrument hides at one event and shows at the
// next. Only events holding a counted value are listed and summed.
func TestRecordCompleteness(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Completeness Study")
	armID := e.mustArm(projectID, 1)
	v1 := e.mustEvent(projectID, armID, "Visit 1", "v1_arm_1")
	v2 := e.mustEvent(projectID, armID, "Visit 2", "v2_arm_1")
	v3 := e.mustEvent(projectID, armID, "Visit 3", "v3_arm_1")
	intake := e.mustInstrument(projectID, "intake")
	pregnancy, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: projectID, Name: "pregnancy",
		Position: 2, BranchingLogic: logic(`[sex] = 'Female'`)})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	field := func(instrument int64, pos int, name, typ string, opts ...func(*db.Field)) {
		f := db.Field{ProjectID: projectID, InstrumentID: instrument, FieldName: name, FieldType: typ, Position: pos}
		for _, o := range opts {
			o(&f)
		}
		e.mustDesignField(f)
	}
	choices := func(c string) func(*db.Field) { return func(f *db.Field) { f.Choices = logic(c) } }
	branching := func(b string) func(*db.Field) { return func(f *db.Field) { f.BranchingLogic = logic(b) } }
	field(intake, 1, "record_id", "text") // the identifier (GD-8)
	field(intake, 2, "age", "text")
	field(intake, 3, "sex", "radio", choices("1$Male##2$Female"))
	field(intake, 4, "preg", "yesno", choices(validate.YesNoChoices), branching(`[sex] = "2"`))
	field(intake, 5, "cb", "checkbox", choices("1$A##2$B"))
	field(intake, 6, "odd", "text", branching(`[age] >`)) // stored malformed: shown
	field(intake, 7, "note", "notes", func(f *db.Field) { f.FieldAnnotation = logic("@READONLY") })
	field(intake, 8, "dbl", "calculated", func(f *db.Field) { f.Calculation = logic("[v1_arm_1][age] * 2") })
	field(intake, 9, "intro", "description")
	field(pregnancy, 1, "weeks", "text")
	e.mustMap(projectID, armID,
		db.InstrumentEvent{InstrumentID: intake, EventID: v1},
		db.InstrumentEvent{InstrumentID: pregnancy, EventID: v1},
		db.InstrumentEvent{InstrumentID: intake, EventID: v2},
		db.InstrumentEvent{InstrumentID: pregnancy, EventID: v2},
		db.InstrumentEvent{InstrumentID: intake, EventID: v3},
	)
	for _, r := range []string{"R1", "R2", "R3"} {
		e.mustRecord(projectID, r)
	}
	// R1 is male at v1, female at v2.
	for f, v := range map[string]string{"record_id": "R1", "age": "4711", "sex": "1", "cb___1": "1", "cb___2": "1",
		"preg": "1", "note": "ro", "dbl": "9422"} {
		e.mustValue(projectID, "R1", "v1_arm_1", "", f, v)
	}
	e.mustValue(projectID, "R1", "v1_arm_1", "", "weeks", "12") // a hidden instrument's value
	e.mustValue(projectID, "R1", "v2_arm_1", "", "sex", "2")
	// v3 holds only values the count leaves out: it is not reached.
	e.mustValue(projectID, "R1", "v3_arm_1", "", "note", "ro")
	e.mustValue(projectID, "R1", "v3_arm_1", "", "dbl", "1")
	// R3 holds its identifier and nothing else.
	e.mustValue(projectID, "R3", "v1_arm_1", "", "record_id", "R3")

	before := len(e.auditTypes())
	rec := e.do("GET", completenessURL(projectID), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("record-completeness: %d %s", rec.Code, rec.Body.String())
	}
	for _, v := range []string{"4711", "9422", `"ro"`} {
		if strings.Contains(rec.Body.String(), v) {
			t.Fatalf("response leaks the value %s: %s", v, rec.Body.String())
		}
	}
	var rows []completenessRow
	e.decode(rec, &rows)
	want := []completenessRow{
		{RecordID: "R1", Filled: 4, Visible: 10, Events: []completenessEvent{
			// v1: sex is Male here — preg and the pregnancy instrument hide. Of
			// age, sex, cb and odd, three hold a value.
			{UniqueEventName: "v1_arm_1", Hidden: []string{"pregnancy"}, Instruments: []completenessInstrument{
				{Name: "intake", Filled: 3, Visible: 4}}},
			// v2: [sex] now reads v2's Female — preg shows, and so does
			// pregnancy, which holds nothing here: counted, not listed.
			{UniqueEventName: "v2_arm_1", Hidden: []string{}, Instruments: []completenessInstrument{
				{Name: "intake", Filled: 1, Visible: 5}}},
		}},
		{RecordID: "R2", Events: []completenessEvent{}},
		{RecordID: "R3", Events: []completenessEvent{}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("completeness\n got %s\nwant %s", asJSON(rows), asJSON(want))
	}

	// A completeness read writes no audit row (ASM-AUD-2).
	if after := len(e.auditTypes()); after != before {
		t.Fatalf("record-completeness wrote audit entries: %v", e.auditTypes())
	}
	// The shared scope refuses as record-status does.
	if rec := e.do("GET", completenessURL(999999), nil, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: %d, want 404", rec.Code)
	}
	if rec := e.do("GET", completenessURL(projectID), nil, e.mustUser("out@example.org")); rec.Code != http.StatusForbidden {
		t.Fatalf("non-member: %d, want 403", rec.Code)
	}
}

// TestRecordCompletenessRepeating: each instance of a repeating instrument is
// a cell of its own, and logic reading its own instrument reads that
// instance (REQ-VAL-045). A further instance counts only while the pair
// repeats (REQ-DB-048).
func TestRecordCompletenessRepeating(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Repeating Completeness")
	armID := e.mustArm(projectID, 1)
	v1 := e.mustEvent(projectID, armID, "Visit 1", "v1_arm_1")
	intake := e.mustInstrument(projectID, "intake")
	visits := e.mustInstrument(projectID, "visits")
	e.mustDesignField(db.Field{ProjectID: projectID, InstrumentID: intake, FieldName: "record_id", FieldType: "text", Position: 1})
	e.mustDesignField(db.Field{ProjectID: projectID, InstrumentID: visits, FieldName: "kind", FieldType: "radio",
		Position: 1, Choices: logic("1$Phone##2$Clinic")})
	e.mustDesignField(db.Field{ProjectID: projectID, InstrumentID: visits, FieldName: "room", FieldType: "text",
		Position: 2, BranchingLogic: logic(`[kind] = 'Clinic'`)})
	e.mustMap(projectID, armID,
		db.InstrumentEvent{InstrumentID: intake, EventID: v1},
		db.InstrumentEvent{InstrumentID: visits, EventID: v1},
	)
	if rec := e.do("PUT", repeatingURL(projectID, visits), map[string]any{"events": []string{"v1_arm_1"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("designate: %d %s", rec.Code, rec.Body.String())
	}
	e.mustRecord(projectID, "R1")
	e.mustValue(projectID, "R1", "v1_arm_1", "", "record_id", "R1")
	e.mustInstanceValue(projectID, "R1", "v1_arm_1", "visits", 1, "kind", "1") // phone: no room
	e.mustInstanceValue(projectID, "R1", "v1_arm_1", "visits", 2, "kind", "2")
	e.mustInstanceValue(projectID, "R1", "v1_arm_1", "visits", 2, "room", "A1")
	e.mustInstanceValue(projectID, "R1", "v1_arm_1", "visits", 3, "kind", "2") // clinic, room missing

	rows := e.completenessOf(projectID, admin)
	want := `[{"record_id":"R1","filled":4,"visible":5,"events":[{"unique_event_name":"v1_arm_1","hidden":[],` +
		`"instruments":[{"name":"visits","filled":1,"visible":1,"instances":[` +
		`{"instance":1,"filled":1,"visible":1},{"instance":2,"filled":2,"visible":2},{"instance":3,"filled":1,"visible":2}]}]}]}]`
	if got := asJSON(rows); got != want {
		t.Fatalf("completeness\n got %s\nwant %s", got, want)
	}

	// Off: the further instances are hidden and count no more.
	if rec := e.do("PUT", repeatingURL(projectID, visits), map[string]any{"events": []string{}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("off: %d %s", rec.Code, rec.Body.String())
	}
	want = `[{"record_id":"R1","filled":1,"visible":1,"events":[{"unique_event_name":"v1_arm_1","hidden":[],` +
		`"instruments":[{"name":"visits","filled":1,"visible":1}]}]}]`
	if got := asJSON(e.completenessOf(projectID, admin)); got != want {
		t.Fatalf("completeness after off\n got %s\nwant %s", got, want)
	}
}

// TestRecordCompletenessReadsAsTheCallerMay: a branching reference to a pair
// the caller may not read is empty (REQ-API-171), so the count says nothing
// about it — here an instrument that shows for the administrator stays hidden
// for a member without access to the value it branches on.
func TestRecordCompletenessReadsAsTheCallerMay(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Restricted Completeness")
	armID := e.mustArm(projectID, 1)
	v1 := e.mustEvent(projectID, armID, "Visit 1", "v1_arm_1")
	v2 := e.mustEvent(projectID, armID, "Visit 2", "v2_arm_1")
	intake := e.mustInstrument(projectID, "intake")
	followup, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: projectID, Name: "followup",
		Position: 2, BranchingLogic: logic(`[v1_arm_1][sex] = "2"`)})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	e.mustDesignField(db.Field{ProjectID: projectID, InstrumentID: intake, FieldName: "record_id", FieldType: "text", Position: 1})
	e.mustDesignField(db.Field{ProjectID: projectID, InstrumentID: intake, FieldName: "sex", FieldType: "radio",
		Position: 2, Choices: logic("1$Male##2$Female")})
	e.mustDesignField(db.Field{ProjectID: projectID, InstrumentID: followup, FieldName: "outcome", FieldType: "text", Position: 1})
	e.mustMap(projectID, armID,
		db.InstrumentEvent{InstrumentID: intake, EventID: v1},
		db.InstrumentEvent{InstrumentID: followup, EventID: v2},
	)
	e.mustRecord(projectID, "R1")
	e.mustValue(projectID, "R1", "v1_arm_1", "", "sex", "2")
	e.mustValue(projectID, "R1", "v2_arm_1", "", "outcome", "well")

	roleID, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: projectID, RoleName: "blind"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}},
		[]db.RoleGrant{{EventID: v1, InstrumentID: intake, DataAccessLevel: "no_access", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	blind := e.mustUser("blind@example.org")
	e.mustMemberWithRole(projectID, blind, roleID)

	at := func(rows []completenessRow, event string) completenessEvent {
		t.Helper()
		for _, ev := range rows[0].Events {
			if ev.UniqueEventName == event {
				return ev
			}
		}
		t.Fatalf("event %s not listed: %s", event, asJSON(rows))
		return completenessEvent{}
	}
	if got := asJSON(at(e.completenessOf(projectID, admin), "v2_arm_1")); got !=
		`{"unique_event_name":"v2_arm_1","hidden":[],"instruments":[{"name":"followup","filled":1,"visible":1}]}` {
		t.Fatalf("administrator at v2: %s", got)
	}
	if got := asJSON(at(e.completenessOf(projectID, blind), "v2_arm_1")); got !=
		`{"unique_event_name":"v2_arm_1","hidden":["followup"],"instruments":[]}` {
		t.Fatalf("member without access to sex at v2: %s", got)
	}
}
