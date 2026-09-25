// Package validate is the pure, DB-free core of the data-validation design
// (Data_Validation_Design.md): the CSV formula-injection neutralizer (§5.3)
// and the type validators (§4), including the validation-type registry that
// resolves named regex types from caller-supplied rows (§4.2). Nothing here
// touches the store or the network, so it is unit-tested in isolation and the
// data API maps its own types into these signatures. The expression evaluator
// shared by filterLogic (§7) and branching logic will live here as well.
package validate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// §4 built-in structured grammars, precompiled once (Data_Validation_Design.md §4).
var (
	integerRE = regexp.MustCompile(`^-?[0-9]+$`)
	floatRE   = regexp.MustCompile(`^[+-]?[0-9]+(\.[0-9]+)?$`)
)

// identifierTypes are the seeded registry entries whose fields preset the
// direct_identifier flag (REQ-EXP-020); names match the migration seed.
var identifierTypes = map[string]bool{
	"email":               true,
	"MRN":                 true,
	"international phone": true,
	"national phone":      true,
}

// PresetDirectIdentifier reports whether a new field with this validation
// type starts out flagged as a direct identifier (REQ-EXP-020).
func PresetDirectIdentifier(validationType string) bool { return identifierTypes[validationType] }

// RegistryEntry is one validation_types row (REQ-DB-033).
type RegistryEntry struct {
	Name    string
	Regex   string
	Builtin bool
}

// Registry resolves regex validation types beyond the four built-in
// structured ones (§4.2). Patterns are compiled once at load and wrapped as
// \A(?:…)\z so anchoring in the stored pattern is belt-and-braces; RE2 keeps
// per-value cost bounded for adversarial input.
type Registry struct {
	patterns map[string]*regexp.Regexp
	entries  []RegistryEntry
}

// NewRegistry compiles every entry's pattern; a pattern that fails to compile
// makes the whole call fail — callers reject the entry at design time and
// application-log the failure (REQ-TECH-016, §4.2).
func NewRegistry(rows []RegistryEntry) (*Registry, error) {
	r := &Registry{
		patterns: make(map[string]*regexp.Regexp, len(rows)),
		entries:  append([]RegistryEntry(nil), rows...),
	}
	for _, row := range rows {
		re, err := regexp.Compile(`\A(?:` + row.Regex + `)\z`)
		if err != nil {
			return nil, fmt.Errorf("validation type %q: %w", row.Name, err)
		}
		r.patterns[row.Name] = re
	}
	return r, nil
}

// Lookup returns the compiled pattern for a registry name.
func (r *Registry) Lookup(name string) (*regexp.Regexp, bool) {
	if r == nil {
		return nil, false
	}
	re, ok := r.patterns[name]
	return re, ok
}

// Entries returns the registry rows in load order — the designer listing of
// available types (REQ-API-104).
func (r *Registry) Entries() []RegistryEntry {
	if r == nil {
		return nil
	}
	return append([]RegistryEntry(nil), r.entries...)
}

// Rule codes are stable, machine-readable (REQ-VAL-009). Messages are
// human-readable and must not leak internals (REQ-API-006/039).
type RuleCode string

const (
	CodeTypeInvalid    RuleCode = "TYPE_INVALID"
	CodeRange          RuleCode = "RANGE"
	CodeChoiceInvalid  RuleCode = "CHOICE_INVALID"
	CodeNonValueField  RuleCode = "NON_VALUE_FIELD"
	CodeCalculatedRO   RuleCode = "CALCULATED_READONLY"
	CodeUnknownField   RuleCode = "UNKNOWN_FIELD"
	CodeContentInvalid RuleCode = "CONTENT_INVALID"
)

// Violation is one validation failure: a stable code plus a message.
type Violation struct {
	Code    RuleCode
	Message string
}

func (v Violation) Error() string { return string(v.Code) + ": " + v.Message }

// Field is the minimal, store-independent view of a data-dictionary entry the
// validators need (Data_Validation_Design.md §4/§9). The data API copies the
// relevant columns from its own field type into this.
type Field struct {
	Name           string
	Type           string // text | dropdown | radio | matrix | description | header | calculated
	ValidationType string // "" | integer | floating point | email | MRN | date | datetime
	ValidationMin  string
	ValidationMax  string
	// Choices is the stored code$label##code$label encoding (§1). Empty for
	// non-choice fields.
	Choices string
}

// --- CSV formula-injection neutralization (REQ-VAL-032, §5.3) ---

// CSVCell returns v ready for a CSV cell, prefixing a single quote when v
// would otherwise read as a spreadsheet formula. Applied at CSV serialization
// only, never for JSON, and never at store: a leading `=`/`@` always; a
// leading `+`/`-` only when v is not itself a number (so `-5` and `+3.14`
// pass through intact). Leading whitespace is trimmed only to *decide* — the
// cell content itself is emitted unchanged apart from an added quote, so
// legitimate values never lose data.
func CSVCell(v string) string {
	t := strings.TrimLeft(v, " \t")
	if t == "" {
		return v
	}
	switch t[0] {
	case '=', '@':
		return "'" + v
	case '+', '-':
		if !isNumber(t) {
			return "'" + v
		}
	}
	return v
}

// isNumber reports whether v matches the §4 integer or floating-point grammar
// exactly (not strconv's wider acceptance of e.g. -Inf, -.5 or 1e10).
func isNumber(v string) bool {
	return matchRegex(integerRE, v) || matchRegex(floatRE, v)
}

