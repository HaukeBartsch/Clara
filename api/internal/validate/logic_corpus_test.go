package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The branching/filterLogic corpus is the single case set both evaluators are
// held to: EvalLogic here and web/assets/js/branching.js (driven by
// e2e/tests/branching.spec.ts). Data_Validation_Design.md §7.3 fixes one
// normative semantics, so a change that moves either evaluator must move this
// file's expectations too — never the JS alone.
//
// Regenerate after intentional semantic changes: UPDATE_CORPUS=1 go test -run TestGenerateBranchingCorpus

type corpusCase struct {
	Name     string            `json:"name"`
	Expr     string            `json:"expr"`
	Values   map[string]string `json:"values"`
	Choices  map[string]string `json:"choices"`
	Expected bool              `json:"expected"`
	WantErr  bool              `json:"want_err,omitempty"`
}

// corpusInputs are the cases; expected/want_err are computed by EvalLogic and
// written to testdata/branching_corpus.json. Reference keys are
// "<event>|<field>"; an empty event is the [field] shorthand's unresolved form,
// which both evaluators resolve identically through their Ref-keyed resolver.
func corpusInputs() []corpusCase {
	return []corpusCase{
		// Numeric comparisons.
		{Name: "num_eq", Expr: `[e1][age] = 18`, Values: map[string]string{"e1|age": "18"}, Expected: true},
		{Name: "num_ne", Expr: `[e1][age] != 18`, Values: map[string]string{"e1|age": "19"}, Expected: true},
		{Name: "num_lt_str_values", Expr: `[e1][a] < [e1][b]`, Values: map[string]string{"e1|a": "2", "e1|b": "10"}, Expected: true},
		{Name: "num_float", Expr: `[e1][bmi] >= 25.0`, Values: map[string]string{"e1|bmi": "24.99"}, Expected: false},
		{Name: "num_signed_value", Expr: `[e1][delta] < 0`, Values: map[string]string{"e1|delta": "-3"}, Expected: true},
		{Name: "num_plus_value_vs_ref", Expr: `[e1][delta] = [e1][other]`, Values: map[string]string{"e1|delta": "+3", "e1|other": "3"}, Expected: true},
		// Signed literals are not in the grammar (§7.1) — a leading sign is an operator.
		{Name: "err_signed_literal", Expr: `[e1][a] = -3`, Values: map[string]string{}, WantErr: true},
		{Name: "num_zero_forms", Expr: `[e1][a] = [e1][b]`, Values: map[string]string{"e1|a": "0.0", "e1|b": "0"}, Expected: true},
		// Missing values (ASM-VAL-6): every operator is 0.
		{Name: "missing_eq", Expr: `[e1][x] = 5`, Values: map[string]string{}, Expected: false},
		{Name: "missing_ne_is_false", Expr: `[e1][x] != 5`, Values: map[string]string{}, Expected: false},
		{Name: "missing_lt", Expr: `[e1][x] < 5`, Values: map[string]string{}, Expected: false},
		{Name: "both_missing_eq", Expr: `[e1][x] = [e1][y]`, Values: map[string]string{}, Expected: false},
		// String comparisons, byte-wise.
		{Name: "str_eq", Expr: `[e1][site] = "oslo"`, Values: map[string]string{"e1|site": "oslo"}, Expected: true},
		{Name: "str_ne_case", Expr: `[e1][site] != "Oslo"`, Values: map[string]string{"e1|site": "oslo"}, Expected: true},
		{Name: "str_lt_lexicographic", Expr: `[e1][a] < [e1][b]`, Values: map[string]string{"e1|a": "abc", "e1|b": "abd"}, Expected: true},
		{Name: "str_numeric_vs_text_falls_back", Expr: `[e1][x] = 1`, Values: map[string]string{"e1|x": "abc"}, Expected: false},
		{Name: "str_nonascii_order", Expr: `[e1][a] < [e1][b]`, Values: map[string]string{"e1|a": "é", "e1|b": "f"}, Expected: false}, // UTF-8 bytes C3 A9 > 66
		{Name: "str_escaped_quote", Expr: `[e1][x] = "a\"b"`, Values: map[string]string{"e1|x": `a"b`}, Expected: true},
		{Name: "str_escaped_backslash", Expr: "[e1][x] = \"a\\\\b\"", Values: map[string]string{"e1|x": "a\\b"}, Expected: true},
		// Choice labels resolve to codes before comparison (REQ-VAL-022).
		{
			Name: "choice_label_eq", Expr: `[e1][sex] = "Male"`,
			Values: map[string]string{"e1|sex": "1"}, Choices: map[string]string{"e1|sex": "1$Male##2$Female"},
			Expected: true,
		},
		{
			Name: "choice_label_ne", Expr: `[e1][sex] != "Female"`,
			Values: map[string]string{"e1|sex": "1"}, Choices: map[string]string{"e1|sex": "1$Male##2$Female"},
			Expected: true,
		},
		{
			Name: "choice_code_used_directly", Expr: `[e1][sex] = "2"`,
			Values: map[string]string{"e1|sex": "2"}, Choices: map[string]string{"e1|sex": "1$Male##2$Female"},
			Expected: true,
		},
		{
			Name: "choice_label_on_left", Expr: `"Female" = [e1][sex]`,
			Values: map[string]string{"e1|sex": "2"}, Choices: map[string]string{"e1|sex": "1$Male##2$Female"},
			Expected: true,
		},
		{
			Name: "choice_label_no_match_passes_through", Expr: `[e1][sex] = "man"`,
			Values: map[string]string{"e1|sex": "1"}, Choices: map[string]string{"e1|sex": "1$Male##2$Female"},
			Expected: false,
		},
		// Truthiness (REQ-VAL-029, ASM-VAL-6).
		{Name: "truthy_radio_selected", Expr: `[e1][chk]`, Values: map[string]string{"e1|chk": "1"}, Choices: map[string]string{"e1|chk": "1$Yes"}, Expected: true},
		{Name: "truthy_radio_zero_code_still_true", Expr: `[e1][chk]`, Values: map[string]string{"e1|chk": "0"}, Choices: map[string]string{"e1|chk": "0$No##1$Yes"}, Expected: true},
		{Name: "truthy_radio_unselected", Expr: `[e1][chk]`, Values: map[string]string{}, Choices: map[string]string{"e1|chk": "1$Yes"}, Expected: false},
		{Name: "truthy_text_nonempty", Expr: `[e1][note]`, Values: map[string]string{"e1|note": "x"}, Expected: true},
		{Name: "truthy_numeric_zero", Expr: `[e1][score]`, Values: map[string]string{"e1|score": "0"}, Expected: false},
		{Name: "truthy_numeric_zero_dot_zero", Expr: `[e1][score]`, Values: map[string]string{"e1|score": "0.0"}, Expected: false},
		{Name: "truthy_numeric_nonzero", Expr: `[e1][score]`, Values: map[string]string{"e1|score": "0.5"}, Expected: true},
		{Name: "truthy_text_zero_like_word", Expr: `[e1][note]`, Values: map[string]string{"e1|note": "no"}, Expected: true},
		{Name: "truthy_num_literal", Expr: `1`, Values: map[string]string{}, Expected: true},
		{Name: "truthy_num_literal_zero", Expr: `0`, Values: map[string]string{}, Expected: false},
		// Functions.
		{Name: "is_blank_true", Expr: `is_blank([e1][x])`, Values: map[string]string{}, Expected: true},
		{Name: "is_blank_false", Expr: `is_blank([e1][x])`, Values: map[string]string{"e1|x": "v"}, Expected: false},
		{Name: "is_not_blank", Expr: `is_not_blank([e1][x])`, Values: map[string]string{"e1|x": "v"}, Expected: true},
		{Name: "text_contains_hit", Expr: `text_contains([e1][notes], "follow-up")`, Values: map[string]string{"e1|notes": "a follow-up visit"}, Expected: true},
		{Name: "text_contains_miss", Expr: `text_contains([e1][notes], "follow-up")`, Values: map[string]string{"e1|notes": "routine"}, Expected: false},
		{Name: "text_contains_empty_ref", Expr: `text_contains([e1][notes], "x")`, Values: map[string]string{}, Expected: false},
		{Name: "text_contains_num_needle", Expr: `text_contains([e1][code], 42)`, Values: map[string]string{"e1|code": "room-42"}, Expected: true},
		// Logical structure and precedence.
		{
			Name: "and_or_precedence", Expr: `[e1][a] = 1 || [e1][b] = 2 && [e1][c] = 3`,
			Values: map[string]string{"e1|a": "0", "e1|b": "2", "e1|c": "3"}, Expected: true,
		},
		{
			Name: "parens_override_precedence", Expr: `([e1][a] = 1 || [e1][b] = 2) && [e1][c] = 9`,
			Values: map[string]string{"e1|a": "0", "e1|b": "2", "e1|c": "3"}, Expected: false,
		},
		{Name: "deep_parens", Expr: `((([e1][a] = 1)))`, Values: map[string]string{"e1|a": "1"}, Expected: true},
		{Name: "and_with_functions", Expr: `is_not_blank([e1][x]) && text_contains([e1][x], "ok")`, Values: map[string]string{"e1|x": "looks ok"}, Expected: true},
		// Dates and instants (§4.1, GD-16).
		{Name: "date_eq", Expr: `[e1][d] = "2026-03-05"`, Values: map[string]string{"e1|d": "2026-03-05"}, Expected: true},
		{Name: "date_lt_chronological", Expr: `[e1][d] < [e1][d2]`, Values: map[string]string{"e1|d": "2026-02-28", "e1|d2": "2026-03-01"}, Expected: true},
		{
			Name: "datetime_offset_instant_equal", Expr: `[e1][t] = [e1][t2]`,
			Values:   map[string]string{"e1|t": "2026-03-05 12:00:00 +0200", "e1|t2": "2026-03-05 10:00:00"},
			Expected: true,
		},
		{
			Name: "datetime_offset_colon_instant_equal", Expr: `[e1][t] = [e1][t2]`,
			Values:   map[string]string{"e1|t": "2026-03-05 12:00 +02:00", "e1|t2": "2026-03-05 10:00"},
			Expected: true,
		},
		{
			Name: "datetime_offset_ordering", Expr: `[e1][t] < [e1][t2]`,
			Values:   map[string]string{"e1|t": "2026-03-05 11:00 -0400", "e1|t2": "2026-03-05 14:00"},
			Expected: false, // 15:00Z vs 14:00Z
		},
		{Name: "date_invalid_not_instant_falls_back", Expr: `[e1][d] < [e1][d2]`, Values: map[string]string{"e1|d": "2026-02-30", "e1|d2": "2026-02-01"}, Expected: false},
		{Name: "date_leap_day_valid", Expr: `[e1][d] = [e1][d2]`, Values: map[string]string{"e1|d": "2024-02-29", "e1|d2": "2024-02-29"}, Expected: true},
		// Bare [field] shorthand resolves as an empty-event reference.
		{Name: "bare_ref", Expr: `[age] = 30`, Values: map[string]string{"|age": "30"}, Expected: true},
		{Name: "bare_and_qualified_mixed", Expr: `[age] = 30 && [e1][sex] = "Male"`, Values: map[string]string{"|age": "30", "e1|sex": "1"}, Choices: map[string]string{"e1|sex": "1$Male"}, Expected: true},
		// Malformed expressions (§7.2 rules 1/3) — both sides must reject.
		{Name: "err_empty", Expr: ``, Values: map[string]string{}, WantErr: true},
		{Name: "err_unknown_function", Expr: `contains([e1][x], "a")`, Values: map[string]string{}, WantErr: true},
		{Name: "err_wrong_arity", Expr: `is_blank([e1][x], 2)`, Values: map[string]string{}, WantErr: true},
		{Name: "err_unbalanced_open", Expr: `([e1][a] = 1`, Values: map[string]string{}, WantErr: true},
		{Name: "err_unbalanced_close", Expr: `[e1][a] = 1)`, Values: map[string]string{}, WantErr: true},
		{Name: "err_trailing_operator", Expr: `[e1][a] =`, Values: map[string]string{}, WantErr: true},
		{Name: "err_double_compare", Expr: `1 < [e1][a] < 2`, Values: map[string]string{}, WantErr: true},
		{Name: "err_unterminated_string", Expr: `[e1][x] = "abc`, Values: map[string]string{}, WantErr: true},
		{Name: "err_unterminated_ref", Expr: `[e1][x = 1`, Values: map[string]string{}, WantErr: true},
		{Name: "err_bare_identifier", Expr: `foo`, Values: map[string]string{}, WantErr: true},
		{Name: "err_arithmetic_in_branching", Expr: `[e1][a] + 1`, Values: map[string]string{}, WantErr: true},
	}
}

