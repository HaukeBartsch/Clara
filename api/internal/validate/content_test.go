package validate

import (
	"strings"
	"testing"
)

// §5.1 steps 1–3 reject with CONTENT_INVALID; step 4 never does.
func TestContentPipelineRejections(t *testing.T) {
	notes := Field{Name: "clinical_notes", Type: "notes"}
	t.Run("invalid UTF-8", func(t *testing.T) {
		if _, v := ApplyContentPolicy(notes, "ok\xff\xfe", 0); v == nil || v.Code != CodeContentInvalid {
			t.Errorf("want CONTENT_INVALID for invalid UTF-8, got %v", v)
		}
	})
	t.Run("NUL rejected", func(t *testing.T) {
		if _, v := ApplyContentPolicy(notes, "a\x00b", 0); v == nil || v.Code != CodeContentInvalid {
			t.Errorf("want CONTENT_INVALID for a NUL byte, got %v", v)
		}
	})
	t.Run("escape rejected", func(t *testing.T) {
		if _, v := ApplyContentPolicy(notes, "a\x1bb", 0); v == nil || v.Code != CodeContentInvalid {
			t.Errorf("want CONTENT_INVALID for ESC, got %v", v)
		}
	})
	t.Run("tab and LF kept", func(t *testing.T) {
		if _, v := ApplyContentPolicy(notes, "a\tb\nc", 0); v != nil {
			t.Errorf("tab and line feed must be accepted, got %v", v)
		}
	})
	t.Run("length cap only when configured", func(t *testing.T) {
		long := strings.Repeat("x", 5000)
		if _, v := ApplyContentPolicy(notes, long, 0); v != nil {
			t.Errorf("no cap configured (0) must accept any length, got %v", v)
		}
		if _, v := ApplyContentPolicy(notes, long, 4096); v == nil || v.Code != CodeContentInvalid {
			t.Errorf("want CONTENT_INVALID above a 4096-byte cap, got %v", v)
		}
		if _, v := ApplyContentPolicy(notes, strings.Repeat("x", 4096), 4096); v != nil {
			t.Errorf("a value exactly at the cap must be accepted, got %v", v)
		}
	})
	t.Run("empty value is no value", func(t *testing.T) {
		got, v := ApplyContentPolicy(notes, "", 0)
		if v != nil || got != "" {
			t.Errorf(`empty value must pass through untouched, got %q / %v`, got, v)
		}
	})
}

// §5.1 step 2 normalizes CR and CRLF to LF before the value is stored.
func TestCarriageReturnNormalized(t *testing.T) {
	notes := Field{Name: "clinical_notes", Type: "notes"}
	cases := []struct{ in, want string }{
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\r\n\r\nb", "a\n\nb"},
	}
	for _, c := range cases {
		got, v := ApplyContentPolicy(notes, c.in, 0)
		if v != nil {
			t.Errorf("ApplyContentPolicy(%q) = %v, want no violation", c.in, v)
			continue
		}
		if got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A structured or choice value is returned untouched, like free text: the policy
// refuses or passes a value, it never rewrites one (DEV-VAL-15) — a legitimate
// value may well contain a less-than sign or an ampersand.
func TestNonFreeTextUntouched(t *testing.T) {
	f := Field{Name: "dob", Type: "text", ValidationType: "date"}
	for _, in := range []string{"2026-10-20", "<not markup>", `a & b`} {
		if got, _ := ApplyContentPolicy(f, in, 0); got != in {
			t.Errorf("ApplyContentPolicy on %q = %q, want it untouched", in, got)
		}
	}
}

// Free text is stored as received (DEV-VAL-15): the values the FIONA test run saw
// rewritten — a DICOM series description in angle brackets, quotes, an
// ampersand — and markup alike come back unchanged, as a REDCap client reads
// them. Nothing renders a stored value as HTML, so markup stays inert text
// (REQ-VAL-031).
func TestFreeTextStoredVerbatim(t *testing.T) {
	for _, f := range []Field{{Name: "series_description", Type: "text"}, {Name: "clinical_notes", Type: "notes"}} {
		for _, in := range []string{
			"<MPR Thick Range>",
			`{"uid": "1.2.3", "note": "a & b"}`,
			"a &amp; b",
			"hello <b>world</b> <script>x()</script>",
			"  leading and trailing  ",
		} {
			got, v := ApplyContentPolicy(f, in, 0)
			if v != nil || got != in {
				t.Errorf("%s: ApplyContentPolicy(%q) = %q, %v; want it stored as received", f.Type, in, got, v)
			}
		}
	}
}
