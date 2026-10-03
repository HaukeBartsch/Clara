// The structure editors' behaviour — Setup (§6.2) and the Instrument Designer (§7). One module
// for that logical section (REQ-UI-045); no UI string lives here, every label comes from the
// injected data-i18n block (REQ-UI-008). Everything degrades: without this module the
// expression editors are plain text areas and every part of the field form is visible.
//
// 1. Expression editors (§7.3): a reference picker filled from the page route's `references`
//    data region on first use (REQ-UI-044), quick-insert buttons, and a highlighted read-back
//    of the expression. Assistance only — the API validates at save (REQ-VAL-029).
// 2. The field form (§7.2): shows the parts the chosen type and validation use, warns on
//    names over 26 characters (REQ-VAL-013), presets the direct-identifier flag for
//    identifier-shaped types and reports an explicit clear (REQ-EXP-020/021), and adds
//    choice rows.

import { el, fetchJson, ready, t } from "../app.js"

// --- 1. expression editors ---

const TOKEN = /\[[^\]]*\](?:\[[^\]]*\])?|"(?:[^"\\]|\\.)*"?|\d+(?:\.\d+)?|&&|\|\||!=|<=|>=|[=<>+\-*/()]|[A-Za-z_][A-Za-z0-9_]*|\s+|./gu

const tokenClass = (text) => {
  if (text.startsWith("[")) return "clara-tok-ref"
  if (text.startsWith('"')) return "clara-tok-str"
  if (/^\d/.test(text)) return "clara-tok-num"
  if (/^[A-Za-z_]/.test(text)) return "clara-tok-fn"
  if (/^\s+$/.test(text)) return ""
  if (/^(&&|\|\||!=|<=|>=|[=<>+\-*/()])$/.test(text)) return "clara-tok-op"
  return "clara-tok-other"
}

const highlight = (preview, source) => {
  const spans = (source.match(TOKEN) || []).map((text) => {
    const className = tokenClass(text)
    return className === "" ? document.createTextNode(text) : el("span", { class: className }, text)
  })
  preview.replaceChildren(...spans)
}

const insertAtCaret = (textarea, text) => {
  const start = textarea.selectionStart ?? textarea.value.length
  const end = textarea.selectionEnd ?? textarea.value.length
  textarea.setRangeText(text, start, end, "end")
  textarea.focus()
  textarea.dispatchEvent(new Event("input", { bubbles: true }))
}

// One fetch per page, shared by every editor on it: the region is the same for all of them.
const referenceCache = new Map()
const loadReferences = (url) => {
  if (!referenceCache.has(url)) {
    referenceCache.set(url, fetchJson(url).then((body) => body.references || []))
  }
  return referenceCache.get(url)
}

const fillPicker = async (select, url) => {
  if (select.dataset.loaded) return
  select.dataset.loaded = "1"
  const placeholder = select.options[0]
  placeholder.textContent = t("design.refs.loading")
  try {
    const references = await loadReferences(url)
    const byEvent = new Map()
    for (const ref of references) {
      if (!byEvent.has(ref.event)) byEvent.set(ref.event, [])
      byEvent.get(ref.event).push(ref)
    }
    for (const [event, refs] of byEvent) {
      const group = el("optgroup", { label: event })
      for (const ref of refs) {
        group.appendChild(el("option", { value: ref.reference }, ref.field + (ref.label ? " — " + ref.label : "")))
      }
      select.appendChild(group)
    }
    placeholder.textContent = references.length === 0 ? t("design.refs.none") : t("design.refs.choose")
  } catch (error) {
    placeholder.textContent = t("design.refs.failed")
    delete select.dataset.loaded
    referenceCache.delete(url)
  }
}

const wireExpression = (root) => {
  const textarea = root.querySelector("textarea")
  const preview = root.querySelector(".clara-expr-preview")
  const select = root.querySelector("[data-clara-refs-select]")
  if (!textarea) return

  const refresh = () => highlight(preview, textarea.value)
  textarea.addEventListener("input", refresh)
  refresh()

  for (const button of root.querySelectorAll("[data-clara-insert]")) {
    button.addEventListener("click", () => insertAtCaret(textarea, button.dataset.claraInsert))
  }

  if (select) {
    const url = root.dataset.claraRefs
    const load = () => fillPicker(select, url)
    select.addEventListener("focus", load)
    select.addEventListener("pointerdown", load)
    select.addEventListener("change", () => {
      if (select.value === "") return
      insertAtCaret(textarea, select.value)
      select.value = ""
    })
  }
}

// --- 2. the field form ---

const listOf = (value) => (value || "").split("|").filter((item) => item !== "")

const wireFieldForm = (form) => {
  const type = form.querySelector("[data-clara-field-type]")
  const validation = form.querySelector("[data-clara-validation-type]")
  const name = form.querySelector("[data-clara-field-name]")
  const nameWarning = form.querySelector("[data-clara-name-warning]")
  const direct = form.querySelector("[data-clara-direct-identifier]")
  const directCleared = form.querySelector("[data-clara-direct-cleared]")
  const directWarning = form.querySelector("[data-clara-direct-warning]")
  const identifierTypes = validation ? listOf(validation.dataset.claraIdentifierTypes) : []

  const sync = () => {
    for (const part of form.querySelectorAll("[data-clara-for-types]")) {
      part.classList.toggle("d-none", !listOf(part.dataset.claraForTypes).includes(type.value))
    }
    for (const part of form.querySelectorAll("[data-clara-for-validation]")) {
      part.classList.toggle("d-none", !validation || !listOf(part.dataset.claraForValidation).includes(validation.value))
    }
  }
  type.addEventListener("change", sync)
  sync()

  if (name && nameWarning) {
    name.addEventListener("input", () => nameWarning.classList.toggle("d-none", name.value.length <= 26))
  }

  if (validation && direct) {
    validation.addEventListener("change", () => {
      if (identifierTypes.includes(validation.value)) {
        direct.checked = true
        directCleared.value = "0"
        directWarning.classList.add("d-none")
      }
      sync()
    })
    direct.addEventListener("change", () => {
      const clearingPreset = !direct.checked && identifierTypes.includes(validation.value)
      directCleared.value = clearingPreset ? "1" : "0"
      directWarning.classList.toggle("d-none", !clearingPreset)
    })
  }

  const rows = form.querySelector("[data-clara-choice-rows]")
  const addChoice = form.querySelector("[data-clara-add-choice]")
  if (rows && addChoice) {
    addChoice.classList.remove("d-none")
    addChoice.addEventListener("click", () => {
      const last = rows.lastElementChild
      if (!last) return
      const row = last.cloneNode(true)
      for (const input of row.querySelectorAll("input")) input.value = ""
      rows.appendChild(row)
      row.querySelector("input").focus()
    })
  }
}

ready(() => {
  for (const root of document.querySelectorAll("[data-clara-expression]")) wireExpression(root)
  for (const form of document.querySelectorAll("[data-clara-field-form]")) wireFieldForm(form)
})
