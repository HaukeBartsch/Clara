package dataapi

// REDCap names an instrument in lower case with an underscore where this system
// stores the label's capitals and spaces: the imported FIONA dictionary holds the
// instruments "Incoming Studies", "Transfers" and "RewritePixelExclusions", while
// its recorded calls ask `forms[0]=incoming_studies` and `forms[2]=rewritepixelexclusions`
// (VISION_AND_REQUIREMENTS.md, REQ-API-037). A forms[] entry is therefore read in
// both spellings (redcapFormName). Covered here: the matching rule itself, then
// every surface that applies it — content=metadata and content=exportFieldNames
// (REQ-API-022), the record export (REQ-API-024), and a survey link's naming test
// for its own instrument (§3.10, REQ-API-083).

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"csms/api/internal/db"
)

// The rule, without a store: each entry selects an instrument by its stored name
// or by REDCap's spelling of it, in either direction; nothing else is selected,
// and no filter selects everything.
func TestFormSetMatchesBothSpellings(t *testing.T) {
	if toFormSet(nil) != nil || toFormSet([]string{}) != nil {
		t.Errorf("an absent forms[] must be the nil filter")
	}
	if !toFormSet(nil).has("whatever the project calls it") {
		t.Errorf("the nil filter must select every instrument")
	}

	sel := toFormSet([]string{"incoming_studies"})
	for _, name := range []string{"Incoming Studies", "incoming_studies", "INCOMING_STUDIES", "incoming studies"} {
		if !sel.has(name) {
			t.Errorf("forms[]=incoming_studies does not select %q", name)
		}
	}
	for _, name := range []string{"Series", "Transfers", "incoming", "incoming_studies_and_transfers", "incoming-studies"} {
		if sel.has(name) {
			t.Errorf("forms[]=incoming_studies selects %q", name)
		}
	}

	// The rule runs both ways, and an exact entry keeps matching exactly as it
	// always did.
	exact := toFormSet([]string{"Incoming Studies"})
	if !exact.has("Incoming Studies") || !exact.has("incoming_studies") {
		t.Errorf("forms[]=Incoming Studies must select the instrument under either spelling")
	}

	// Two entries select two instruments, one per spelling.
	mixed := toFormSet([]string{"incoming_studies", "Transfers"})
	if !mixed.has("Incoming Studies") || !mixed.has("Transfers") || mixed.has("Series") {
		t.Errorf("forms[]=incoming_studies&forms[]=Transfers selects the wrong set")
	}
}

// renameInstrument renames one instrument of a seeded project, which is how a
// dictionary imported from FIONA spells it: an instrument label with capitals and
// a space, where the caller sends REDCap's name.
func renameInstrument(t *testing.T, s *db.Store, pid int64, from, to string) {
	t.Helper()
	ctx := context.Background()
	ins, err := s.GetInstrumentByName(ctx, pid, from)
	if err != nil || ins == nil {
		t.Fatalf("GetInstrumentByName(%q): %v", from, err)
	}
	ins.Name = to
	if err := s.UpdateInstrument(ctx, ins); err != nil {
		t.Fatalf("UpdateInstrument(%q → %q): %v", from, to, err)
	}
}