// --- type validators (REQ-VAL-015…023, §4) ---

// ValidateValue checks a single value against f. An empty value is a
// no-op ("no value", REQ-VAL-024) and returns nil; the caller handles the
// identifier case separately. reg resolves regex validation types (§4.2);
// a type that is neither built-in nor in reg is rejected (a field must never
// silently stop validating because its registry row went away). Date/datetime
// and free-text HTML sanitization are the import slice's responsibility (they
// need the collection offset and the §5.2 sanitizer) and are not applied here.
func ValidateValue(f Field, value string, reg *Registry) *Violation {
	if value == "" {
		return nil
	}
	if f.Type == "description" || f.Type == "header" {
		return &Violation{CodeNonValueField, fmt.Sprintf("field %q does not accept values", f.Name)}
	}
	if f.Type == "calculated" {
		return &Violation{CodeCalculatedRO, fmt.Sprintf("field %q is calculated and read-only", f.Name)}
	}
	if v := validateChoices(f, value); v != nil {
		return v
	}
	switch f.ValidationType {
	case "integer":
		if !matchRegex(integerRE, value) {
			return &Violation{CodeTypeInvalid, fmt.Sprintf("value %q is not a valid integer", value)}
		}
	case "floating point":
		if !matchRegex(floatRE, value) {
			return &Violation{CodeTypeInvalid, fmt.Sprintf("value %q is not a valid floating point", value)}
		}
	case "", "date", "datetime":
		// Free text, or date/datetime whose grammar is checked by the import
		// slice with its collection offset (REQ-VAL-019/039, §4.1).
	default:
		re, ok := reg.Lookup(f.ValidationType)
		if !ok {
			return &Violation{CodeTypeInvalid, fmt.Sprintf("field %q has unknown validation type %q", f.Name, f.ValidationType)}
		}
		if !re.MatchString(value) {
			return &Violation{CodeTypeInvalid, fmt.Sprintf("value %q is not a valid %s", value, f.ValidationType)}
		}
	}
	return rangeCheck(f, value)
}

// validateChoices enforces REQ-VAL-022/023: a choice field's value must be one
// of its codes, regardless of the field's validation type.
func validateChoices(f Field, value string) *Violation {
	if f.Choices == "" {
		return nil
	}
	codes := choiceCodes(f.Choices)
	for _, c := range codes {
		if c == value {
			return nil
		}
	}
	return &Violation{CodeChoiceInvalid, fmt.Sprintf("value %q is not a choice of %q", value, f.Name)}
}

// rangeCheck applies validation_min/max to integer/floating point only
// (REQ-VAL-020); other types ignore the bounds. Integers compare as int64 so
// values beyond float64's 2^53 exact-integer range are not rounded away;
// unparseable bounds are ignored (design-time validation rejects them, §9).
func rangeCheck(f Field, value string) *Violation {
	if f.ValidationType != "integer" && f.ValidationType != "floating point" {
		return nil
	}
	if f.ValidationMin == "" && f.ValidationMax == "" {
		return nil
	}
	if f.ValidationType == "integer" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil // type validator already reported the grammar
		}
		if f.ValidationMin != "" {
			if lo, err := strconv.ParseInt(f.ValidationMin, 10, 64); err == nil && n < lo {
				return &Violation{CodeRange, fmt.Sprintf("value %s outside range %s…", value, f.ValidationMin)}
			}
		}
		if f.ValidationMax != "" {
			if hi, err := strconv.ParseInt(f.ValidationMax, 10, 64); err == nil && n > hi {
				return &Violation{CodeRange, fmt.Sprintf("value %s outside range …%s", value, f.ValidationMax)}
			}
		}
		return nil
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil // type validator already reported the grammar
	}
	if f.ValidationMin != "" {
		if lo, err := strconv.ParseFloat(f.ValidationMin, 64); err == nil && n < lo {
			return &Violation{CodeRange, fmt.Sprintf("value %s outside range %s…", value, f.ValidationMin)}
		}
	}
	if f.ValidationMax != "" {
		if hi, err := strconv.ParseFloat(f.ValidationMax, 64); err == nil && n > hi {
			return &Violation{CodeRange, fmt.Sprintf("value %s outside range …%s", value, f.ValidationMax)}
		}
	}
	return nil
}

// matchRegex reports whether s fully matches the precompiled (anchored)
// pattern re.
func matchRegex(re *regexp.Regexp, s string) bool {
	return re.MatchString(s)
}

// choiceCodes splits the code$label##code$label encoding into its codes.
func choiceCodes(s string) []string {
	var out []string
	for _, pair := range strings.Split(s, "##") {
		if pair == "" {
			continue
		}
		out = append(out, strings.SplitN(pair, "$", 2)[0])
	}
	return out
}

// LabelForCode returns the label of code within the stored choices encoding,
// or "" when the code is absent (raw/label export mapping, REQ-API-027).
func LabelForCode(choices, code string) string {
	for _, pair := range strings.Split(choices, "##") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "$", 2)
		if kv[0] == code {
			if len(kv) == 2 {
				return kv[1]
			}
			return code
		}
	}
	return ""
}

// CodeForLabel is the inverse of LabelForCode, used by the expression
// evaluator to resolve a string constant that matches a choice label to its
// code before comparison (Data_Validation_Design.md §7.3).
func CodeForLabel(choices, label string) string {
	for _, pair := range strings.Split(choices, "##") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "$", 2)
		if len(kv) == 2 && kv[1] == label {
			return kv[0]
		}
	}
	return ""
}
