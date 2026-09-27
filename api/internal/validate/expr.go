package validate

import (
	"fmt"
	"strconv"
	"strings"
)

// Expression grammars for calculated fields (§6.1) and branching logic (§7.1),
// Data_Validation_Design.md. The evaluator is shared by the runtime
// recomputation path and the design-time test action (REQ-API-096); the
// branching parser exists for design-time validation only — the API applies no
// branching logic on any data path (DEV-VAL-5).

// Ref is one field reference [<event>][<field>] of an expression.
type Ref struct {
	Event string
	Field string
}

func (r Ref) String() string { return "[" + r.Event + "][" + r.Field + "]" }

// ProblemKind classifies a single evaluation problem (§6.4).
type ProblemKind string

const (
	ProblemMissingValue   ProblemKind = "missing_value"
	ProblemNonNumeric     ProblemKind = "non_numeric_operand"
	ProblemDivisionByZero ProblemKind = "division_by_zero"
)

// Problem is one operand-scoped evaluation problem for the test action.
type Problem struct {
	Operand string
	Problem ProblemKind
}

// --- tokenizer (shared by both grammars) ---

type tokKind int

const (
	tokEOF tokKind = iota
	tokNum
	tokStr
	tokRef
	tokIdent // function name
	tokOp    // arithmetic / comparison / logical operator
	tokLParen
	tokRParen
	tokComma
)

type token struct {
	kind tokKind
	text string // operator text, identifier, or reference source
	num  float64
	ref  Ref
	str  string // unquoted string value (tokStr)
}

