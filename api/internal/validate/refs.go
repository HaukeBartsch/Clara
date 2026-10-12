package validate

import "strings"

// Source-level rewriting of the field references in an expression — what the
// instrument import (REQ-API-152, API_Endpoints_Design.md §4.10) needs when it
// renames a field with `_2` (every reference to it follows) or carries a
// REDCap calculation over (its bare [field] references gain the event the
// calculation grammar requires, §6.1). The rest of the expression is kept
// byte for byte: spacing, quoting and REDCap's spellings are the designer's.

// refSpan is one reference as written: the rune offsets of its whole
// bracketed text, and its segments — event is "" for the single-segment form.
type refSpan struct {
	start, end int
	event      string
	field      string
}

// scanRefSpans finds the references of an expression without parsing it.
// Brackets inside a string literal — double-quoted, or single-quoted in
// REDCap's spelling (REQ-VAL-044) — are text, not references. A malformed
// tail (an unterminated bracket) ends the scan; the parsers report it.
func scanRefSpans(s string) []refSpan {
	runes := []rune(s)
	var out []refSpan
	var quote rune
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++ // the escaped rune is part of the string
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '[':
			end := indexRune(runes, ']', i+1)
			if end < 0 {
				return out
			}
			first := string(runes[i+1 : end])
			if end+1 < len(runes) && runes[end+1] == '[' {
				end2 := indexRune(runes, ']', end+2)
				if end2 < 0 {
					return out
				}
				out = append(out, refSpan{start: i, end: end2 + 1, event: first, field: string(runes[end+2 : end2])})
				i = end2
				continue
			}
			out = append(out, refSpan{start: i, end: end + 1, field: first})
			i = end
		}
	}
	return out
}

func indexRune(runes []rune, r rune, from int) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == r {
			return i
		}
	}
	return -1
}

// splitRefField splits a reference's field segment into the field it names
// and the checkbox suffix, which keeps the spelling it was written in: "(code)"
// for REDCap's form, "___code" for the value-column name (REQ-DB-042), ""
// otherwise. isBox tells whether a ___ suffix really is a box — a field name
// may contain three underscores of its own.
func splitRefField(segment string, isBox func(name string) bool) (base, suffix string) {
	if m := boxRefRE.FindStringSubmatch(segment); m != nil {
		return m[1], "(" + m[2] + ")"
	}
	if isBox != nil && isBox(segment) {
		if i := strings.LastIndex(segment, "___"); i > 0 {
			return segment[:i], segment[i:]
		}
	}
	return segment, ""
}

// rewriteRefs rebuilds s with each reference replaced by fn's text for it.
func rewriteRefs(s string, fn func(refSpan) string) string {
	spans := scanRefSpans(s)
	if len(spans) == 0 {
		return s
	}
	runes := []rune(s)
	var sb strings.Builder
	last := 0
	for _, sp := range spans {
		sb.WriteString(string(runes[last:sp.start]))
		sb.WriteString(fn(sp))
		last = sp.end
	}
	sb.WriteString(string(runes[last:]))
	return sb.String()
}

// RenameRefs rewrites every reference to a renamed field — `[f]`,
// `[event][f]`, and a box of a renamed checkbox in either spelling,
// `[f(code)]` or `[f___code]` — to the new name; every other byte of the
// expression stays as written. isBox reports whether a name is a checkbox
// box (nil: only the (code) spelling is recognised as one).
func RenameRefs(expr string, renamed map[string]string, isBox func(name string) bool) string {
	if len(renamed) == 0 {
		return expr
	}
	return rewriteRefs(expr, func(sp refSpan) string {
		base, suffix := splitRefField(sp.field, isBox)
		to, ok := renamed[base]
		if !ok {
			return formatRef(sp.event, sp.field)
		}
		return formatRef(sp.event, to+suffix)
	})
}

// QualifyRefs gives every single-segment reference the event: `[f]` becomes
// `[event][f]`, and REDCap's box spelling `[f(code)]` becomes the
// value-column name `[event][f___code]` — the event-qualified shape the
// calculation grammar requires (§6.1, REQ-VAL-033). An event-qualified box in
// REDCap's spelling is converted the same way.
func QualifyRefs(expr, event string) string {
	return rewriteRefs(expr, func(sp refSpan) string {
		ev := sp.event
		if ev == "" {
			ev = event
		}
		return formatRef(ev, boxField(sp.field))
	})
}

func formatRef(event, field string) string {
	if event == "" {
		return "[" + field + "]"
	}
	return "[" + event + "][" + field + "]"
}
