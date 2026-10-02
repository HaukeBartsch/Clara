// The branching-logic evaluator — one module, imported by both the data-entry
// form and the public survey page so the two cannot diverge (REQ-UI-045,
// User_Interface_Design.md §8.4). It implements the single normative semantics
// of Data_Validation_Design.md §7.3 (operand resolution, choice-label→code,
// comparison kinds, truthiness, functions, &&/||) and mirrors
// api/internal/validate/expr.go entry for entry; e2e/tests/branching.spec.ts
// runs both evaluators over one shared corpus to prove it.
//
// Display-only: a false expression hides fields, it never blocks or alters a
// submission (GD-13, DEV-VAL-5). Pure ES2020 with no DOM access and no UI
// strings (REQ-UI-045); the caller resolves references against its own record.

const MAX_LENGTH = 4096 // runes of source text (security finding F5)
const MAX_DEPTH = 64 // nested parentheses

const FUNCTIONS = {
  text_contains: 2,
  is_blank: 1,
  is_not_blank: 1,
}

// --- tokenizer (mirrors expr.go tokenize with bareRefs = true) ---

const TWO_CHAR_OPS = ["&&", "||", "!=", "<=", ">="]
const ONE_CHAR_OPS = ["+", "-", "*", "/", "=", "<", ">"]

const isDigit = (c) => c >= "0" && c <= "9"
const isLetter = (c) => (c >= "a" && c <= "z") || (c >= "A" && c <= "Z")

function matchOperator(runes, i) {
  for (const op of TWO_CHAR_OPS) {
    if (runes[i] === op[0] && runes[i + 1] === op[1]) return op
  }
  for (const op of ONE_CHAR_OPS) {
    if (runes[i] === op) return op
  }
  return ""
}

function tokenize(source) {
  const runes = Array.from(source) // code points, like Go's []rune
  if (runes.length > MAX_LENGTH) {
    throw new Error(`expression exceeds the maximum of ${MAX_LENGTH} characters`)
  }
  const tokens = []
  let i = 0
  while (i < runes.length) {
    const c = runes[i]
    if (c === " " || c === "\t" || c === "\n" || c === "\r") {
      i += 1
    } else if (c === "[") {
      const end = runes.indexOf("]", i + 1) // absolute index of the first ']'
      if (end < 0) throw new Error(`unterminated field reference at offset ${i}`)
      const event = runes.slice(i + 1, end).join("")
      const j = end + 1 // index just past the first ']'
      if (runes[j] === "[") {
        const close2 = runes.indexOf("]", j + 1)
        if (close2 < 0) throw new Error(`unterminated field reference at offset ${j}`)
        const field = runes.slice(j + 1, close2).join("")
        tokens.push({ kind: "ref", ref: { event, field }, text: `[${event}][${field}]` })
        i = close2 + 1
      } else {
        // Single-segment [field]: names no event; the caller resolves it
        // against the project's first event in canonical order (GD-15).
        tokens.push({ kind: "ref", ref: { event: "", field: event }, text: `[${event}]` })
        i = j
      }
    } else if (c === '"') {
      let value = ""
      let closed = false
      i += 1
      while (i < runes.length) {
        if (runes[i] === "\\") {
          const next = runes[i + 1]
          if (next !== '"' && next !== "\\") {
            throw new Error(`invalid escape in string at offset ${i}`)
          }
          value += next
          i += 2
          continue
        }
        if (runes[i] === '"') {
          closed = true
          i += 1
          break
        }
        value += runes[i]
        i += 1
      }
      if (!closed) throw new Error("unterminated string literal")
      tokens.push({ kind: "str", str: value })
    } else if (c === "(") {
      tokens.push({ kind: "lparen" })
      i += 1
    } else if (c === ")") {
      tokens.push({ kind: "rparen" })
      i += 1
    } else if (c === ",") {
      tokens.push({ kind: "comma" })
      i += 1
    } else if (isDigit(c)) {
      let j = i + 1
      while (j < runes.length && (isDigit(runes[j]) || runes[j] === ".")) j += 1
      const literal = runes.slice(i, j).join("")
      const num = Number(literal)
      if (!Number.isFinite(num)) throw new Error(`invalid numeric literal ${literal}`)
      tokens.push({ kind: "num", num })
      i = j
    } else if (isLetter(c)) {
      let j = i
      while (j < runes.length && (isDigit(runes[j]) || isLetter(runes[j]) || runes[j] === "_")) {
        j += 1
      }
      tokens.push({ kind: "ident", text: runes.slice(i, j).join("") })
      i = j
    } else {
      const op = matchOperator(runes, i)
      if (op === "") throw new Error(`unexpected character ${c} at offset ${i}`)
      tokens.push({ kind: "op", text: op })
      i += op.length
    }
  }
  tokens.push({ kind: "eof" })
  return tokens
}

