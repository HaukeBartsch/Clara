// The Regions section's mapping grid (§5.13, REQ-UI-078): the provider role values a sign-in
// recorded or an administrator entered, each with the region it maps to. A Tabulator-backed
// data region bound from this route's JSON (REQ-UI-044) — a provider can release hundreds of
// values (AGENTS.md "use Tabulator"). Each row's region select and Remove are ordinary
// CSRF-protected POST forms (§3.3): changing the select submits its form, so no Save button
// is needed, and Remove asks first through admin.js's confirmation. No UI string appears
// here — every label comes from the injected data-i18n block (REQ-UI-008) — and role values
// are provider text, written with textContent only (REQ-UI-004).

import { csrfToken, el, fetchJson, ready, t, tabulatorOptions } from "../app.js"
import { TabulatorFull as Tabulator } from "../vendor/tabulator/tabulator_esm.min.js"

const DAY = 86400000

const container = document.querySelector("[data-region='regions']")

/** The region select's options, code => display text in REQ-DB-006 order, as PHP rendered them. */
function regionOptions() {
  try {
    const options = JSON.parse(container.dataset.claraRegions || "{}")
    return options && typeof options === "object" ? options : {}
  } catch (error) {
    return {}
  }
}

/** A stored UTC stamp as "today", "yesterday" or a date — like the overview's "last used" (§4). */
function seenText(stamp) {
  if (!stamp) {
    return t("admin.regions.never")
  }
  const when = new Date(String(stamp).replace(" ", "T") + "Z")
  if (Number.isNaN(when.getTime())) {
    return String(stamp)
  }
  const dayStart = (date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
  const days = Math.round((dayStart(new Date()) - dayStart(when)) / DAY)
  if (days === 0) {
    return t("dashboard.project.last_used.today")
  }
  if (days === 1) {
    return t("dashboard.project.last_used.yesterday")
  }
  const options = { day: "numeric", month: "short" }
  if (when.getFullYear() !== new Date().getFullYear()) {
    options.year = "numeric"
  }

  return new Intl.DateTimeFormat(navigator.language || undefined, options).format(when)
}

/** A POST form to one of this section's actions, carrying the CSRF token and the row id. */
function actionForm(action, id, children, attributes) {
  return el("form", Object.assign({
    method: "post",
    action: "/admin?section=regions&action=" + action,
    class: "d-flex gap-1 align-items-center m-0",
  }, attributes || {}), [
    el("input", { type: "hidden", name: "csrf_token", value: csrfToken() }),
    el("input", { type: "hidden", name: "id", value: String(id) }),
  ].concat(children))
}

/** Provider cell: the login display name, the issuer in the tooltip. */
function providerCell(cell) {
  const row = cell.getData()

  return el("span", { title: row.issuer }, String(row.provider))
}

/** Role value cell: the value as text, with the "new" badge while no administrator decided on it. */
function valueCell(cell) {
  const row = cell.getData()
  const wrap = el("span", null, [el("code", null, String(row.role_value))])
  if (!row.reviewed) {
    wrap.appendChild(el("span", { class: "badge text-bg-warning ms-1" }, t("admin.regions.new")))
  }

  return wrap
}

/**
 * Region cell: a select that submits its form on change (§5.13). An undecided row shows a
 * disabled "choose…" placeholder; a stored code the enumeration no longer offers stays listed.
 */
function regionCell(cell) {
  const row = cell.getData()
  const options = regionOptions()
  const current = row.reviewed ? (row.region || "") : null
  const select = el("select", {
    class: "form-select form-select-sm",
    name: "region",
    "aria-label": t("admin.regions.region") + ": " + row.role_value,
  })
  if (current === null) {
    select.appendChild(el("option", { value: "", disabled: "", selected: "" }, t("admin.regions.choose")))
  }
  const choices = [["", t("admin.regions.no_region")]].concat(Object.entries(options))
  if (current && !(current in options)) {
    choices.push([current, current])
  }
  for (const [code, text] of choices) {
    const option = el("option", { value: code }, text)
    // A disabled placeholder carries value "" too; only a decided row selects the real option.
    if (current !== null && code === current) {
      option.selected = true
    }
    select.appendChild(option)
  }
  const form = actionForm("map_role", row.id, [select])
  select.addEventListener("change", () => form.requestSubmit())

  return form
}

/** Remove cell: a confirmed POST (§3.5); the value returns as new when a sign-in carries it again. */
function removeCell(cell) {
  const row = cell.getData()
  const button = el("button", { type: "submit", class: "btn btn-sm btn-outline-danger" }, t("admin.regions.remove"))
  button.setAttribute("aria-label", t("admin.regions.remove") + ": " + row.role_value)

  return actionForm("remove_role", row.id, [button], {
    "data-clara-confirm": t("admin.regions.remove_confirm", { value: String(row.role_value) }),
  })
}

function message(text, withRetry) {
  const box = el("div", { class: "clara-region-empty py-3 d-flex flex-wrap gap-2 align-items-center" }, text)
  if (withRetry) {
    const retry = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" }, t("js.retry"))
    retry.addEventListener("click", loadRows)
    box.appendChild(retry)
  }
  container.replaceChildren(box)
}

async function loadRows() {
  let payload
  try {
    payload = await fetchJson("/admin?section=regions")
  } catch (error) {
    message(t("js.load_failed"), true)
    return
  }
  const rows = Array.isArray(payload.rows) ? payload.rows : []
  if (!rows.length) {
    message(t("admin.regions.empty"), false)
    return
  }

  container.replaceChildren()
  // Rows keep the API's order — undecided values first, then by provider and value (REQ-API-177).
  new Tabulator(container, tabulatorOptions({
    data: rows,
    index: "id",
    columns: [
      { title: t("admin.regions.provider"), field: "provider", formatter: providerCell, width: 150 },
      {
        title: t("admin.regions.value"), field: "role_value", formatter: valueCell, minWidth: 180,
        headerFilter: "input", headerFilterPlaceholder: t("admin.regions.filter"),
      },
      { title: t("admin.regions.seen"), field: "seen_count", hozAlign: "right", width: 90 },
      { title: t("admin.regions.first_seen"), field: "first_seen_at", formatter: (cell) => seenText(cell.getValue()), width: 120 },
      { title: t("admin.regions.last_seen"), field: "last_seen_at", formatter: (cell) => seenText(cell.getValue()), width: 120 },
      { title: t("admin.regions.region"), field: "region", formatter: regionCell, headerSort: false, minWidth: 200 },
      { title: "", field: "id", formatter: removeCell, headerSort: false, width: 110 },
    ],
  }))
}

ready(() => {
  if (container) {
    loadRows()
  }
})
