// The Translations section's key table (§5.7, REQ-UI-030): a Tabulator-backed data region
// bound from this route's JSON (REQ-UI-044) — client-side key filter and missing-only
// toggle over the full universe, inline editing of the translation column, and a Save that
// collects edited cells into hidden inputs on the POST form. The save stays a CSRF-protected
// PRG mutation (§3.3); the server still diffs against the stored strings and sends only what
// changed (REQ-API-100). No UI string appears here — every label comes from the injected
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

  loadRows()
}

ready(() => {
  // No region on the page (no editable language configured) means nothing to bind.
  const form = document.getElementById("tr-form")
  if (container !== null && form !== null) {
    bindTranslations(form)
  }
})