// --- parser (mirrors expr.go logicParser: || loosest, then &&, comparison,
// call; a bare operand is a truth test) ---

const isCompareOp = (op) => ["=", "!=", "<", ">", "<=", ">="].includes(op)

function parse(tokens) {
  let pos = 0
  let depth = 0
  const cur = () => tokens[pos]
  const advance = () => {
    pos += 1
  }

  const parseOr = () => {
    let node = parseAnd()
    while (cur().kind === "op" && cur().text === "||") {
      advance()
      node = { type: "or", l: node, r: parseAnd() }
    }
    return node
  }

  const parseAnd = () => {
    let node = parseComparison()
    while (cur().kind === "op" && cur().text === "&&") {
      advance()
      node = { type: "and", l: node, r: parseComparison() }
    }
    return node
  }

  const parseComparison = () => {
    if (cur().kind === "lparen") {
      advance()
      depth += 1
      if (depth > MAX_DEPTH) throw new Error(`expression nests deeper than ${MAX_DEPTH} levels`)
      const node = parseOr()
      depth -= 1
      if (cur().kind !== "rparen") throw new Error("unbalanced parentheses")
      advance()
      return node
    }
    if (cur().kind === "ident") return parseCall()
    const a = parseSimpleOperand()
    if (cur().kind === "op" && isCompareOp(cur().text)) {
      const op = cur().text
      advance()
      const b = parseSimpleOperand()
      return { type: "cmp", op, l: a, r: b }
    }
    return { type: "truthy", a }
  }

  const parseSimpleOperand = () => {
    const t = cur()
    if (t.kind === "ref") {
      advance()
      return { kind: "ref", ref: t.ref }
    }
    if (t.kind === "num") {
      advance()
      return { kind: "num", num: t.num }
    }
    if (t.kind === "str") {
      advance()
      return { kind: "str", str: t.str }
    }
    throw new Error(`unexpected token in expression: ${t.text ?? t.kind}`)
  }

  const parseCall = () => {
    const name = cur().text
    const arity = FUNCTIONS[name]
    if (arity === undefined) throw new Error(`unknown function ${name}`)
    advance()
    if (cur().kind !== "lparen") throw new Error(`expected ( after function ${name}`)
    advance()
    const args = []
    if (cur().kind !== "rparen") {
      for (;;) {
        args.push(parseSimpleOperand())
        if (cur().kind === "comma") {
          advance()
          continue
        }
        break
      }
    }
    if (cur().kind !== "rparen") throw new Error(`unbalanced parentheses in ${name}`)
    advance()
    if (args.length !== arity) {
      throw new Error(`function ${name} expects ${arity} arguments, got ${args.length}`)
    }
    return { type: "call", name, args }
  }

  if (tokens[0].kind === "eof") throw new Error("empty expression")
  const root = parseOr()
  if (cur().kind !== "eof") throw new Error(`unexpected token after expression: ${cur().text}`)
  return root
}

// --- number forms (mirrors expr.go parseNumber / formatNumber) ---

const INTEGER_RE = /^-?[0-9]+$/
const FLOAT_RE = /^[+-]?[0-9]+(\.[0-9]+)?$/

function parseNumber(raw) {
  if (INTEGER_RE.test(raw) || FLOAT_RE.test(raw)) return Number(raw)
  return null
}

