package admin

// Tests for the checkbox field type on the administration API (REQ-DB-042):
// design-time rules, and every place a stored value is traced back to its
// field — record status, history, survey re-issue, rename and delete — seeing
// the values a checkbox stores under its choice columns.

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"csms/api/internal/db"
)

// checkboxProject is one arm, one event, one instrument "habits_form" mapped to
// it, and a checkbox `habits` with choices 1 and 2 on that instrument.
type checkboxProject struct {
	projectID, armID, eventID, instID, fieldID int64
	fieldsPath                                 string
}

func newCheckboxProject(t *testing.T, e *env, admin *db.User) checkboxProject {
	t.Helper()
	var p checkboxProject
	p.projectID = e.mustProject("Checkbox Study")
	p.armID = e.mustArm(p.projectID, 1)
	p.eventID = e.mustEvent(p.projectID, p.armID, "baseline", "baseline_arm_1")
	p.instID = e.mustInstrument(p.projectID, "habits_form")
	e.mustMap(p.projectID, p.armID, db.InstrumentEvent{InstrumentID: p.instID, EventID: p.eventID})
	p.fieldsPath = "/api/v1/projects/" + itoa(p.projectID) + "/instruments/" + itoa(p.instID) + "/fields"
	e.do("POST", p.fieldsPath, map[string]any{"field_name": "record_id", "field_type": "text"}, admin)
	rec := e.do("POST", p.fieldsPath, map[string]any{
		"field_name": "habits", "field_type": "checkbox", "choices": "1$Smoking##2$Drinking",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkbox field: %d %s", rec.Code, rec.Body.String())
	}
	var f fieldObject
	e.decode(rec, &f)
	p.fieldID = f.ID
	return p
}

func dataRows(t *testing.T, e *env, projectID int64, field string) int {
	t.Helper()
	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM data WHERE project_id = ? AND field_name = ?`,
		projectID, field).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCheckboxFieldDesignRules(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p := newCheckboxProject(t, e, admin)

	rec := e.do("POST", p.fieldsPath, map[string]any{"field_name": "empty_box", "field_type": "checkbox"}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("checkbox without choices: %d %s, want 400", rec.Code, rec.Body.String())
	}
	// No field may take a name one of the checkbox's boxes already addresses…
	rec = e.do("POST", p.fieldsPath, map[string]any{"field_name": "habits___2", "field_type": "text"}, admin)
	if rec.Code != http.StatusConflict {
		t.Errorf("field named like a choice column: %d %s, want 409", rec.Code, rec.Body.String())
	}
	// …and no checkbox may expand onto an existing field.
	e.do("POST", p.fieldsPath, map[string]any{"field_name": "sport___1", "field_type": "text"}, admin)
	rec = e.do("POST", p.fieldsPath, map[string]any{
		"field_name": "sport", "field_type": "checkbox", "choices": "1$Running",
	}, admin)
	if rec.Code != http.StatusConflict {
		t.Errorf("checkbox whose box collides: %d %s, want 409", rec.Code, rec.Body.String())
	}
	// Adding a choice to the checkbox itself never collides with its own boxes.
	rec = e.do("PUT", p.fieldsPath+"/"+itoa(p.fieldID), map[string]any{"choices": "1$Smoking##2$Drinking##3$Sports"}, admin)
	if rec.Code != http.StatusOK {
		t.Errorf("add a choice: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCheckboxRecordStatusSeesBoxes(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p := newCheckboxProject(t, e, admin)
	e.mustRecord(p.projectID, "R1")
	e.mustValue(p.projectID, "R1", "baseline_arm_1", "", "habits___2", "1")

	rec := e.do("GET", "/api/v1/projects/"+itoa(p.projectID)+"/record-status", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("record-status: %d %s", rec.Code, rec.Body.String())
	}
	var rows []recordStatusRow
	e.decode(rec, &rows)
	if len(rows) != 1 || len(rows[0].Events) != 1 || len(rows[0].Events[0].Instruments) != 1 ||
		rows[0].Events[0].Instruments[0].Name != "habits_form" ||
		rows[0].Events[0].Instruments[0].State != StateSomeData {
		t.Fatalf("a checked box is data of its instrument (REQ-API-074): %+v", rows)
	}
}

func TestCheckboxHistoryFilterMatchesBoxes(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	entry := e.mustUser("entry@example.org")
	p := newCheckboxProject(t, e, admin)
	e.mustRecord(p.projectID, "R1")
	e.insertHistory(p.projectID, entry, "R1", "update", "habits_form", "baseline_arm_1",
		[]map[string]any{{"field": "habits___1", "old": nil, "new": "1"}})
	e.insertHistory(p.projectID, entry, "R1", "update", "habits_form", "baseline_arm_1",
		[]map[string]any{{"field": "record_id", "old": nil, "new": "R1"}})

	var page struct {
		Entries []historyEntry `json:"entries"`
	}
	rec := e.do("GET", e.historyPath(p.projectID, "R1")+"?field=habits", nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 1 || page.Entries[0].Fields[0].Field != "habits___1" {
		t.Fatalf("?field=habits: %s", rec.Body.String())
	}
	rec = e.do("GET", e.historyPath(p.projectID, "R1")+"?field=habits___2", nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 0 {
		t.Fatalf("?field=habits___2 matched another box: %s", rec.Body.String())
	}
}

func TestCheckboxSurveyReissueSeesBoxes(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newSurveyFixture(t, e)
	if _, err := e.Store.AddField(context.Background(), &db.Field{ProjectID: f.projectID, InstrumentID: f.survey,
		FieldName: "symptoms", FieldType: "checkbox", Position: 3,
		Choices: sql.NullString{String: "1$Cough##2$Fever", Valid: true}}); err != nil {
		t.Fatalf("AddField: %v", err)
	}
	path := surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1")
	e.mustValue(f.projectID, "R1", "v1_arm_1", "", "symptoms___2", "1")
	if rec := e.do("POST", path, nil, admin); rec.Code != http.StatusConflict {
		t.Errorf("issue over a checked box: got %d %s, want 409", rec.Code, rec.Body.String())
	}
}

func TestCheckboxRenameAndDeleteFollowTheBoxes(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p := newCheckboxProject(t, e, admin)
	e.mustRecord(p.projectID, "R1")
	e.mustValue(p.projectID, "R1", "baseline_arm_1", "", "habits___1", "1")

	// An expression on another instrument reading one box holds the checkbox
	// and its instrument back (§6.2).
	scores := e.mustInstrument(p.projectID, "scores")
	e.mustMap(p.projectID, p.armID,
		db.InstrumentEvent{InstrumentID: p.instID, EventID: p.eventID},
		db.InstrumentEvent{InstrumentID: scores, EventID: p.eventID})
	scoresPath := "/api/v1/projects/" + itoa(p.projectID) + "/instruments/" + itoa(scores) + "/fields"
	rec := e.do("POST", scoresPath, map[string]any{
		"field_name": "smoker", "field_type": "calculated", "calculation": "[baseline_arm_1][habits___1] * 1",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("calc over a box: %d %s", rec.Code, rec.Body.String())
	}
	var calc fieldObject
	e.decode(rec, &calc)
	if rec = e.do("DELETE", p.fieldsPath+"/"+itoa(p.fieldID), nil, admin); rec.Code != http.StatusConflict {
		t.Fatalf("delete a checkbox an expression reads: %d, want 409", rec.Code)
	}
	if rec = e.do("DELETE", "/api/v1/projects/"+itoa(p.projectID)+"/instruments/"+itoa(p.instID), nil, admin); rec.Code != http.StatusConflict {
		t.Fatalf("delete the instrument while an expression reads its box: %d, want 409", rec.Code)
	}

	// A rename carries the boxes' values and dependency rows along (REQ-VAL-014).
	rec = e.do("PUT", p.fieldsPath+"/"+itoa(p.fieldID), map[string]any{"field_name": "vices"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	if dataRows(t, e, p.projectID, "vices___1") != 1 || dataRows(t, e, p.projectID, "habits___1") != 0 {
		t.Fatal("box values did not follow the rename")
	}
	var deps int
	e.Store.DB.QueryRow(`SELECT COUNT(*) FROM calculated_dependencies WHERE project_id = ? AND ref_field_name = 'vices___1'`,
		p.projectID).Scan(&deps)
	if deps != 1 {
		t.Fatalf("dependency rows on vices___1 = %d, want 1", deps)
	}

	// With the expression gone, the instrument's delete takes the boxes too.
	if rec = e.do("DELETE", scoresPath+"/"+itoa(calc.ID), nil, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete calc: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do("DELETE", "/api/v1/projects/"+itoa(p.projectID)+"/instruments/"+itoa(p.instID), nil, admin); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("delete instrument: %d %s", rec.Code, rec.Body.String())
	}
	if n := dataRows(t, e, p.projectID, "vices___1"); n != 0 {
		t.Fatalf("instrument delete left %d box values behind", n)
	}
}
