package dataapi

import (
	"net/url"
	"testing"
)

// The §5.1 content policy on the real write path: every entry path (data API
// import, UI data entry through PHP, survey link submission) arrives at this one
// import handler, so what a stored free-text value holds is decided here. The
// fixture's `notes` field is a `text` field with no validation type — free-form
// text under §5.

func importNote(t *testing.T, f *recordFixture, recordID, value string) importRow {
	t.Helper()
	rows := f.importRows(t, url.Values{
		"token": {"tok-edit"}, "content": {"record"}, "action": {"import"}, "returnFormat": {"json"},
		"data[0][record_id]":  {recordID},
		"data[0][form_name]":  {"demo"},
		"data[0][event_name]": {"baseline_arm_1"},
		"data[0][notes]":      {value},
	})
	return rows[0]
}

// Free text is stored and exported exactly as imported (REQ-VAL-030, DEV-VAL-15):
// the values the FIONA test run saw rewritten — `"` stored as &#34;, `&` as
// &amp;, a DICOM series description `<MPR Thick Range>` stored empty — and markup
// of any kind come back byte for byte, as a REDCap client reads them. Markup is
// inert because nothing renders a stored value as HTML: the web application
// escapes every value (REQ-VAL-031), which tests/php/rendering_test.php holds.
func TestImportStoresFreeTextAsReceived(t *testing.T) {
	f := newRecordFixture(t)
	values := []string{
		"<MPR Thick Range>",
		`{"StudyDescription": "MR \"knee\"", "note": "a & b"}`,
		"a &amp; b",
		"hello <b>world</b>",
		`<script src="https://evil.no/a.js"></script>note`,
		`<a href="javascript:alert(1)">x</a>`,
	}
	for i, in := range values {
		rec := "8DISC0" + string(rune('A'+i))
		if res := importNote(t, f, rec, in); res.ImportRecordID == importInvalid {
			t.Fatalf("import %q refused: %+v", in, res)
		}
		if got := storedValue(t, f, rec, "baseline_arm_1", "notes"); got != in {
			t.Errorf("import %q stored %q, want it as received", in, got)
		}
		rows := f.exportJSON(t, "tok-admin", url.Values{"records[0]": {rec}, "fields[0]": {"notes"}})
		if len(rows) != 1 || rows[0]["notes"] != in {
			t.Errorf("export of %q = %v, want the value as imported", in, rows)
		}
	}
}

// §5.1 steps 1–3 reject instead of transforming: a value with a control character
// in it did not arrive the way the client meant it to, so the row is refused rather
// than silently cleaned.
func TestImportRejectsControlCharacters(t *testing.T) {
	f := newRecordFixture(t)
	res := importNote(t, f, "8DISC099", "note\x00with NUL")
	if res.ImportRecordID != 0 {
		t.Errorf("row with a NUL byte = %v, want it refused (ImportRecordID 0)", res)
	}
}

// Tab and line feed are legitimate in multi-line free text (§5.1 step 2 excepts
// them), and CR is normalized rather than rejected — the shape a Windows-origin
// clinical note arrives in.
func TestImportAcceptsOrdinaryWhitespace(t *testing.T) {
	f := newRecordFixture(t)
	importNote(t, f, "8DISC098", "line one\r\nline two\tindented")
	if got := storedValue(t, f, "8DISC098", "baseline_arm_1", "notes"); got != "line one\nline two\tindented" {
		t.Errorf("stored note = %q, want CR normalized to LF with tab kept", got)
	}
}

// A validated field is stored as received too: `age` is an integer and
// `contact_email` an email, and neither is rewritten (REQ-VAL-030, DEV-VAL-15).
func TestImportLeavesStructuredFieldsAlone(t *testing.T) {
	f := newRecordFixture(t)
	rows := f.importRows(t, url.Values{
		"token": {"tok-edit"}, "content": {"record"}, "action": {"import"}, "returnFormat": {"json"},
		"data[0][record_id]":     {"8DISC097"},
		"data[0][form_name]":     {"demo"},
		"data[0][event_name]":    {"baseline_arm_1"},
		"data[0][age]":           {"42"},
		"data[0][contact_email]": {"a@b.no"},
	})
	res := rows[0]
	if res.ImportRecordID == 0 {
		t.Fatalf("structured import refused: %v", res)
	}
	if got := storedValue(t, f, "8DISC097", "baseline_arm_1", "age"); got != "42" {
		t.Errorf("stored age = %q, want 42", got)
	}
	if got := storedValue(t, f, "8DISC097", "baseline_arm_1", "contact_email"); got != "a@b.no" {
		t.Errorf("stored email = %q, want a@b.no", got)
	}
}
