// The behaviour every instrument form shares — the record view's data-entry form (§8) and
// the public survey page (§8.8) render one instrument the same way (views/partials/
// form-fields.php) and run the same client code over it, so the two cannot diverge
// (REQ-UI-045, User_Interface_Design.md §8.4). Nothing here changes what a form submits:
// the submission policy (GD-14) travels in the form's `was[…]` fields and is applied
// server-side, so it holds without this module too.
//
// 1. Branching (§8.4, GD-13): the shared evaluator of branching.js shows and hides fields —
//    and the instrument itself — while their logic is true; re-run on every change.
//    Display-only: a hidden field is still submitted as it stands (DEV-VAL-5). A reference
//    to a field of this form reads its input; any other reads the current value the server
//    put in the record-context block. A form that knows only its own fields (the survey
//    page) sets `anyEvent`, so a reference to one of them reads the input whatever event it
//    names.
// 2. Advisory validation (REQ-VAL-002): type, min/max, date format and registry pattern
//    hints as the user types; the API decides (REQ-VAL-001).
// 3. Required fields (REQ-VAL-028): a submit with visible required fields empty is held
//    once with the list; the second click saves anyway — partial records are first-class
//    (GD-14, ASM-VAL-3).
// 4. Small things: clearing a radio, and the browser timezone for date values (GD-16).

import { compileBranching } from "./branching.js"
import { t } from "../app.js"

// --- shared state ---

export const readContext = () => {
  const block = document.querySelector("script[data-clara-record-context]")
  try {
    return block ? JSON.parse(block.textContent || "{}") : {}
  } catch (error) {
    return {}
  }
}

export const fieldValue = (form, wrapper) => {
  const name = wrapper.dataset.claraField
  const control = form.elements.namedItem("value[" + name + "]")
  if (control) return control.value ?? ""
  return wrapper.dataset.claraValue ?? ""
}

// --- 1. branching ---

function compile(source) {
  if (!source) return null
  try {
    return compileBranching(source)
  } catch (error) {
    return null // a malformed expression shows the item: never hide data by accident
  }
}

export function wireBranching(form, context) {
  const fields = new Map()
  for (const wrapper of form.querySelectorAll("[data-clara-field]")) {
    fields.set(wrapper.dataset.claraField, wrapper)
  }

  const env = {
    value: (event, field) => {
      const ev = event || context.firstEvent
      if ((context.anyEvent || ev === context.event) && fields.has(field)) return fieldValue(form, fields.get(field))
      return (context.values || {})[ev + "|" + field] ?? ""
    },
    choices: (event, field) => {
      const ev = event || context.firstEvent
      if ((context.anyEvent || ev === context.event) && fields.has(field)) return fields.get(field).dataset.claraChoices || ""
      return ""
    },
  }

  const governed = []
  for (const wrapper of fields.values()) {
    const program = compile(wrapper.dataset.claraBranching)
    if (program) governed.push({ node: wrapper, program })
  }
  const instruments = []
  for (const item of document.querySelectorAll(".clara-instrument-nav [data-clara-branching]")) {
    const program = compile(item.dataset.claraBranching)
    if (program) instruments.push({ node: item, program, name: item.dataset.claraInstrument })
  }
  const notice = document.querySelector("[data-clara-instrument-hidden]")

  const evaluate = () => {
    for (const { node, program } of governed) {
      node.classList.toggle("d-none", !program.evaluate(env))
    }
    for (const { node, program, name } of instruments) {
      const shown = program.evaluate(env)
      node.classList.toggle("d-none", !shown && name !== form.dataset.instrument)
      if (name === form.dataset.instrument) {
        // A hidden instrument hides all of its fields (REQ-VAL-040).
        form.classList.toggle("d-none", !shown)
        if (notice) notice.classList.toggle("d-none", shown)
      }
    }
  }

  form.addEventListener("input", evaluate)
  form.addEventListener("change", evaluate)
  evaluate()
}

// --- 2. advisory validation ---

const INTEGER = /^-?[0-9]+$/
const FLOAT = /^[+-]?[0-9]+(\.[0-9]+)?$/
// A stored value comes back canonical — wall time plus the collection offset (§4.1) — and
// the API accepts it unchanged, so the hint must too.
const CANONICAL_DATE = /^\d{4}-\d{2}-\d{2}( \d{2}:\d{2})?[+-]\d{2}:\d{2}$/

