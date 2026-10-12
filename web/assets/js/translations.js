// The Translations section's key table (§5.7, REQ-UI-030): a Tabulator-backed data region
// bound from this route's JSON (REQ-UI-044) — client-side key filter and missing-only
// toggle over the full universe, inline editing of the translation column, spreadsheet
// export/import of the selected language (REQ-UI-049), and a Save that collects edited
// cells into hidden inputs on the POST form. The save stays a CSRF-protected PRG mutation
// (§3.3); the server still diffs against the stored strings and sends only what changed
// (REQ-API-100). No UI string appears here — every label comes from the injected
// data-i18n block (REQ-UI-008) — and all DOM goes through createElement/textContent (§3.2).

import { el, fetchJson, ready, t, tabulatorOptions } from "../app.js"
import { TabulatorFull as Tabulator } from "../vendor/tabulator/tabulator_esm.min.js"

const container = document.querySelector("[data-region='translations']")
let table = null
// Tabulator builds its DOM asynchronously; filters and the status line only run once the
// `tableBuilt` event has fired, or they would read an empty model (v6 warns about exactly this).
let built = false
const originalText = new Map()

/** The missing count is the universe's property, not the filtered view's — it is what the
 *  §5.7 worklist reports — so it is computed from the fetched rows, never from `active`. */
function loadStatus() {
  const status = document.getElementById("tr-status")
  if (status === null || table === null) {
    return
  }

  let missing = 0
  let edited = 0
  for (const row of table.getData()) {
    if (row.missing) {
      missing++
    }
    if (String(row.text ?? "") !== originalText.get(row.key)) {
      edited++
    }
  }

  const parts = [t("admin.translations.missing_status", { missing: String(missing), total: String(originalText.size) })]
  if (edited > 0) {
    parts.push(t("admin.translations.edited", { count: String(edited) }))
  }
  status.textContent = parts.join(" · ")

  const save = document.getElementById("tr-save")
  if (save !== null) {
    save.disabled = edited === 0
  }
}

/** Key cell: the code plus the missing badge where the key falls back to English (§5.7). */
function keyCell(cell) {
  const row = cell.getData()
  const wrap = el("span", { class: "clara-tr-key" }, [el("code", null, String(row.key))])
  if (row.missing) {
    wrap.appendChild(el("span", { class: "badge text-bg-warning ms-1" }, t("admin.translations.missing")))
  }

  return wrap
}

function applyFilters() {
  if (table === null || !built) {
    return
  }
  const filters = []
  const missingBox = document.getElementById("tr-missing")
  if (missingBox !== null && missingBox.checked) {
    filters.push({ field: "missing", type: "=", value: true })
  }
  const search = document.getElementById("tr-search")
  const term = search === null ? "" : search.value.trim()
  if (term !== "") {
    filters.push({ field: "key", type: "like", value: term })
  }
  table.setFilter(filters)
}

// --- Spreadsheet export and import (REQ-UI-049) -----------------------------------
// Both halves work client-side over the loaded region: an export serialises the table's
// current data to a CSV download, an import parses a spreadsheet CSV — or the flat JSON
// seed shape of tools/i18n — and stages its values into the grid as unsaved edits. Nothing
// here talks to the API: applied texts reach storage only through the same PRG save as
// inline edits (REQ-UI-005), an import never adds a key (rows whose key is not in the
// universe are skipped), entries absent from the file keep their text, and empty cells
// change nothing — clearing a translation stays a grid edit followed by Save.

/** One RFC 4180 field: quoted when it carries a quote, comma or newline; quotes double up. */
function csvField(value) {
  const text = String(value ?? "")

  return /[",\r\n]/.test(text) ? "\"" + text.split("\"").join("\"\"") + "\"" : text
}

/** The table as spreadsheet text — UTF-8 BOM first so Excel reads Norwegian characters. */
function exportCsv() {
  const rows = [["key", "english", "text"]]
  for (const row of table.getData()) {
    rows.push([String(row.key), String(row.english ?? ""), String(row.text ?? "")])
  }
  const csv = String.fromCharCode(0xfeff) + rows.map((row) => row.map(csvField).join(",")).join("\r\n") + "\r\n"
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" }))
  const link = el("a", { href: url, download: "clara-translations-" + (container.dataset.language || "") + ".csv" })
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 10000)
}