// The record export, content=metadata and content=exportFieldNames answer a
// forms[] call in REDCap's spelling exactly as they answer the stored name: the
// filter selects instruments, and an instrument whose name has capitals and a
// space is no longer unreachable (REQ-API-024, REQ-API-022).
func TestRedcapFormNameSelectsTheSameRows(t *testing.T) {
	f := newRecordFixture(t)
	renameInstrument(t, f.s, f.pid, "calc", "Incoming Studies")

	// The export: same rows, same columns — including the completion column, which
	// carries the instrument's own name.
	redcap := f.exportJSON(t, "tok-admin", url.Values{"forms": {"incoming_studies"}})
	exact := f.exportJSON(t, "tok-admin", url.Values{"forms": {"Incoming Studies"}})
	if len(redcap) == 0 {
		t.Fatalf("forms[]=incoming_studies exported no rows")
	}
	if len(redcap) != len(exact) {
		t.Fatalf("rows = %d under the REDCap spelling, %d under the stored name", len(redcap), len(exact))
	}
	for i := range redcap {
		// The completion column carries the instrument's own name; at an event the
		// instrument is not mapped to its cell is empty (REQ-API-134).
		if _, ok := redcap[i]["Incoming Studies_complete"]; !ok {
			t.Errorf("row %d lost its completion column: %v", i, redcap[i])
		}
		delete(redcap[i], "redcap_event_name")
		delete(exact[i], "redcap_event_name")
		if len(redcap[i]) != len(exact[i]) {
			t.Errorf("row %d columns = %v, want %v", i, redcap[i], exact[i])
			continue
		}
		for k, v := range redcap[i] {
			if exact[i][k] != v {
				t.Errorf("row %d column %q = %q under the REDCap spelling, %q under the stored name", i, k, v, exact[i][k])
			}
		}
	}
	// The filter still filters: an instrument it did not name contributes nothing.
	if _, leaked := redcap[0]["age"]; leaked {
		t.Errorf("forms[]=incoming_studies exported demo's age column: %v", redcap[0])
	}

	for _, tc := range []struct{ content, form string }{
		{"metadata", "incoming_studies"},
		{"exportFieldNames", "incoming_studies"},
	} {
		code, body := f.call(t, url.Values{
			"token": {"tok-admin"}, "content": {tc.content}, "returnFormat": {"json"},
			"forms": {tc.form},
		})
		mustStatus(t, code, http.StatusOK, body)
		rows := decodeJSON(t, body)
		if len(rows) != 1 {
			t.Fatalf("%s over forms[]=%s returned %d rows, calc's one field: %v", tc.content, tc.form, len(rows), rows)
		}
		if rows[0]["form_name"] != "Incoming Studies" {
			t.Errorf("%s form_name = %v, want the stored instrument name", tc.content, rows[0]["form_name"])
		}
	}

	// A name the project does not hold still selects nothing, in either spelling.
	code, body := f.call(t, url.Values{
		"token": {"tok-admin"}, "content": {"exportFieldNames"}, "returnFormat": {"json"},
		"forms": {"no_such_instrument"},
	})
	mustStatus(t, code, http.StatusOK, body)
	if rows := decodeJSON(t, body); len(rows) != 0 {
		t.Errorf("an unknown forms[] name selected %d fields: %v", len(rows), rows)
	}
}

// A survey link's forms[] naming test reads both spellings too: a caller that
// knows the instrument as `incoming_studies` is naming the very instrument the
// link was issued for, and is still confined to it (REQ-API-083).
func TestSurveyLinkAcceptsRedcapFormName(t *testing.T) {
	f := newRecordFixture(t)
	renameInstrument(t, f.s, f.pid, "demo", "Incoming Studies")
	ctx := context.Background()
	ins, err := f.s.GetInstrumentByName(ctx, f.pid, "Incoming Studies")
	if err != nil || ins == nil {
		t.Fatalf("GetInstrumentByName: %v", err)
	}
	ev, err := f.s.GetEventByUniqueName(ctx, f.pid, "baseline_arm_1")
	if err != nil || ev == nil {
		t.Fatalf("GetEventByUniqueName: %v", err)
	}
	link := &db.SurveyLink{ProjectID: f.pid, RecordID: "8DISC001", InstrumentID: ins.ID, EventID: ev.ID}
	if _, err := f.s.CreateSurveyLink(ctx, link); err != nil {
		t.Fatalf("CreateSurveyLink: %v", err)
	}

	code, body := f.call(t, url.Values{
		"token": {link.Token}, "content": {"metadata"}, "returnFormat": {"json"},
		"forms[0]": {"incoming_studies"},
	})
	mustStatus(t, code, http.StatusOK, body)
	rows := decodeJSON(t, body)
	if len(rows) != 7 { // the instrument's seven fields; calc's total stays unseen
		t.Fatalf("metadata rows = %d, want 7: %v", len(rows), rows)
	}
	for _, r := range rows {
		if r["form_name"] != "Incoming Studies" {
			t.Errorf("metadata leaked form_name = %v for field %v", r["form_name"], r["field_name"])
		}
	}

	// Naming another instrument alongside its REDCap name still reaches past the
	// link, and a name that is not the link's instrument in either spelling stays
	// a denial.
	for _, reaching := range [][2]string{
		{"incoming_studies", "calc"},
		{"calc", "incoming_studies"},
		{"Incoming_Studies_Alias", ""},
	} {
		form := url.Values{"token": {link.Token}, "content": {"metadata"}}
		for i, name := range reaching {
			if name == "" {
				continue
			}
			form.Set("forms["+strconv.Itoa(i)+"]", name)
		}
		if code, _ := f.call(t, form); code != http.StatusForbidden {
			t.Errorf("forms[]=%v answered %d, want 403", reaching, code)
		}
	}
}
