package validate

import (
	"strings"
	"testing"
)

func TestParseCalcExpression(t *testing.T) {
	refs, err := ParseCalcExpression("[event_a][age] + 2 * [event_b][score]")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []Ref{{Event: "event_a", Field: "age"}, {Event: "event_b", Field: "score"}}
	if len(refs) != 2 || refs[0] != want[0] || refs[1] != want[1] {
		t.Fatalf("refs: %+v", refs)
	}

	// Precedence and parentheses still yield the same reference set.
	if _, err := ParseCalcExpression("([event_a][age] + [event_a][score]) * 2"); err != nil {
		t.Fatalf("parenthesized: %v", err)
	}
	// A repeated reference appears once.
	refs, _ = ParseCalcExpression("[e][x] + [e][x]")
	if len(refs) != 1 {
		t.Fatalf("dedupe: %+v", refs)
	}

	for _, bad := range []string{
		"",           // empty
		"[e]",        // incomplete reference
		"[e][x + 1",  // unterminated reference
		"1 + ",       // missing right operand
		"* 2",        // leading binary operator
		"(1 + 2",     // unbalanced parens
		"[e][x] & 2", // unexpected character
		`"text"`,     // strings are not arithmetic operands
	} {
		if _, err := ParseCalcExpression(bad); err == nil {
			t.Errorf("expected parse error for %q", bad)
		}
	}
}

func TestEvalCalc(t *testing.T) {
	vals := map[Ref]string{
		{Event: "e", Field: "a"}: "10",
		{Event: "e", Field: "b"}: "2.5",
		{Event: "e", Field: "c"}: "",    // stored but empty
		{Event: "e", Field: "d"}: "abc", // non-numeric
	}
	resolve := func(r Ref) (string, bool) { v, ok := vals[r]; return v, ok }

	cases := []struct {
		expr string
		want string
	}{
		{"[e][a] + [e][b]", "12.5"},
		{"[e][a] * 2", "20"},
		{"[e][a] / [e][b]", "4"},
		{"([e][a] + [e][b]) * 2", "25"},
		{"-[e][b]", "-2.5"},
	}
	for _, c := range cases {
		got, problems, err := EvalCalc(c.expr, resolve)
		if err != nil {
			t.Fatalf("eval %q: %v", c.expr, err)
		}
		if got != c.want || len(problems) != 0 {
			t.Errorf("eval %q = %q problems %+v, want %q", c.expr, got, problems, c.want)
		}
	}

	// Evaluation problems produce an empty value and one flagged operand (§6.4).
	emptyCases := []struct {
		expr    string
		operand string
		kind    ProblemKind
	}{
		{"[e][a] + [e][missing]", "[e][missing]", ProblemMissingValue},
		{"[e][a] + [e][c]", "[e][c]", ProblemMissingValue},
		{"[e][a] + [e][d]", "[e][d]", ProblemNonNumeric},
		{"[e][a] / 0", "division", ProblemDivisionByZero},
	}
	for _, c := range emptyCases {
		got, problems, err := EvalCalc(c.expr, resolve)
		if err != nil {
			t.Fatalf("eval %q: %v", c.expr, err)
		}
		if got != "" {
			t.Errorf("eval %q = %q, want empty", c.expr, got)
		}
		if len(problems) != 1 || problems[0].Operand != c.operand || problems[0].Problem != c.kind {
			t.Errorf("eval %q problems = %+v, want one %s on %s", c.expr, problems, c.kind, c.operand)
		}
	}

	if _, _, err := EvalCalc("1 +", resolve); err == nil {
		t.Error("expected parse error from EvalCalc")
	}
}

func TestValidateBranching(t *testing.T) {
	ok := map[Ref]bool{{Event: "e", Field: "yn"}: true, {Event: "e", Field: "txt"}: true, {Event: "e", Field: "age"}: true}
	resolve := func(r Ref) bool { return ok[r] }

	good := []string{
		`[e][yn] = "y"`,
		"[e][age] > 18 && [e][yn] != \"n\"",
		"[e][yn] = \"y\" || is_blank([e][txt])",
		"is_not_blank([e][txt]) && ([e][yn] = \"y\" || [e][yn] = \"n\")",
		`text_contains([e][txt], "abc")`,
		"[e][yn]", // bare reference truthiness (ASM-VAL-6)
	}
	for _, expr := range good {
		if err := ValidateBranching(expr, resolve); err != nil {
			t.Errorf("expected %q valid: %v", expr, err)
		}
	}

	bad := []string{
		"",                        // empty
		"[e][yn] =",               // missing right operand
		"[e][yn] == \"y\"",        // no == operator
		"is_blank([e][yn], 1)",    // wrong arity
		"text_contains([e][txt])", // wrong arity
		"unknown_fn([e][yn])",     // unknown function
		"[e][nope] = \"y\"",       // reference does not resolve
		"([e][yn] = \"y\"",        // unbalanced parens
		`[e][yn] = "unclosed`,     // unterminated string
		"is_blank()",              // zero args
	}
	for _, expr := range bad {
		if err := ValidateBranching(expr, resolve); err == nil {
			t.Errorf("expected %q invalid", expr)
		}
	}

	// Well-formedness-only mode (nil resolver) ignores reference resolution.
	if err := ValidateBranching(`[e][nope] = "y"`, nil); err != nil {
		t.Errorf("nil resolver should skip refs: %v", err)
	}
}

// TestBareFieldReferences: the branching grammar accepts a single-segment
// [field] reference (the §3.6.3 minimum form — empty Event, callers resolve
// it against the project's first event); the calc grammar keeps the
// event-qualified shape of §6.1.
func TestBareFieldReferences(t *testing.T) {
	if err := ValidateBranching(`[age] = "42"`, nil); err != nil {
		t.Errorf(`bare [field] equality: %v`, err)
	}
	if err := ValidateBranching(`[yn] && is_blank([txt]) || text_contains([txt], "a")`, nil); err != nil {
		t.Errorf("bare refs in compounds: %v", err)
	}
	refs, err := ParseBranchingRefs(`[age] > 18 && [e][yn] = "y"`)
	if err != nil || len(refs) != 2 ||
		refs[0] != (Ref{Field: "age"}) || refs[1] != (Ref{Event: "e", Field: "yn"}) {
		t.Errorf("ParseBranchingRefs = %v, %v; want bare then qualified", refs, err)
	}
	if _, err := ParseCalcExpression("[age] * 2"); err == nil {
		t.Error("calc grammar must keep event-qualified references")
	}
	// EvalLogic hands the empty Event to the resolver unchanged.
	vals := map[string]string{"|age": "42"}
	ok, err := EvalLogic(`[age] = "42"`, LogicEval{
		Value: func(r Ref) string { return vals[r.Event+"|"+r.Field] },
	})
	if err != nil || !ok {
		t.Errorf("bare ref eval = %v, %v; want true", ok, err)
	}
}

func TestFormatNumber(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{
		{3, "3"}, {-3, "-3"}, {2.5, "2.5"}, {0.1 + 0.2, "0.3"},
	} {
		if got := formatNumber(c.v); got != c.want {
			t.Errorf("formatNumber(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestEvalCalcLongChain(t *testing.T) {
	// A deep chain must not blow the parser and must evaluate left to right.
	var sb strings.Builder
	sb.WriteString("1")
	for i := 0; i < 200; i++ {
		sb.WriteString(" + 1")
	}
	got, problems, err := EvalCalc(sb.String(), func(Ref) (string, bool) { return "", false })
	if err != nil || len(problems) != 0 || got != "201" {
		t.Fatalf("long chain = %q %+v %v", got, problems, err)
	}
}
