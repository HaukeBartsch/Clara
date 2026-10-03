// The Record Status Dashboard's behaviour (User_Interface_Design.md §6.3): bind the rows of
// the `records` data region — served by the page's own route on Accept (REQ-UI-044) — into
// the table body of every arm. The server rendered the column structure; each header carries
// the `<event>|<instrument>` key of the cells under it, so this module only places states and
// never decides which columns exist. One module for one surface (REQ-UI-045); every label
// comes from the injected data-i18n block (REQ-UI-008), and no field value is ever in the
// payload (REQ-API-074).

import { el, fetchJson, fill, notice, ready, t, td } from "../app.js"

const STATES = ["no_data", "some_data", "finished"]

const columnsOf = (table) =>
  Array.from(table.querySelectorAll("th[data-col]")).map((th) => ({
    key: th.dataset.col,
    event: th.dataset.event,
    instrument: th.dataset.instrument,
    eventLabel: th.dataset.eventLabel || th.dataset.event,
  }))

const recordLink = (base, record, column) =>
  base + encodeURIComponent(record) + (column ? "?event=" + encodeURIComponent(column.event) + "&instrument=" + encodeURIComponent(column.instrument) : "")

function cell(base, record, column, state) {
  if (state === undefined) {
    // The pair is not part of this record's status (an arm the record has no data in, or a
    // pair the API did not report): an empty cell rather than a claim about it.
    return td("", "text-center")
  }
  const safe = STATES.includes(state) ? state : "no_data"
  const label = t("records.open_cell", {
    record,
    instrument: column.instrument,
    event: column.eventLabel,
    state: t("records.state." + safe),
  })
  const link = el("a", { href: recordLink(base, record, column), class: "clara-state-link", title: label, "aria-label": label }, [
    el("span", { class: "clara-state clara-state-" + safe }),
  ])
  return el("td", { class: "text-center", dataset: { state: safe } }, link)
}

function row(base, columns, record) {
  const name = el("th", { scope: "row", class: "fw-normal text-nowrap" }, el("a", { href: recordLink(base, record.record_id) }, record.record_id))
  const cells = record.cells || {}
  return el("tr", { dataset: { record: record.record_id } }, [name, ...columns.map((column) => cell(base, record.record_id, column, cells[column.key]))])
}

function failed(body, span, retry) {
  const line = notice(span, t("js.load_failed"))
  const button = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary ms-2" }, t("js.retry"))
  button.addEventListener("click", retry)
  line.querySelector("td").appendChild(button)
  fill(body, [line])
}

async function load() {
  const bodies = Array.from(document.querySelectorAll("tbody[data-region='records']"))
  if (bodies.length === 0) return

  let records
  try {
    // One fetch serves every arm's table: the region is the same rows for all of them.
    records = (await fetchJson(bodies[0].dataset.source)).records || []
  } catch (error) {
    for (const body of bodies) failed(body, Number(body.dataset.columns), load)
    return
  }

  for (const body of bodies) {
    const columns = columnsOf(body.closest("table"))
    const base = body.dataset.recordBase
    fill(body, records.length ? records.map((record) => row(base, columns, record)) : [notice(Number(body.dataset.columns), t("records.empty"))])
  }
}

ready(load)
