// Free-form text content policy (Data_Validation_Design.md §5, REQ-VAL-030/031).
//
// One implementation of the §5.1 pipeline, called at every entry point that
// stores a value so the three paths — data API import, UI data entry through PHP,
// survey link submission — cannot drift apart (REQ-VAL-001/004). It runs after the
// type validator and before the write. It refuses what cannot be stored as sent —
// invalid UTF-8, a control character, a value over the length cap — and otherwise
// stores the value as received: there is no HTML allowlist at storage any more
// (DEV-VAL-15). A REDCap client reads back what it wrote, and the defence against
// stored markup is at output, where the web application escapes every value
// (REQ-VAL-031, REQ-TECH-020).
//
// The policy is deterministic and stateless: no per-request state, so two calls
// with one input store one output and an audit comparison stays meaningful.

package validate

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ApplyContentPolicy runs the §5.1 pipeline over one value of field f: UTF-8
// validity, control characters with CR normalized to LF, and the optional length
// cap. Each step rejects (a per-field CONTENT_INVALID error, all-or-nothing per
// record); a value that passes is stored as received, CR normalization aside —
// markup included, since nothing renders a stored value as HTML (DEV-VAL-15).
//
// maxBytes is MAX_VALUE_BYTES: 0 means no application cap and the storage type is
// the bound (REQ-VAL-021, ASM-VAL-2). A value is measured after CR normalization,
// since step 2 runs first and the stored string is what has to fit.
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
	return value, nil
}
