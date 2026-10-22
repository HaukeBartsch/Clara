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

// An unchecked checkbox box reads as 0, a checked one as its stored 1; any
// other missing operand still empties the result (§6.3, REQ-DB-042).
func TestEvalCalcUncheckedBoxReadsZero(t *testing.T) {
	vals := map[Ref]string{{Event: "e", Field: "habits___1"}: "1", {Event: "e", Field: "a"}: "10"}
	isBox := func(field string) bool { return strings.HasPrefix(field, "habits___") }
	resolve := UncheckedAsZero(func(r Ref) (string, bool) { v, ok := vals[r]; return v, ok }, isBox)

	for expr, want := range map[string]string{
		"[e][habits___1] + [e][habits___2] + [e][habits___3]": "1",
		"[e][habits___2] + [e][habits___3]":                   "0",
		"[e][a] * [e][habits___2]":                            "0",
		"[e][a] * [e][habits___1]":                            "10",
	} {
		got, problems, err := EvalCalc(expr, resolve)
		if err != nil || got != want || len(problems) != 0 {
			t.Errorf("eval %q = %q problems %+v err %v, want %q", expr, got, problems, err, want)
		}
	}
	got, problems, _ := EvalCalc("[e][habits___2] + [e][missing]", resolve)
	if got != "" || len(problems) != 1 || problems[0].Operand != "[e][missing]" || problems[0].Problem != ProblemMissingValue {
		t.Errorf("missing non-box operand = %q %+v, want empty with one missing_value", got, problems)
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

// TestBranchingGrammarsAgree holds the design-time validator and the runtime
// evaluator to one grammar (Data_Validation_Design.md §7.1, REQ-VAL-044): an
// expression that saves MUST be one the evaluators accept. While the two
// parsers disagreed, `([a] = 1) = 1` and `is_blank([x]) = 1` passed design time
// and failed only on the evaluation path, where branching gates data entry —
// the direction §7.2 rules out. web/assets/js/branching.js is held to the same
// accept set by the corpus cases (logic_corpus_test.go).
func TestBranchingGrammarsAgree(t *testing.T) {
	// Atoms spanning every shape the grammar can start a condition with, plus
	// the malformed shapes that must fail in both parsers.
	atoms := []string{
		`[e][a]`, `1`, `"x"`, `""`,
		`is_blank([e][a])`, `is_not_blank([e][a])`, `text_contains([e][a], "x")`,
		`([e][a] = 1)`, `(is_blank([e][a]))`, `(([e][a] && [e][b]) || 1)`,
		`unknown_fn([e][a])`, `is_blank()`, `is_blank([e][a], 2)`,
	}
	ops := []string{" = ", " != ", " <> ", " < ", " && ", " || "}

	var exprs []string
	for _, l := range atoms {
		exprs = append(exprs, l)
		for _, op := range ops {
			for _, r := range atoms {
				exprs = append(exprs, l+op+r)
			}
		}
	}

	diverged := 0
	for _, expr := range exprs {
		berr := ValidateBranching(expr, nil)
		_, lerr := CompileLogic(expr)
		if (berr == nil) != (lerr == nil) {
			diverged++
			t.Errorf("grammars disagree on %q: ValidateBranching=%v CompileLogic=%v", expr, berr, lerr)
		}
	}
	if t.Failed() {
		t.Fatalf("%d of %d expressions diverge", diverged, len(exprs))
	}

	// The forms the widened parser used to accept are rejected by both.
	for _, expr := range []string{
		`([e][a] = 1) = 1`,
		`is_blank([e][a]) = 1`,
		`is_not_blank([e][a]) = 0`,
		`text_contains([e][a], "x") = 1`,
		`1 = ([e][a] = 1)`,
		`[e][a] = is_blank([e][a])`,
	} {
		if err := ValidateBranching(expr, nil); err == nil {
			t.Errorf("%q: design time accepts a form the grammar of §7.1 excludes", expr)
		}
		if _, err := CompileLogic(expr); err == nil {
			t.Errorf("%q: evaluator parses a form the grammar of §7.1 excludes", expr)
		}
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

// TestExpressionDepthLimit covers security finding F5: deeply nested
// parentheses and unary operators must surface as a parse error — never as a
// stack overflow, which would take the whole process down. All three grammars
// share the cap.
func TestExpressionDepthLimit(t *testing.T) {
	deep := strings.Repeat("(", maxExprDepth+1) + "1" + strings.Repeat(")", maxExprDepth+1)
	if _, err := ParseCalcExpression(deep); err == nil {
		t.Error("calc: deeply nested expression accepted, want depth error")
	}
	if err := ValidateBranching(strings.ReplaceAll(deep, "1", "[a]"), nil); err == nil {
		t.Error("branching: deeply nested expression accepted, want depth error")
	}
	if _, err := EvalLogic(strings.ReplaceAll(deep, "1", "[a]"), LogicEval{}); err == nil {
		t.Error("logic: deeply nested expression accepted, want depth error")
	}
	// Unary minus nests the calc parser too.
	if _, err := ParseCalcExpression(strings.Repeat("-", maxExprDepth+1) + "1"); err == nil {
		t.Error("calc: unary-minus chain accepted, want depth error")
	}
	// Within the cap, nesting still parses.
	ok := strings.Repeat("(", 40) + "[e][x]" + strings.Repeat(")", 40)
	if _, err := ParseCalcExpression(ok); err != nil {
		t.Errorf("nested expression within the cap rejected: %v", err)
	}
}

// TestExpressionLengthLimit covers security finding F5's other half: a huge
// expression is rejected by the tokenizer before any parsing starts.
func TestExpressionLengthLimit(t *testing.T) {
	long := strings.Repeat("1+ ", maxExprLen) + "1"
	if _, err := ParseCalcExpression(long); err == nil {
		t.Error("calc: oversized expression accepted, want length error")
	}
	if err := ValidateBranching(strings.Repeat("[a]||", maxExprLen)+"[a]", nil); err == nil {
		t.Error("branching: oversized expression accepted, want length error")
	}
	if _, err := EvalLogic(strings.Repeat("[a]||", maxExprLen)+"[a]", LogicEval{}); err == nil {
		t.Error("logic: oversized expression accepted, want length error")
	}
}

// TestCompileLogic covers the compile-once program used by the export's
// filterLogic path (security finding F5): a malformed expression fails to
// compile; a compiled program evaluates per record without re-parsing.
func TestCompileLogic(t *testing.T) {
	if _, err := CompileLogic("[a] ="); err == nil {
		t.Fatal("malformed expression compiled without error")
	}
	prog, err := CompileLogic(`[age] >= "18" && [status] = "2"`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ev := func(vals map[string]string) LogicEval {
		return LogicEval{Value: func(r Ref) string { return vals[r.Field] }}
	}
	if !prog.Eval(ev(map[string]string{"age": "21", "status": "2"})) {
		t.Error("matching record rejected")
	}
	if prog.Eval(ev(map[string]string{"age": "17", "status": "2"})) {
		t.Error("non-matching record accepted")
	}
}

// TestLogicProgramRefs covers the reference list a caller checks an expression
// against — the export's filter gate, master-spec finding F6: every reference the
// program reads, inside function arguments too, in first-seen order and
// deduplicated, with a checkbox box under its value-column name in either
// spelling (REQ-DB-042, REQ-VAL-044) and a single-segment reference keeping its
// empty Event for the caller to resolve as Eval does (GD-15).
func TestLogicProgramRefs(t *testing.T) {
	cases := []struct {
		expr string
		want []Ref
	}{
		{`[age]`, []Ref{{Field: "age"}}},
		{`[baseline_arm_1][age] > "18" && [age] < "65"`, []Ref{
			{Event: "baseline_arm_1", Field: "age"},
			{Field: "age"}, // a different reference: no event to resolve it at
		}},
		{`is_blank([name]) || text_contains([notes], "x") && [status] = "2"`, []Ref{
			{Field: "name"}, {Field: "notes"}, {Field: "status"},
		}},
		// Both box spellings arrive as the value column, and once each.
		{`[demo(code)] = "1" && [demo___code] != "0"`, []Ref{{Field: "demo___code"}}},
	}
	for _, c := range cases {
		prog, err := CompileLogic(c.expr)
		if err != nil {
			t.Fatalf("compile %q: %v", c.expr, err)
		}
		got := prog.Refs()
		if len(got) != len(c.want) {
			t.Errorf("%q refs = %v, want %v", c.expr, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%q ref %d = %+v, want %+v", c.expr, i, got[i], c.want[i])
			}
		}
	}
}
