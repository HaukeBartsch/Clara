package validate

import (
	"regexp"
	"strings"
)

// ChoiceCodeRE is the grammar of a choice code (REQ-VAL-022): letters, digits,
// `-` and `_` — REDCap projects use text codes such as PECTMRI or ECTMRI-DEID.
// CheckboxCodeRE is the stricter rule for a checkbox, whose codes become part of
// a value-column name `<field>___<code>` that must itself be a valid field name
// (REQ-DB-042, REQ-VAL-011).
var (
	ChoiceCodeRE   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	CheckboxCodeRE = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// NormalizeChoices returns a choice list in the stored code$label##code$label
// encoding (Data_Validation_Design.md §9). A value that already parses as that
// encoding is returned unchanged; any other is read as REDCap's form
// "code, label | code, label" — split on | between choices and on the first
// comma inside a choice, code and label trimmed (a choice without a comma is
// its own label). The two cannot be confused: a REDCap list's first code is
// followed by a comma, and no code may contain $, a comma or a space. The
// result is validated by the design-time rules like any stored encoding.
//
// A value that is neither form comes back unchanged, so the design-time rules
// report it against the stored encoding rather than against a conversion.
func NormalizeChoices(s string) string {
	if strings.TrimSpace(s) == "" || isStoredChoices(s) || !isREDCapChoices(s) {
		return s
	}
	var parts []string
	for _, choice := range strings.Split(s, "|") {
		choice = strings.TrimSpace(choice)
		if choice == "" {
			continue
		}
		code, label, found := strings.Cut(choice, ",")
		code = strings.TrimSpace(code)
		label = strings.TrimSpace(label)
		if !found {
			label = code
		}
		parts = append(parts, code+"$"+label)
	}
	return strings.Join(parts, "##")
}

// isREDCapChoices reports whether every |-separated choice starts with a
// valid code — the part before its first comma, or the whole choice.
func isREDCapChoices(s string) bool {
	seen := false
	for _, choice := range strings.Split(s, "|") {
		choice = strings.TrimSpace(choice)
		if choice == "" {
			continue
		}
		code, _, _ := strings.Cut(choice, ",")
		if !ChoiceCodeRE.MatchString(strings.TrimSpace(code)) {
			return false
		}
		seen = true
	}
	return seen
}

// isStoredChoices reports whether every ##-separated choice is a valid code,
// a $, and a label.
func isStoredChoices(s string) bool {
	for _, pair := range strings.Split(s, "##") {
		code, _, found := strings.Cut(pair, "$")
		if !found || !ChoiceCodeRE.MatchString(code) {
			return false
		}
	}
	return true
}
