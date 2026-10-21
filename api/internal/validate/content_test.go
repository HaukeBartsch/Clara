package validate

import (
	"strings"
	"testing"
)

// The §5.2 worked examples, transcribed from Data_Validation_Design.md: these are
// the normative answers, so they are asserted exactly and any sanitizer change
// that moves one of them is a design change.
func TestSanitizeWorkedExamples(t *testing.T) {
	notes := Field{Name: "clinical_notes", Type: "notes"}
	cases := []struct{ in, want string }{
		{"hello <b>world</b>", "hello <b>world</b>"},
		{`<div>note: <a href="https://x.no">link</a></div>`, `note: <a href="https://x.no">link</a>`},
		{`<a href="javascript:alert(1)">x</a>`, "x"},
		{"<script>steal()</script>ok", "ok"},
		{"<img src=x onerror=alert(1)>", ""},
		{`a &amp; b <script>bad()</script>`, "a &amp; b"},
	}
	for _, c := range cases {
		got, v := ApplyContentPolicy(notes, c.in, 0)
		if v != nil {
			t.Errorf("ApplyContentPolicy(%q) = violation %v, want none", c.in, v)
			continue
		}
		if got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// §5.2 attribute rule: href survives only as an absolute URI on an allowed
// scheme; every other attribute on every element is dropped, including the event
// handlers that make stripped-markup injection interesting.
func TestSanitizeAttributes(t *testing.T) {
	notes := Field{Name: "clinical_notes", Type: "notes"}
	cases := []struct{ in, want string }{
		{`<a href="mailto:a@b.no">write</a>`, `<a href="mailto:a@b.no">write</a>`},
		{`<a href="https://x.no/a?b=1&amp;c=2">q</a>`, `<a href="https://x.no/a?b=1&amp;c=2">q</a>`},
		// A relative URL has no scheme to check, so the attribute goes and the
		// text stays.
		{`<a href="/admin/users">x</a>`, "x"},
		{`<a href="//evil.no/x" target="_blank">x</a>`, "x"},
		// Attributes on allowed elements that are not href are dropped…
		{`<p class="x" onclick="steal()">text</p>`, "<p>text</p>"},
		// …and an id cannot be used to clobber the page's own DOM targets.
		{`<b id="clara-record-form">x</b>`, "<b>x</b>"},
	}
	for _, c := range cases {
		if got, _ := ApplyContentPolicy(notes, c.in, 0); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// §5.2 distinguishes disallowed elements that carry display text (flatten, keep
// the words) from those carrying executable or external content (delete with
// their content). Both directions matter: flattening a <form> would leave its
// labels as prose, and deleting a <div>'s content would silently eat a note.
func TestSanitizeElementClasses(t *testing.T) {
	notes := Field{Name: "clinical_notes", Type: "notes"}
	cases := []struct{ in, want string }{
		{"<span>flat</span>", "flat"},
		{"<h2>head</h2>", "head"},
		{"<table><tr><td>cell</td></tr></table>", "cell"},
		{"<div><b>kept</b> tail</div>", "<b>kept</b> tail"},
		{"<style>b{}</style>after", "after"},
		{"<iframe src=\"https://x.no\">fallback</iframe>", ""},
		{"<form><input value=\"v\"><button>go</button></form>", ""},
		// An executable element nested inside an allowed one is still deleted, and
		// the surrounding text survives.
		{"<p>before <script>bad()</script> after</p>", "<p>before  after</p>"},
	}
	for _, c := range cases {
		if got, _ := ApplyContentPolicy(notes, c.in, 0); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Sanitizing an already-sanitized value must be a no-op: transfers and staged
// recomputes can hand a stored value back through the policy, and a policy that
// changed it on every pass would make record history churn over identical data.
func TestSanitizeIsIdempotent(t *testing.T) {
	notes := Field{Name: "clinical_notes", Type: "notes"}
	for _, in := range []string{
		`hello <b>world</b> <a href="https://x.no">link</a> a &amp; b`,
		`<div><p>one</p><ul><li>two</li></ul></div>`,
		`<script>x</script>plain`,
	} {
		once, _ := ApplyContentPolicy(notes, in, 0)
		twice, _ := ApplyContentPolicy(notes, once, 0)
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}

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

// Only a `text` field with no validation type and every `notes` field are in the
// policy's scope; a validated text field has already been matched against its
// grammar, and choice/matrix/calculated values hold no free text (§5).
func TestIsFreeText(t *testing.T) {
	cases := []struct {
		f    Field
		want bool
	}{
		{Field{Type: "notes"}, true},
		{Field{Type: "text", ValidationType: ""}, true},
		{Field{Type: "text", ValidationType: "date"}, false},
		{Field{Type: "text", ValidationType: "email"}, false},
		{Field{Type: "dropdown"}, false},
		{Field{Type: "calculated"}, false},
	}
	for _, c := range cases {
		if got := IsFreeText(c.f); got != c.want {
			t.Errorf("IsFreeText(%+v) = %v, want %v", c.f, got, c.want)
		}
	}
}

// A structured or choice value is returned untouched — sanitizing it would be a
// no-op at best and could corrupt a legitimate value that happens to contain a
// less-than sign.
func TestNonFreeTextUntouched(t *testing.T) {
	f := Field{Name: "dob", Type: "text", ValidationType: "date"}
	for _, in := range []string{"2026-10-20", "<not markup>", `a & b`} {
		if got, _ := ApplyContentPolicy(f, in, 0); got != in {
			t.Errorf("ApplyContentPolicy on %q = %q, want it untouched", in, got)
		}
	}
}