// formatNumber renders the shortest exact decimal with no trailing zeros and
// no exponent — Go's strconv.FormatFloat(v, 'f', -1, 64).
function formatNumber(v) {
  if (Number.isInteger(v) && Math.abs(v) < Number.MAX_SAFE_INTEGER) return String(v)
  const text = String(v)
  const [mantissa, exponent] = text.split(/e/i)
  if (exponent === undefined) return mantissa.replace(/\.?0+$/, "") || "0"
  // Expand the exponential form Go never emits.
  const sign = exponent.startsWith("-") ? -1 : 1
  const power = Math.abs(Number(exponent))
  const digits = mantissa.replace(".", "").replace(/^([+-])/, "")
  const pointIndex = (mantissa.indexOf(".") >= 0 ? mantissa.indexOf(".") : digits.length) + sign * power
  let out
  if (pointIndex <= 0) {
    out = "0." + "0".repeat(-pointIndex) + digits
  } else if (pointIndex >= digits.length) {
    out = digits + "0".repeat(pointIndex - digits.length)
  } else {
    out = digits.slice(0, pointIndex) + "." + digits.slice(pointIndex)
  }
  return (mantissa.startsWith("-") ? "-" : "") + (out.replace(/\.?0+$/, "") || "0")
}

// --- comparison inputs (mirrors expr.go logicOperand.resolve) ---

function operandValue(o, env) {
  if (o.kind === "ref") return env.value(o.ref.event, o.ref.field) ?? ""
  if (o.kind === "num") return formatNumber(o.num)
  return o.str
}

function operandResolve(o, env) {
  const val = operandValue(o, env)
  let present = true
  if (o.kind === "ref") present = val !== ""
  if (o.kind === "num") return { val, present, num: o.num, isNum: true }
  const num = parseNumber(val)
  return { val, present, num: num ?? 0, isNum: num !== null }
}

// CodeForLabel resolves a string constant that names a choice label of the
// opposite reference's field to its code (validate.go CodeForLabel; choices
// carry the stored code$label##… encoding). Anything else passes through.
function codeForLabel(choices, label) {
  for (const pair of choices.split("##")) {
    if (pair === "") continue
    const k = pair.indexOf("$")
    if (k >= 0 && pair.slice(k + 1) === label) return pair.slice(0, k)
  }
  return ""
}

function resolveChoiceLabel(env, o, other, val, isNum) {
  if (o.kind !== "str" || other.kind !== "ref" || !env.choices) return [val, isNum]
  const code = codeForLabel(env.choices(other.ref.event, other.ref.field) ?? "", val)
  return code !== "" ? [code, false] : [val, isNum]
}

// --- comparison kinds (mirrors expr.go logicCmp.eval) ---

const compareFloats = (l, r, op) =>
  ({ "=": l === r, "!=": l !== r, "<": l < r, ">": l > r, "<=": l <= r, ">=": l >= r })[op] ?? false

// Byte-wise UTF-8 lexicographic order — Go's string comparison. JS string
// ordering is UTF-16 and disagrees for supplementary characters, so compare
// the encoded bytes.
const encoder = new TextEncoder()
function compareStrings(l, r, op) {
  const a = encoder.encode(l)
  const b = encoder.encode(r)
  let cmp = 0
  for (let i = 0; i < Math.min(a.length, b.length); i += 1) {
    if (a[i] !== b[i]) {
      cmp = a[i] - b[i]
      break
    }
    cmp = a.length - b.length
  }
  return compareFloats(cmp, 0, op)
}

// parseCanonicalInstant parses the §4.1 canonical date/date-time forms (with
// or without the ±HH:MM collection offset; a missing offset reads as UTC) into
// an absolute instant in epoch seconds (§7.3). Non-canonical or out-of-range
// components are not instants.
// The §4.1 canonical forms attach the offset directly ("2026-03-01 09:30+01:00");
// a space-separated offset is accepted as well, mirroring expr.go's parse.
const INSTANT_RE =
  /^(\d{4})-(\d{1,2})-(\d{1,2})(?: (\d{1,2}):(\d{1,2})(?::(\d{1,2}))?)?(?: ?)([+-])(\d{2}):?(\d{2})?$/

