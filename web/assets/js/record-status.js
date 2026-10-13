// The Record Status Dashboard's behaviour (User_Interface_Design.md §6.3): bind the rows of
// the `records` data region — served by the page's own route on Accept (REQ-UI-044) — into
// the table body of every arm. The server rendered the column structure; each header carries
// the `<event>|<instrument>` key of the cells under it, so this module only places states and
// never decides which columns exist. A repeating instrument's column (REQ-UI-055) shows one
// dot per instance — the first MAX_DOTS of them, then a link to the record view for the rest —
// and "+" opening the next instance where the header offers it. One module for one surface
// (REQ-UI-045); every label comes from the injected data-i18n block (REQ-UI-008), and no
// field value is ever in the payload (REQ-API-074).

import { el, fetchJson, fill, notice, ready, t, td } from "../app.js"

const STATES = ["no_data", "some_data", "finished"]

// A cell lists at most this many instance dots; the rest are one link away (REQ-UI-055).
const MAX_DOTS = 5

const columnsOf = (table) =>
  Array.from(table.querySelectorAll("th[data-col]")).map((th) => ({
    key: th.dataset.col,
    event: th.dataset.event,
    instrument: th.dataset.instrument,
    eventLabel: th.dataset.eventLabel || th.dataset.event,
    repeating: th.dataset.repeating === "1",
    add: th.dataset.add === "1",
  }))

const recordLink = (base, record, column, instance) =>
  base + encodeURIComponent(record) +
  (column ? "?event=" + encodeURIComponent(column.event) + "&instrument=" + encodeURIComponent(column.instrument) : "") +
  (column && instance ? "&instance=" + instance : "")

const stateDot = (href, label, safe) =>
  el("a", { href, class: "clara-state-link", title: label, "aria-label": label }, [
    el("span", { class: "clara-state clara-state-" + safe }),
  ])

function cell(base, record, column, state) {
  if (state === undefined) {
    // The event was not reported for this record (an arm the API did not disclose): an
    // empty cell rather than a claim about it.
    return td("", "text-center")
  }
  const safe = STATES.includes(state) ? state : "no_data"
  const label = t("records.open_cell", {
    record,
    instrument: column.instrument,
    event: column.eventLabel,
    state: t("records.state." + safe),
  })
  return el("td", { class: "text-center", dataset: { state: safe } }, stateDot(recordLink(base, record, column), label, safe))
}

// A repeating instrument's cell: a dot per instance that holds something — or one grey dot
// for the first when none does — then "+" for the instance after the highest (REQ-UI-055).
function instancesCell(base, record, column, state, instances) {
  if (state === undefined) return td("", "text-center")
  const listed = (instances || []).filter(([n]) => Number.isInteger(n) && n > 0)
  const shown = listed.length ? listed : [[1, "no_data"]]
  const dots = shown.slice(0, MAX_DOTS).map(([n, instanceState]) => {
    const safe = STATES.includes(instanceState) ? instanceState : "no_data"
    const label = t("records.open_instance", {
      record,
      instrument: column.instrument,
      event: column.eventLabel,
      instance: String(n),
      state: t("records.state." + safe),
    })
    return stateDot(recordLink(base, record, column, n), label, safe)
  })
  const parts = [...dots]
  if (shown.length > MAX_DOTS) {
    const more = t("records.more_instances", { count: String(shown.length - MAX_DOTS) })
    parts.push(el("a", { href: recordLink(base, record, column), class: "clara-instance-more", title: more, "aria-label": more }, "+" + (shown.length - MAX_DOTS)))
  }
  if (column.add) {
    const next = listed.reduce((top, [n]) => Math.max(top, n), 0) + 1
    const label = t("records.add_instance", { record, instrument: column.instrument, event: column.eventLabel, instance: String(next) })
    parts.push(el("a", { href: recordLink(base, record, column, next), class: "clara-instance-add", title: label, "aria-label": label, dataset: { instance: String(next) } }, "+"))
  }
  const safe = STATES.includes(state) ? state : "no_data"
  return el("td", { class: "text-center", dataset: { state: safe } }, el("span", { class: "clara-instances" }, parts))
}

// The region is sparse (REQ-API-149): a reported event lists only the cells that hold
// something, so a column of a reported event without a cell is no data.
const stateOf = (cells, reported, column) =>
  cells[column.key] ?? (reported.has(column.event) ? "no_data" : undefined)

function row(base, columns, record) {
  const name = el("th", { scope: "row", class: "fw-normal text-nowrap" }, el("a", { href: recordLink(base, record.record_id) }, record.record_id))
  const cells = record.cells || {}
  const instances = record.instances || {}
  const reported = new Set(record.events || [])
  return el("tr", { dataset: { record: record.record_id } }, [name, ...columns.map((column) => column.repeating
    ? instancesCell(base, record.record_id, column, stateOf(cells, reported, column), instances[column.key])
    : cell(base, record.record_id, column, stateOf(cells, reported, column)))])
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