function formatPattern(format) {
  const tokens = { Y: "\\d{4}", m: "\\d{1,2}", d: "\\d{1,2}", H: "\\d{1,2}", i: "\\d{2}" }
  let source = ""
  for (const ch of format) {
    source += tokens[ch] ?? ch.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
  }
  return new RegExp("^" + source + "$")
}

function registryPattern(pattern) {
  try {
    return new RegExp("^(?:" + pattern + ")$", "u")
  } catch (error) {
    return null // an RE2 construct JavaScript does not know: no hint rather than a wrong one
  }
}

function hintFor(wrapper, value) {
  if (value === "") return ""
  const type = wrapper.dataset.claraValidation || ""
  const { claraMin: min, claraMax: max, claraFormat: format, claraPattern: pattern } = wrapper.dataset

  if (type === "integer" || type === "floating point") {
    if (!(type === "integer" ? INTEGER : FLOAT).test(value)) return t(type === "integer" ? "record.hint.integer" : "record.hint.float")
    if (min !== undefined && min !== "" && Number(value) < Number(min)) return t("record.hint.min", { min })
    if (max !== undefined && max !== "" && Number(value) > Number(max)) return t("record.hint.max", { max })
    return ""
  }
  if ((type === "date" || type === "datetime") && format) {
    return formatPattern(format).test(value) || CANONICAL_DATE.test(value) ? "" : t("record.hint.format", { format })
  }
  if (pattern) {
    const re = registryPattern(pattern)
    return re && !re.test(value) ? t("record.hint.pattern") : ""
  }
  return ""
}

export function wireHints(form) {
  const check = (event) => {
    const wrapper = event.target.closest?.("[data-clara-field]")
    if (!wrapper || wrapper.dataset.claraType !== "text") return
    const hint = wrapper.querySelector("[data-clara-hint]")
    if (!hint) return
    const text = hintFor(wrapper, fieldValue(form, wrapper))
    hint.textContent = text
    hint.classList.toggle("d-none", text === "")
  }
  form.addEventListener("input", check)
  form.addEventListener("change", check)
}

// --- 3. required fields ---

export function wireRequired(form) {
  const warning = form.querySelector("[data-clara-required-warning]")
  if (!warning) return
  let heldFor = null

  form.addEventListener("submit", (event) => {
    const missing = []
    for (const wrapper of form.querySelectorAll("[data-clara-required='1']")) {
      if (wrapper.closest(".d-none")) continue // hidden by branching: not asked for
      if (!form.elements.namedItem("value[" + wrapper.dataset.claraField + "]")) continue
      if (fieldValue(form, wrapper) === "") missing.push(wrapper.dataset.claraField)
    }
    const signature = missing.join(",")
    if (missing.length === 0 || heldFor === signature) {
      warning.classList.add("d-none")
      return
    }
    // Held once: the user sees what is missing and may save the partial form on the next click.
    event.preventDefault()
    heldFor = signature
    warning.textContent = t("record.required_missing", { fields: missing.join(", ") })
    warning.classList.remove("d-none")
    for (const name of missing) {
      form.querySelector(`[data-clara-field="${CSS.escape(name)}"]`)?.classList.add("clara-field-missing")
    }
  })
}

// --- 4. small things ---

export function wireRadioReset(form) {
  for (const button of form.querySelectorAll("[data-clara-reset]")) {
    button.classList.remove("d-none")
    button.addEventListener("click", () => {
      const scope = button.closest("tr, [role=radiogroup]")
      for (const radio of scope.querySelectorAll("input[type=radio]")) radio.checked = false
      form.dispatchEvent(new Event("change", { bubbles: true }))
    })
  }
}

/** The browser's timezone into the form's `tz` field — the collection zone of its dates. */
export function fillTimezone(form) {
  const zone = form.querySelector("[data-clara-tz]")
  if (!zone) return
  try {
    zone.value = Intl.DateTimeFormat().resolvedOptions().timeZone || ""
  } catch (error) {
    zone.value = "" // the API falls back to APP_TIMEZONE (REQ-VAL-041)
  }
}