function daysInMonth(year, month) {
  const leap = (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0
  return [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1]
}

function parseCanonicalInstant(s) {
  const m = INSTANT_RE.exec(s)
  if (!m) return null
  const [year, month, day] = [Number(m[1]), Number(m[2]), Number(m[3])]
  const hour = m[4] === undefined ? 0 : Number(m[4])
  const minute = m[5] === undefined ? 0 : Number(m[5])
  const second = m[6] === undefined ? 0 : Number(m[6])
  if (month < 1 || month > 12) return null
  if (day < 1 || day > daysInMonth(year, month)) return null
  if (hour > 23 || minute > 59 || second > 59) return null
  const date = new Date(Date.UTC(2000, month - 1, day, hour, minute, second))
  date.setUTCFullYear(year) // Date.UTC would map years 0–99 into the 1900s
  let seconds = date.getTime() / 1000
  if (m[7] !== undefined) {
    const offset = Number(m[8]) * 3600 + Number(m[9]) * 60
    seconds -= m[7] === "-" ? -offset : offset
  }
  return seconds
}

function evalCmp(node, env) {
  const left = operandResolve(node.l, env)
  const right = operandResolve(node.r, env)
  // A missing/empty referenced value makes every operator 0 (ASM-VAL-6).
  if ((node.l.kind === "ref" && !left.present) || (node.r.kind === "ref" && !right.present)) {
    return 0
  }
  const [lv, lisn] = resolveChoiceLabel(env, node.l, node.r, left.val, left.isNum)
  const [rv, risn] = resolveChoiceLabel(env, node.r, node.l, right.val, right.isNum)

  if (lisn && risn) return compareFloats(left.num, right.num, node.op) ? 1 : 0
  const lt = parseCanonicalInstant(lv)
  const rt = parseCanonicalInstant(rv)
  if (lt !== null && rt !== null) return compareFloats(lt, rt, node.op) ? 1 : 0
  return compareStrings(lv, rv, node.op) ? 1 : 0
}

// truthiness (mirrors expr.go truthiness): empty → 0; a choice-field reference
// is presence-based (REQ-VAL-029); numeric 0/0.0 → 0; else non-empty → 1.
function truthiness(o, env) {
  if (o.kind === "num") return o.num !== 0 ? 1 : 0
  const v = operandValue(o, env)
  if (v === "") return 0
  if (o.kind === "ref" && env.choices && (env.choices(o.ref.event, o.ref.field) ?? "") !== "") {
    return 1
  }
  const num = parseNumber(v)
  return num !== null && num === 0 ? 0 : 1
}

function evalNode(node, env) {
  switch (node.type) {
    case "or":
      return evalNode(node.l, env) === 1 || evalNode(node.r, env) === 1 ? 1 : 0
    case "and":
      return evalNode(node.l, env) === 1 && evalNode(node.r, env) === 1 ? 1 : 0
    case "cmp":
      return evalCmp(node, env)
    case "truthy":
      return truthiness(node.a, env)
    case "call": {
      const values = node.args.map((a) => operandValue(a, env))
      if (node.name === "is_blank") return values[0] === "" ? 1 : 0
      if (node.name === "is_not_blank") return values[0] !== "" ? 1 : 0
      // text_contains: missing/empty → 0 (§7.3)
      return values[0] !== "" && values[0].includes(values[1]) ? 1 : 0
    }
    default:
      throw new Error(`unknown node ${node.type}`)
  }
}

// --- public surface ---

/**
 * Compiles a branching/filterLogic expression (§7.1) into a program that can
 * be evaluated against many records. The environment resolves references the
 * way Go's LogicEval does: value(event, field) returns the stored value (""
 * when absent), choices(event, field) the code$label##… encoding ("" for
 * non-choice fields). A reference with an empty event is the [field] shorthand
 * — the caller resolves it to the project's first event in canonical order
 * (GD-15) before lookup. Throws on a malformed expression (§7.2 rules 1/3).
 */
export function compileBranching(expr) {
  const root = parse(tokenize(expr))
  return { evaluate: (env) => evalNode(root, env) === 1 }
}

/** One-shot convenience over compileBranching for a single record. */
export function evaluateBranching(expr, env) {
  return compileBranching(expr).evaluate(env)
}

/** The expression's field references in first-seen order, deduplicated — the
 * set a form re-evaluates on change of (§7.4); mirrors ParseBranchingRefs. */
export function branchingRefs(expr) {
  const root = parse(tokenize(expr))
  const seen = new Set()
  const refs = []
  const walk = (node) => {
    if (node.type === "or" || node.type === "and") {
      walk(node.l)
      walk(node.r)
    } else if (node.type === "cmp") {
      collect(node.l)
      collect(node.r)
    } else if (node.type === "truthy") {
      collect(node.a)
    } else if (node.type === "call") {
      node.args.forEach(collect)
    }
  }
  const collect = (o) => {
    if (o.kind !== "ref") return
    const key = o.ref.event + "|" + o.ref.field
    if (!seen.has(key)) {
      seen.add(key)
      refs.push({ event: o.ref.event, field: o.ref.field })
    }
  }
  walk(root)
  return refs
}