func runCorpusCase(t *testing.T, cs corpusCase) (bool, error) {
	t.Helper()
	ev := LogicEval{
		Value:   func(r Ref) string { return cs.Values[r.Event+"|"+r.Field] },
		Choices: func(r Ref) string { return cs.Choices[r.Event+"|"+r.Field] },
	}
	return EvalLogic(cs.Expr, ev)
}

// TestBranchingCorpus checks EvalLogic against the committed corpus — the same
// file e2e/tests/branching.spec.ts holds branching.js to.
func TestBranchingCorpus(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "branching_corpus.json"))
	if err != nil {
		t.Fatalf("read corpus (generate with UPDATE_CORPUS=1): %v", err)
	}
	var cases []corpusCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	inputs := corpusInputs()
	if len(cases) != len(inputs) {
		t.Fatalf("corpus has %d cases, inputs define %d — regenerate with UPDATE_CORPUS=1", len(cases), len(inputs))
	}
	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			got, err := runCorpusCase(t, cs)
			if cs.WantErr {
				if err == nil {
					t.Fatalf("expected a parse error, got value %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("EvalLogic: %v", err)
			}
			if got != cs.Expected {
				t.Fatalf("got %v, want %v", got, cs.Expected)
			}
		})
	}
}

// TestGenerateBranchingCorpus computes expected results with EvalLogic and
// writes the shared corpus. Run: UPDATE_CORPUS=1 go test -run TestGenerateBranchingCorpus
func TestGenerateBranchingCorpus(t *testing.T) {
	if os.Getenv("UPDATE_CORPUS") != "1" {
		t.Skip("set UPDATE_CORPUS=1 to write testdata/branching_corpus.json")
	}
	cases := corpusInputs()
	for i := range cases {
		got, err := runCorpusCase(t, cases[i])
		if cases[i].WantErr {
			if err == nil {
				t.Fatalf("%s: expected parse error, got %v", cases[i].Name, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: EvalLogic: %v", cases[i].Name, err)
		}
		cases[i].Expected = got
	}
	raw, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		t.Fatalf("marshal corpus: %v", err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "branching_corpus.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
}