/** RFC 4180 rows of fields: quoted fields may carry commas, doubled quotes and newlines. */
function parseCsv(text) {
  // A BOM from Excel or a seed file is metadata, not part of the first field.
  const source = text.charCodeAt(0) === 0xfeff ? text.slice(1) : text
  const rows = []
  let row = []
  let field = ""
  let quoted = false
  for (let i = 0; i < source.length; i++) {
    const char = source[i]
    if (quoted) {
      if (char !== "\"") {
        field += char
      } else if (source[i + 1] === "\"") {
        field += "\""
        i++
      } else {
        quoted = false
      }
      continue
    }
    if (char === "\"") {
      quoted = true
    } else if (char === ",") {
      row.push(field)
      field = ""
    } else if (char === "\r" || char === "\n") {
      if (char === "\r" && source[i + 1] === "\n") {
        i++
      }
      row.push(field)
      rows.push(row)
      row = []
      field = ""
    } else {
      field += char
    }
  }
  if (field !== "" || row.length > 0) {
    row.push(field)
    rows.push(row)
  }

  return rows.filter((candidate) => candidate.some((cell) => cell.trim() !== ""))
}

/** The uploaded table as [key, text] pairs: the CSV header must name the `key` and `text`
 *  columns (extra ones such as `english` are reference material, ignored on import); a
 *  JSON file is the flat key→text map whose "@"-prefixed metadata keys the seeder skips. */
function entriesFromText(name, text) {
  if (name.toLowerCase().endsWith(".json")) {
    const parsed = JSON.parse(text)
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      throw new Error("shape")
    }

    return Object.entries(parsed)
      .filter(([key]) => !key.startsWith("@"))
      .map(([key, value]) => [key, typeof value === "string" ? value : ""])
  }
  const rows = parseCsv(text)
  if (rows.length === 0) {
    throw new Error("empty")
  }
  const header = rows[0].map((column) => column.trim().toLowerCase())
  const keyAt = header.indexOf("key")
  const textAt = header.indexOf("text")
  if (keyAt === -1 || textAt === -1) {
    throw new Error("header")
  }

  return rows.slice(1).map((row) => [String(row[keyAt] ?? ""), String(row[textAt] ?? "")])
}

/** Stage each row into the grid: known keys only, non-empty texts only (REQ-UI-049). */
function applyEntries(entries) {
  const counts = { applied: 0, unknown: 0, empty: 0 }
  for (const [rawKey, rawText] of entries) {
    const key = rawKey.trim()
    // The server trims on save — stage what would actually be stored.
    const text = rawText.replace(/\r/g, "").trim()
    // getRow reports a miss as falsy — which exact value depends on the Tabulator version.
    const row = key === "" ? null : table.getRow(key)
    if (!row) {
      counts.unknown++
    } else if (text === "") {
      counts.empty++
    } else if (String(row.getData().text ?? "") !== text) {
      row.update({ text })
      counts.applied++
    }
  }

  return counts
}

function setMessage(text, level) {
  const box = document.getElementById("tr-message")
  if (box !== null) {
    box.replaceChildren(el("div", { class: "alert " + level + " py-2 small mb-2" }, text))
  }
}

/** The import result line above the table; the save flash confirms what Save then stored. */
function showImportResult(counts) {
  const parts = []
  if (counts.applied > 0) {
    parts.push(t("admin.translations.import_applied", { count: String(counts.applied) }))
  }
  if (counts.unknown > 0) {
    parts.push(t("admin.translations.import_skipped", { count: String(counts.unknown) }))
  }
  if (counts.empty > 0) {
    parts.push(t("admin.translations.import_empty", { count: String(counts.empty) }))
  }
  setMessage(parts.length === 0 ? t("admin.translations.import_nothing") : parts.join(" "), counts.applied > 0 ? "alert-success" : "alert-warning")
}

