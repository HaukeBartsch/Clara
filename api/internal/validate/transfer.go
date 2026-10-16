package validate

// Transfer-field copy instructions (REQ-VAL-046, REQ-DB-050): the grammar of
// the `transfer` field's choices text, per Data_Validation_Design.md. Pure and
// DB-free like the expression grammars — structure only; existence checks over
// the parsed references (source fields on this instrument, target project /
// event / field resolution) belong to the design-time validator, which has the
// dictionary in hand.

import (
	"fmt"
	"strings"
)

// Caps for the transfer grammar (finding F5 applies here too): the config is
// admin-entered text stored in fields.choices; bound its length and breadth.
const (
	maxTransferConfigLen = 16384 // runes of instruction text
	maxTransferOps       = 256   // operations in one config
)

// TransferOp is one operation of a transfer field's instruction text. Kind is
// "create" or "copy". SourceField names a value-carrying field of the same
// instrument as the transfer field. TargetProject is a project name; for
// "create", RecordIDField is the target project's record identifier field
// (the created record's name is SourceField's value); for "copy", Event is the
// target event token ("" when the config addresses an event-less target) and
// TargetField the destination field.
type TransferOp struct {
	Kind          string
	SourceField   string
	TargetProject string
	RecordIDField string
	Event         string
	TargetField   string
}

// ParseTransferConfig parses a transfer field's instruction text: line-oriented,
// '#' starts an end-of-line comment, blank lines are ignored. A token is a bare
// lower-case identifier or one or more adjacent bracket groups ([proj][base]);
// whitespace separates tokens. Operations:
//
//	create [project][identifier_field] <source_field>
//	copy   <source_field> [project][event][target_field]   (event-enabled target)
//	copy   <source_field> [project][target_field]          (event-less target)
//
// Exactly one create operation must appear, before any copy: one button
// transfers to one target record, which is what makes re-triggering an update
// (REQ-API-165). Bracketed names may not contain brackets; whitespace inside a
// group is kept trimmed — whether a name resolves is the design-time check.
// Returns line-numbered errors.
func ParseTransferConfig(s string) ([]TransferOp, error) {
	if len([]rune(s)) > maxTransferConfigLen {
		return nil, fmt.Errorf("transfer config longer than %d characters", maxTransferConfigLen)
	}
	var ops []TransferOp
	for i, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		if len(ops) == maxTransferOps {
			return nil, fmt.Errorf("line %d: more than %d operations", i+1, maxTransferOps)
		}
		op, err := parseTransferLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		switch op.Kind {
		case "create":
			if len(ops) > 0 {
				// ops[0] is checked first; a second create anywhere is ambiguous ownership.
				return nil, fmt.Errorf("line %d: only one create operation is allowed", i+1)
			}
		case "copy":
			if len(ops) == 0 {
				return nil, fmt.Errorf("line %d: copy before the create operation", i+1)
			}
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("transfer config has no operations")
	}
	if ops[0].Kind != "create" {
		return nil, fmt.Errorf("the first operation must be create")
	}
	return ops, nil
}

// transferPart is one name of a token: bracketed reports whether it was
// written in [brackets] or bare.
type transferPart struct {
	text      string
	bracketed bool
}

// parseTransferLine parses one instruction line (comment stripped, trimmed).
func parseTransferLine(line string) (TransferOp, error) {
	tokens, err := lexTransferLine(line)
	if err != nil {
		return TransferOp{}, err
	}
	var op TransferOp
	switch {
	case isKeyword(tokens, 0, "create") && len(tokens) == 3:
		target, err := bracketedNames(tokens[1], 2)
		if err != nil {
			return TransferOp{}, fmt.Errorf("create target must be [project][identifier_field]: %w", err)
		}
		src, err := singleName(tokens[2])
		if err != nil {
			return TransferOp{}, fmt.Errorf("create needs one source field: %w", err)
		}
		op = TransferOp{Kind: "create", TargetProject: target[0], RecordIDField: target[1], SourceField: src}
	case isKeyword(tokens, 0, "copy") && len(tokens) == 3:
		src, err := singleName(tokens[1])
		if err != nil {
			return TransferOp{}, fmt.Errorf("copy needs one source field: %w", err)
		}
		names, err := bracketedNames(tokens[2], 2, 3)
		if err != nil {
			return TransferOp{}, fmt.Errorf("copy target must be [project][field] or [project][event][field]: %w", err)
		}
		op = TransferOp{Kind: "copy", SourceField: src, TargetProject: names[0], TargetField: names[len(names)-1]}
		if len(names) == 3 {
			op.Event = names[1]
		}
	case len(tokens) > 0 && len(tokens[0]) == 1 && !tokens[0][0].bracketed &&
		(tokens[0][0].text == "create" || tokens[0][0].text == "copy"):
		return TransferOp{}, fmt.Errorf("malformed %s operation", tokens[0][0].text)
	default:
		return TransferOp{}, fmt.Errorf("expected create or copy, got %q", line)
	}
	return op, nil
}

// lexTransferLine splits one line into tokens of adjacent parts; a part is a
// bracket group or a bare identifier run. Whitespace separates tokens only at
// the top level — [a][b] is one token, [a] [b] two.
func lexTransferLine(line string) ([]([]transferPart), error) {
	var tokens [][]transferPart
	var cur []transferPart
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, cur)
			cur = nil
		}
	}
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
			i++
		case c == '[':
			end := strings.IndexByte(line[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated [ in %q", line[i:])
			}
			name := strings.TrimSpace(line[i+1 : i+end])
			if strings.ContainsRune(name, '[') {
				return nil, fmt.Errorf("unterminated [ in %q", line[i:i+end])
			}
			if name == "" {
				return nil, fmt.Errorf("empty bracket group []")
			}
			cur = append(cur, transferPart{text: name, bracketed: true})
			i += end + 1
		case isBareNameByte(c):
			j := i
			for j < len(line) && isBareNameByte(line[j]) {
				j++
			}
			cur = append(cur, transferPart{text: line[i:j], bracketed: false})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q", string(rune(c)))
		}
	}
	flush()
	return tokens, nil
}

// isKeyword reports whether token i of the line is the bare keyword word.
func isKeyword(tokens [][]transferPart, i int, word string) bool {
	return len(tokens) > i && len(tokens[i]) == 1 && !tokens[i][0].bracketed && tokens[i][0].text == word
}

// bracketedNames returns the names of a token written as exactly n (or one of
// ns) adjacent bracket groups.
func bracketedNames(token []transferPart, ns ...int) ([]string, error) {
	for _, n := range ns {
		if len(token) == n {
			names := make([]string, 0, n)
			for _, p := range token {
				if !p.bracketed {
					return nil, fmt.Errorf("%q must be written in [brackets]", p.text)
				}
				names = append(names, p.text)
			}
			return names, nil
		}
	}
	return nil, fmt.Errorf("expected %s bracket groups, got %d", strings.Join(intList(ns), " or "), len(token))
}

// singleName returns the name of a one-part token, bracketed or bare.
func singleName(token []transferPart) (string, error) {
	if len(token) != 1 {
		return "", fmt.Errorf("expected one field name, got %d parts", len(token))
	}
	return token[0].text, nil
}

func isBareNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

func intList(ns []int) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = fmt.Sprintf("%d", n)
	}
	return out
}