// tokenize splits an expression into tokens. It reports a scan error for an
// unterminated string, a malformed reference, or an unexpected character.
func tokenize(s string) ([]token, error) {
	var toks []token
	i := 0
	runes := []rune(s)
	for i < len(runes) {
		c := runes[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '[':
			// [event][field] — two bracketed segments.
			end := strings.IndexRune(string(runes[i+1:]), ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated field reference at offset %d", i)
			}
			ev := string(runes[i+1 : i+1+end])
			j := i + end + 2 // index just past the first ']'
			if j >= len(runes) || runes[j] != '[' {
				return nil, fmt.Errorf("expected a [event][field] reference at offset %d", i)
			}
			close2 := strings.IndexRune(string(runes[j+1:]), ']')
			if close2 < 0 {
				return nil, fmt.Errorf("unterminated field reference at offset %d", j)
			}
			fld := string(runes[j+1 : j+1+close2])
			toks = append(toks, token{kind: tokRef, ref: Ref{Event: ev, Field: fld}, text: "[" + ev + "][" + fld + "]"})
			i = j + close2 + 2
		case c == '"':
			var sb strings.Builder
			i++
			closed := false
			for i < len(runes) {
				if runes[i] == '\\' && i+1 < len(runes) {
					nxt := runes[i+1]
					if nxt != '"' && nxt != '\\' {
						return nil, fmt.Errorf("invalid escape in string at offset %d", i)
					}
					sb.WriteRune(nxt)
					i += 2
					continue
				}
				if runes[i] == '"' {
					closed = true
					i++
					break
				}
				sb.WriteRune(runes[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, token{kind: tokStr, str: sb.String()})
		case c == '(':
			toks = append(toks, token{kind: tokLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen})
			i++
		case c == ',':
			toks = append(toks, token{kind: tokComma})
			i++
		case isNumStart(runes, i):
			j := i + 1
			for j < len(runes) && (isDigit(runes[j]) || runes[j] == '.') {
				j++
			}
			lit := string(runes[i:j])
			v, err := strconv.ParseFloat(lit, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric literal %q", lit)
			}
			toks = append(toks, token{kind: tokNum, num: v})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(runes) && (isIdentPart(runes[j]) || runes[j] == '_') {
				j++
			}
			toks = append(toks, token{kind: tokIdent, text: string(runes[i:j])})
			i = j
		default:
			op := matchOperator(string(runes[i:]))
			if op == "" {
				return nil, fmt.Errorf("unexpected character %q at offset %d", string(c), i)
			}
			toks = append(toks, token{kind: tokOp, text: op})
			i += len([]rune(op))
		}
	}
	toks = append(toks, token{kind: tokEOF})
	return toks, nil
}

// matchOperator returns the longest operator prefix matching two-char then
// one-char forms. Comparison and logical operators are shared; arithmetic is
// a subset.
func matchOperator(s string) string {
	for _, op := range []string{"&&", "||", "!=", "<=", ">="} {
		if strings.HasPrefix(s, op) {
			return op
		}
	}
	for _, op := range []string{"+", "-", "*", "/", "=", "<", ">"} {
		if strings.HasPrefix(s, op) {
			return op
		}
	}
	return ""
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }
func isNumStart(rs []rune, i int) bool {
	if isDigit(rs[i]) {
		return true
	}
	// A leading sign only starts a number when followed by a digit or dot and
	// the previous token does not already close an operand — the parser treats
	// unary minus separately; here we accept a bare numeric start.
	return false
}
func isIdentStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
func isIdentPart(r rune) bool { return isDigit(r) || isIdentStart(r) }

// --- calculated-field grammar (§6.1) ---

type calcNode interface {
	eval(ev *calcEval) (float64, bool)
}

type calcNum struct{ v float64 }
type calcRef struct {
	ref Ref
	src string
}
type calcBin struct {
	op   string
	l, r calcNode
}

func (n calcNum) eval(*calcEval) (float64, bool) { return n.v, true }

func (n calcRef) eval(e *calcEval) (float64, bool) {
	raw, ok := e.resolve(n.ref)
	if !ok || raw == "" {
		e.problem(n.src, ProblemMissingValue)
		return 0, false
	}
	v, err := parseNumber(raw)
	if err != nil {
		e.problem(n.src, ProblemNonNumeric)
		return 0, false
	}
	return v, true
}

func (n calcBin) eval(e *calcEval) (float64, bool) {
	lv, lok := n.l.eval(e)
	rv, rok := n.r.eval(e)
	if !lok || !rok {
		return 0, false
	}
	switch n.op {
	case "+":
		return lv + rv, true
	case "-":
		return lv - rv, true
	case "*":
		return lv * rv, true
	case "/":
		if rv == 0 {
			e.problem("division", ProblemDivisionByZero)
			return 0, false
		}
		return lv / rv, true
	}
	return 0, false
}

// calcEval carries the resolver and the problem accumulator for one evaluation.
type calcEval struct {
	resolve  func(Ref) (string, bool)
	problems []Problem
	seen     map[string]bool
}

func (e *calcEval) problem(operand string, kind ProblemKind) {
	if e.seen == nil {
		e.seen = map[string]bool{}
	}
	key := operand + "|" + string(kind)
	if e.seen[key] {
		return
	}
	e.seen[key] = true
	e.problems = append(e.problems, Problem{Operand: operand, Problem: kind})
}

// calcParser is a recursive-descent parser over the §6.1 grammar.
type calcParser struct {
	toks []token
	pos  int
	refs []Ref
}

// ParseCalcExpression parses a calculated-field expression and returns its
// referenced fields (in first-seen order). A parse error means the expression
// is not well-formed (§6.2 rule 1).
func ParseCalcExpression(s string) ([]Ref, error) {
	toks, err := tokenize(s)
	if err != nil {
		return nil, err
	}
	p := &calcParser{toks: toks}
	if toks[0].kind == tokEOF {
		return nil, fmt.Errorf("empty expression")
	}
	if _, err := p.parseSum(); err != nil {
		return nil, err
	}
	if p.cur().kind != tokEOF {
		return nil, fmt.Errorf("unexpected token after expression: %q", p.cur().text)
	}
	return dedupeRefs(p.refs), nil
}

func (p *calcParser) cur() token { return p.toks[p.pos] }
func (p *calcParser) advance()   { p.pos++ }

func (p *calcParser) parseSum() (calcNode, error) {
	node, err := p.parseProduct()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokOp && (p.cur().text == "+" || p.cur().text == "-") {
		op := p.cur().text
		p.advance()
		rhs, err := p.parseProduct()
		if err != nil {
			return nil, err
		}
		node = calcBin{op: op, l: node, r: rhs}
	}
	return node, nil
}

func (p *calcParser) parseProduct() (calcNode, error) {
	node, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokOp && (p.cur().text == "*" || p.cur().text == "/") {
		op := p.cur().text
		p.advance()
		rhs, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		node = calcBin{op: op, l: node, r: rhs}
	}
	return node, nil
}

func (p *calcParser) parseFactor() (calcNode, error) {
	t := p.cur()
	switch t.kind {
	case tokNum:
		p.advance()
		return calcNum{v: t.num}, nil
	case tokRef:
		p.advance()
		p.refs = append(p.refs, t.ref)
		return calcRef{ref: t.ref, src: t.text}, nil
	case tokOp:
		if t.text == "-" { // unary minus
			p.advance()
			inner, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			return calcBin{op: "-", l: calcNum{v: 0}, r: inner}, nil
		}
		return nil, fmt.Errorf("unexpected operator %q", t.text)
	case tokLParen:
		p.advance()
		inner, err := p.parseSum()
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tokRParen {
			return nil, fmt.Errorf("unbalanced parentheses")
		}
		p.advance()
		return inner, nil
	default:
		return nil, fmt.Errorf("unexpected token in expression: %q", t.text)
	}
}

// EvalCalc evaluates a calculated expression against a resolver (Ref → stored
// value). The bool result is false when the value is empty for any evaluation
// problem (§6.3); problems lists each distinct operand-scoped issue. A parse
// error is returned as an error (the caller validates at design time first).
func EvalCalc(expr string, resolve func(Ref) (string, bool)) (value string, problems []Problem, err error) {
	toks, terr := tokenize(expr)
	if terr != nil {
		return "", nil, terr
	}
	p := &calcParser{toks: toks}
	node, perr := p.parseSum()
	if perr != nil {
		return "", nil, perr
	}
	if p.cur().kind != tokEOF {
		return "", nil, fmt.Errorf("unexpected token after expression")
	}
	ev := &calcEval{resolve: resolve}
	v, ok := node.eval(ev)
	if !ok {
		return "", ev.problems, nil
	}
	return formatNumber(v), ev.problems, nil
}

// parseNumber accepts the §4 integer and floating-point grammars.
func parseNumber(raw string) (float64, error) {
	if integerRE.MatchString(raw) || floatRE.MatchString(raw) {
		return strconv.ParseFloat(raw, 64)
	}
	return 0, fmt.Errorf("not a number: %q", raw)
}

// formatNumber renders the shortest exact decimal with no trailing zeros and
// no exponent (REQ-VAL-038 §6.3 step 4).
func formatNumber(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// --- branching-logic grammar (§7.1) — validation only ---

// ValidateBranching reports whether a branching expression is well-formed and
// resolves its references through the resolver (which reports whether a field
// reference names an active value-carrying field). It enforces §7.2: parse
// correctness, reference resolution, and function arity. The API never
// evaluates branching logic on a data path (DEV-VAL-5); this is design-time
// checking only. A nil resolver skips reference resolution (well-formedness
// check alone).
func ValidateBranching(expr string, resolve func(Ref) bool) error {
	toks, err := tokenize(expr)
	if err != nil {
		return err
	}
	if len(toks) == 1 && toks[0].kind == tokEOF {
		return fmt.Errorf("empty expression")
	}
	p := &branchParser{toks: toks, resolve: resolve}
	if err := p.parseOr(); err != nil {
		return err
	}
	if p.cur().kind != tokEOF {
		return fmt.Errorf("unexpected token after expression: %q", p.cur().text)
	}
	return nil
}

type branchParser struct {
	toks    []token
	pos     int
	resolve func(Ref) bool
	refs    []Ref
}

// ParseBranchingRefs returns the field references of a branching expression
// (first-seen order, deduplicated) without resolving them. Used by the
// field-delete check: deleting a field referenced by any stored expression
// is rejected while the reference is in use (§6.2 invariant).
func ParseBranchingRefs(s string) ([]Ref, error) {
	toks, err := tokenize(s)
	if err != nil {
		return nil, err
	}
	if len(toks) == 1 && toks[0].kind == tokEOF {
		return nil, fmt.Errorf("empty expression")
	}
	p := &branchParser{toks: toks}
	if err := p.parseOr(); err != nil {
		return nil, err
	}
	if p.cur().kind != tokEOF {
		return nil, fmt.Errorf("unexpected token after expression: %q", p.cur().text)
	}
	return dedupeRefs(p.refs), nil
}

func (p *branchParser) cur() token { return p.toks[p.pos] }
func (p *branchParser) advance()   { p.pos++ }

var branchingFuncs = map[string]int{
	"text_contains": 2,
	"is_blank":      1,
	"is_not_blank":  1,
}

func (p *branchParser) parseOr() error {
	if err := p.parseAnd(); err != nil {
		return err
	}
	for p.cur().kind == tokOp && p.cur().text == "||" {
		p.advance()
		if err := p.parseAnd(); err != nil {
			return err
		}
	}
	return nil
}

func (p *branchParser) parseAnd() error {
	if err := p.parseComparison(); err != nil {
		return err
	}
	for p.cur().kind == tokOp && p.cur().text == "&&" {
		p.advance()
		if err := p.parseComparison(); err != nil {
			return err
		}
	}
	return nil
}

func (p *branchParser) parseComparison() error {
	// A comparison is operand [op operand]; a bare operand is also valid
	// (truthiness, ASM-VAL-6).
	if err := p.parseOperand(); err != nil {
		return err
	}
	if p.cur().kind == tokOp && isCompareOp(p.cur().text) {
		p.advance()
		if err := p.parseOperand(); err != nil {
			return err
		}
	}
	return nil
}

func isCompareOp(op string) bool {
	switch op {
	case "=", "!=", "<", ">", "<=", ">=":
		return true
	}
	return false
}

func (p *branchParser) parseOperand() error {
	t := p.cur()
	switch t.kind {
	case tokRef:
		p.advance()
		if p.resolve != nil && !p.resolve(t.ref) {
			return fmt.Errorf("reference %s does not name an active value-carrying field", t.text)
		}
		p.refs = append(p.refs, t.ref)
		return nil
	case tokNum, tokStr:
		p.advance()
		return nil
	case tokLParen:
		p.advance()
		if err := p.parseOr(); err != nil {
			return err
		}
		if p.cur().kind != tokRParen {
			return fmt.Errorf("unbalanced parentheses")
		}
		p.advance()
		return nil
	case tokIdent:
		return p.parseCall()
	default:
		return fmt.Errorf("unexpected token in branching expression: %q", t.text)
	}
}

func (p *branchParser) parseCall() error {
	name := p.cur().text
	arity, ok := branchingFuncs[name]
	if !ok {
		return fmt.Errorf("unknown function %q", name)
	}
	p.advance()
	if p.cur().kind != tokLParen {
		return fmt.Errorf("expected ( after function %q", name)
	}
	p.advance()
	count := 0
	if p.cur().kind != tokRParen {
		for {
			if err := p.parseCallArg(); err != nil {
				return err
			}
			count++
			if p.cur().kind == tokComma {
				p.advance()
				continue
			}
			break
		}
	}
	if p.cur().kind != tokRParen {
		return fmt.Errorf("unbalanced parentheses in %q", name)
	}
	p.advance()
	if count != arity {
		return fmt.Errorf("function %q expects %d arguments, got %d", name, arity, count)
	}
	return nil
}

// parseCallArg accepts a reference, number, string, or (for text_contains's
// second argument) any operand.
func (p *branchParser) parseCallArg() error {
	t := p.cur()
	switch t.kind {
	case tokRef:
		p.advance()
		if p.resolve != nil && !p.resolve(t.ref) {
			return fmt.Errorf("reference %s does not name an active value-carrying field", t.text)
		}
		p.refs = append(p.refs, t.ref)
		return nil
	case tokNum, tokStr:
		p.advance()
		return nil
	default:
		return fmt.Errorf("unexpected token in function arguments: %q", t.text)
	}
}

func dedupeRefs(in []Ref) []Ref {
	seen := map[Ref]bool{}
	out := make([]Ref, 0, len(in))
	for _, r := range in {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