async function importFile(file) {
  let entries
  try {
    entries = entriesFromText(file.name, await file.text())
  } catch (error) {
    setMessage(t("admin.translations.import_failed"), "alert-danger")

    return
  }
  showImportResult(applyEntries(entries))
  loadStatus()
}

function failedNotice() {
  const box = el("div", { class: "clara-region-empty py-3 d-flex flex-wrap gap-2 align-items-center" }, t("js.load_failed"))
  const retry = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" })
  retry.textContent = t("js.retry")
  retry.addEventListener("click", loadRows)
  box.appendChild(retry)
  container.replaceChildren(box)
}

async function loadRows() {
  const language = container.dataset.language || ""
  let payload
  try {
    payload = await fetchJson("/admin?section=translations&language=" + encodeURIComponent(language))
  } catch (error) {
    failedNotice()

    return
  }

  originalText.clear()
  for (const row of payload.rows || []) {
    originalText.set(row.key, String(row.text ?? ""))
  }

  table = new Tabulator(container, tabulatorOptions({
    data: payload.rows || [],
    columns: [
      { title: t("admin.translations.key"), field: "key", formatter: keyCell, sorter: "string", width: "32%" },
      { title: t("admin.translations.english"), field: "english", cssClass: "text-body-secondary", sorter: "string" },
      { title: t("admin.translations.text"), field: "text", editor: "input", sorter: "string", width: "34%", cellEdited: () => loadStatus() },
    ],
  }))

  const search = document.getElementById("tr-search")
  if (search !== null) {
    search.addEventListener("input", applyFilters)
  }
  const missingBox = document.getElementById("tr-missing")
  if (missingBox !== null) {
    missingBox.addEventListener("change", applyFilters)
  }

  table.on("tableBuilt", () => {
    built = true
    applyFilters()
    loadStatus()
    // Export and import operate on the bound data — live only once the region exists.
    for (const control of [document.getElementById("tr-export"), document.getElementById("tr-import-file")]) {
      if (control !== null) {
        control.disabled = false
      }
    }
  })
}

function bindTranslations(form) {
  // Save collects the edited cells into hidden inputs on the POST form — the mutation
  // stays a plain CSRF-protected PRG submit (§3.3), unchanged server-side.
  form.addEventListener("submit", () => {
    for (const stale of form.querySelectorAll("input[data-tr-change]")) {
      stale.remove()
    }
    if (table === null) {
      return
    }
    for (const row of table.getData()) {
      const value = String(row.text ?? "")
      if (value !== originalText.get(row.key)) {
        form.appendChild(el("input", { type: "hidden", name: "text[" + row.key + "]", value, dataset: { trChange: "1" } }))
      }
    }
    const missingBox = document.getElementById("tr-missing")
    if (missingBox !== null && missingBox.checked) {
      form.appendChild(el("input", { type: "hidden", name: "missing", value: "1", dataset: { trChange: "1" } }))
    }
  })

  const exportButton = document.getElementById("tr-export")
  if (exportButton !== null) {
    exportButton.addEventListener("click", exportCsv)
  }
  const importInput = document.getElementById("tr-import-file")
  if (importInput !== null) {
    importInput.addEventListener("change", () => {
      const file = importInput.files[0]
      if (file !== undefined) {
        importFile(file)
      }
      // Clearing lets re-picking the same file fire change again.
      importInput.value = ""
    })
  }

  loadRows()
}

ready(() => {
  // No region on the page (no editable language configured) means nothing to bind.
  const form = document.getElementById("tr-form")
  if (container !== null && form !== null) {
    bindTranslations(form)
  }
})
