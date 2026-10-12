package admin

// Tests for the yesno and notes field types on the administration API
// (REQ-DB-043/044): the attributes a request cannot choose are fixed on every
// write path, before the direct-identifier preset reads the validation type.

import (
	"net/http"
	"testing"
)

func TestYesNoAndNotesFixTheirAttributes(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	pid := e.mustProject("Field Types")
	arm := e.mustArm(pid, 1)
	e.mustEvent(pid, arm, "baseline", "baseline_arm_1")
	inst := e.mustInstrument(pid, "intake")
	path := "/api/v1/projects/" + itoa(pid) + "/instruments/" + itoa(inst) + "/fields"
	e.do("POST", path, map[string]any{"field_name": "record_id", "field_type": "text"}, admin)

	create := func(body map[string]any) fieldObject {
		t.Helper()
		rec := e.do("POST", path, body, admin)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %v: %d %s", body["field_name"], rec.Code, rec.Body.String())
		}
		var f fieldObject
		e.decode(rec, &f)
		return f
	}

	// Supplied choices and an identifier-shaped validation type are dropped:
	// the coding is 1 = Yes, 0 = No, and no identifier preset follows.
	consent := create(map[string]any{"field_name": "consent", "field_type": "yesno",
		"choices": "1$Agree##2$Refuse", "validation_type": "email"})
	if consent.Choices != "1$Yes##0$No" || consent.ValidationType != "" || consent.DirectIdentifier {
		t.Fatalf("yesno = %+v, want choices 1$Yes##0$No, no validation type, no identifier preset", consent)
	}
	remarks := create(map[string]any{"field_name": "remarks", "field_type": "notes",
		"choices": "1$x", "validation_type": "integer", "validation_min": "0"})
	if remarks.Choices != "" || remarks.ValidationType != "" || remarks.ValidationMin != nil {
		t.Fatalf("notes = %+v, want no choices and no validation", remarks)
	}

	// The designer sends empty choices for a type without a choice editor: the
	// coding stays, and nothing is recorded as changed.
	rec := e.do("PUT", path+"/"+itoa(consent.ID), map[string]any{"choices": "", "field_label": "Consent given"}, admin)
	var updated fieldObject
	e.decode(rec, &updated)
	if rec.Code != http.StatusOK || updated.Choices != "1$Yes##0$No" {
		t.Fatalf("relabel yesno: %d %s", rec.Code, rec.Body.String())
	}
	var changes string
	e.Store.DB.QueryRow(`SELECT details FROM audit_events WHERE event_type = 'field_updated' ORDER BY id DESC LIMIT 1`).Scan(&changes)
	if changes == "" || contains(changes, `"choices"`) {
		t.Fatalf("field_updated details = %s, want a label change and no choices change", changes)
	}

	// A radio turned into yesno takes the fixed coding.
	smoker := create(map[string]any{"field_name": "smoker", "field_type": "radio", "choices": "1$Yes##2$No"})
	rec = e.do("PUT", path+"/"+itoa(smoker.ID), map[string]any{"field_type": "yesno"}, admin)
	e.decode(rec, &updated)
	if rec.Code != http.StatusOK || updated.FieldType != "yesno" || updated.Choices != "1$Yes##0$No" {
		t.Fatalf("radio → yesno: %d %s", rec.Code, rec.Body.String())
	}
}
