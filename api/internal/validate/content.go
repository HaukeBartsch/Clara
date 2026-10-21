// Free-form text content policy (Data_Validation_Design.md §5, REQ-VAL-030/031).
//
// One implementation of the §5.1 pipeline and the §5.2 HTML allowlist, called at
// every entry point that stores a free-text value so the three paths — data API
// import, UI data entry through PHP, survey link submission — cannot drift apart
// (REQ-VAL-001/004). It runs after the type validator and before the write; the
// stored string is the result, which is what lets REQ-VAL-031 render free text as
// allowlist HTML anywhere in the UI without a second sanitizer on the read side.
//
// The policy is deterministic and stateless: no per-request state, so two calls
// with one input store one output and an audit comparison stays meaningful.

package validate

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
)

// freeTextTypes are the field types whose values are free-form text: a `text`
// field with no validation type, and every `notes` field (§5, REQ-DB-044). The
// PHP layer answers the same question in DataEntry::isFreeText() to decide
// whether a value renders escaped or as allowlist HTML — the two must agree, or
// a value stored sanitized would be rendered escaped (a cosmetic bug) or, worse,
// a value stored raw would be rendered as markup (the bug this file exists to
// close).
func IsFreeText(f Field) bool {
	return f.Type == "notes" || (f.Type == "text" && f.ValidationType == "")
}

// allowedElements is the final §5.2 element list (resolves ASM-VAL-5). Adding an
// element here is a design change, not an implementation detail.
var allowedElements = []string{
	"a", "b", "br", "code", "em", "i", "li", "ol", "p", "s", "strong", "u", "ul", "blockquote",
}

// executableElements are the disallowed elements that carry executable or
// external content: §5.2 step 2 removes them together with everything inside
// them, so `<script>steal()</script>` leaves no text behind. Kept separate from
// the merely-presentational list because preserving a script's text would put an
// attacker's words into a clinical record looking like plain prose.
var executableElements = []string{
	"script", "style", "noscript", "iframe", "frame", "object", "embed", "applet",
	"link", "meta", "base", "form", "input", "button", "select", "textarea",
}

// contentSanitizer implements §5.2: the allowlist above, no attributes at all
// except an `href` on `a` whose scheme is http, https or mailto. A relative URL
// has no allowed scheme, so its href is dropped and the link text is kept — that
// is what makes `<a href="javascript:alert(1)">x</a>` store as `x`. Everything
// else is either flattened to text (span, div, table, h1…) or deleted with its
// content. bluemonday parses tolerantly and re-escapes on serialization, which is
// §5.2 step 3: the stored string is inert. Built once — a Policy is safe for
// concurrent use, and rebuilding it per value would dominate the cost of storing
// a note.
var contentSanitizer = newContentSanitizer()

func newContentSanitizer() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(allowedElements...)
	p.AllowURLSchemes("http", "https", "mailto")
	p.AllowAttrs("href").OnElements("a")
	// §5.2 step 2, second half: delete these and their content outright rather
	// than flattening them — the default for an unlisted element is to strip the
	// tag and keep the text, which is right for a <div> and wrong for a <form>.
	p.SkipElementsContent(executableElements...)
	return p
}

// ApplyContentPolicy runs the §5.1 pipeline over one value of field f: UTF-8
// validity, control characters with CR normalized to LF, the optional length cap,
// then HTML sanitization. Steps 1–3 reject (a per-field CONTENT_INVALID error,
// all-or-nothing per record like step 7); step 4 never errors, it transforms.
//
// maxBytes is MAX_VALUE_BYTES: 0 means no application cap and the storage type is
// the bound (REQ-VAL-021, ASM-VAL-2). A value is measured after CR normalization,
// since step 2 runs first and the stored string is what has to fit.
//
// Values of fields outside §5's scope come back untouched — a date or an integer
// has already been matched against its grammar by ValidateValue and holds no
// markup to remove.
func ApplyContentPolicy(f Field, value string, maxBytes int) (string, *Violation) {
	if value == "" {
		return value, nil // "no value" is not a content question (REQ-VAL-024)
	}

	// Step 1 — valid UTF-8.
	if !utf8.ValidString(value) {
		return "", &Violation{CodeContentInvalid,
			fmt.Sprintf("value of field %q is not valid UTF-8", f.Name)}
	}

	// Step 2 — CR and CRLF become LF; any other C0 control character is a
	// rejection rather than something to strip silently, because a value that
	// arrived with NULs or escapes in it did not arrive the way the client meant
	// it to. Tab and LF stay: free text is multi-line by nature.
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if i := strings.IndexFunc(value, func(r rune) bool { return r < 0x20 && r != '\t' && r != '\n' }); i >= 0 {
		return "", &Violation{CodeContentInvalid,
			fmt.Sprintf("value of field %q contains control character U+%04X", f.Name, value[i])}
	}

	// Step 3 — the configured length cap, when there is one.
	if maxBytes > 0 && len(value) > maxBytes {
		return "", &Violation{CodeContentInvalid,
			fmt.Sprintf("value of field %q is %d bytes, the limit is %d", f.Name, len(value), maxBytes)}
	}

	// Step 4 — sanitize free-form text to the §5.2 allowlist. The result is
	// trimmed: stripping markup leaves stray whitespace at the edges (`a &amp; b
	// <script>bad()</script>` stores as `a &amp; b`, not `a &amp; b `), and §5.2
	// asks for one canonical stored string so an unchanged value compares equal
	// instead of churning record history over invisible bytes.
	if IsFreeText(f) {
		value = strings.TrimSpace(contentSanitizer.Sanitize(value))
	}
	return value, nil
}
