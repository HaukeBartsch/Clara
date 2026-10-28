// The Record Status Dashboard's behaviour (User_Interface_Design.md §6.3): bind the rows of
// the `records` data region — served by the page's own route on Accept (REQ-UI-044) — into a
// Tabulator grid per arm (AGENTS.md "use Tabulator"). The grid renders virtually in both
// directions, so only the rows and columns in view are in the DOM however many participants
// and (event, instrument) pairs a project holds. The server rendered each arm's column
// structure as a table head whose headers carry the `<event>|<instrument>` key of the cells
// under them; this module reads that head and builds the grid in the table's place, so it only
// places states and never decides which columns exist. A repeating instrument's column
// (REQ-UI-055) shows one dot per instance — the first MAX_DOTS of them, then a link to the
// record view for the rest — and "+" opening the next instance where the header offers it. One
// module for one surface (REQ-UI-045); every label comes from the injected data-i18n block
// (REQ-UI-008), all content is built as DOM nodes — Tabulator writes a string title or cell
// value as HTML, so none is handed to it (REQ-UI-004) — and no field value is ever in the
// payload (REQ-API-074).

import { el, fetchJson, fill, notice, ready, t, tabulatorOptions } from "../app.js"
import { TabulatorFull as Tabulator } from "../vendor/tabulator/tabulator_esm.min.js"

const STATES = ["no_data", "some_data", "finished"]

// A cell lists at most this many instance dots; the rest are one link away (REQ-UI-055).
const MAX_DOTS = 5

const safeState = (state) => (STATES.includes(state) ? state : "no_data")

const columnsOf = (table) =>
  Array.from(table.querySelectorAll("th[data-col]")).map((th) => ({
    key: th.dataset.col,
    event: th.dataset.event,
    instrument: th.dataset.instrument,
    eventLabel: th.dataset.eventLabel || th.dataset.event,
    repeating: th.dataset.repeating === "1",
    add: th.dataset.add === "1",
    header: th,
  }))

const recordLink = (base, record, column, instance) =>
  base + encodeURIComponent(record) +
  (column ? "?event=" + encodeURIComponent(column.event) + "&instrument=" + encodeURIComponent(column.instrument) : "") +
  (column && instance ? "&instance=" + instance : "")

const stateDot = (href, label, safe) =>
  el("a", { href, class: "clara-state-link", title: label, "aria-label": label }, [
    el("span", { class: "clara-state clara-state-" + safe }),
  ])

function stateCell(base, record, column, state) {
  const safe = safeState(state)
  const label = t("records.open_cell", {
    record,
    instrument: column.instrument,
    event: column.eventLabel,
    state: t("records.state." + safe),
  })
  return stateDot(recordLink(base, record, column), label, safe)
}

// A repeating instrument's cell: a dot per instance that holds something — or one grey dot
// for the first when none does — then "+" for the instance after the highest (REQ-UI-055).
function instancesCell(base, record, column, instances) {
  const listed = (instances || []).filter(([n]) => Number.isInteger(n) && n > 0)
  const shown = listed.length ? listed : [[1, "no_data"]]
  const dots = shown.slice(0, MAX_DOTS).map(([n, instanceState]) => {
    const safe = safeState(instanceState)
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
  return el("span", { class: "clara-instances" }, parts)
}

// The region is sparse (REQ-API-149): a reported event lists only the cells that hold
// something, so a column of a reported event without a cell is no data, and a column of an
// event the API did not report for the record (an arm it did not disclose) has no state.
const stateOf = (row, column) =>
  row.cells[column.key] ?? (row.reported.has(column.event) ? "no_data" : undefined)

// One grid row per record, shared by every arm's grid; `reported` is built once here rather
// than once per rendered cell.
const rowOf = (record) => ({
  record_id: String(record.record_id),
  cells: record.cells || {},
  instances: record.instances || {},
  reported: new Set(record.events || []),
})

// A column's header is the server-rendered one, copied: its text, its repeating marker, its
// title and its data attributes (REQ-UI-055) — built as nodes, never as an HTML string.
const headerOf = (column) => () =>
  el("span", { class: "clara-status-col", title: column.header.title || null, dataset: { ...column.header.dataset } },
    Array.from(column.header.childNodes, (node) => node.cloneNode(true)))

const stateColumn = (base, column) => ({
  title: column.instrument,
  field: column.key,
  titleFormatter: headerOf(column),
  headerSort: false,
  hozAlign: "center",
  formatter: (cell) => {
    const row = cell.getData()
    const state = stateOf(row, column)
    const element = cell.getElement()
    if (state === undefined) {
      delete element.dataset.state
      return ""
    }
    element.dataset.state = safeState(state)
    return column.repeating
      ? instancesCell(base, row.record_id, column, row.instances[column.key])
      : stateCell(base, row.record_id, column, state)
  },
})

// The arm's events as column groups, each holding its instruments in the server's order.
function eventGroups(base, columns) {
  const groups = []
  for (const column of columns) {
    const last = groups[groups.length - 1]
    if (last && last.event === column.event) {
      last.columns.push(stateColumn(base, column))
      continue
    }
    groups.push({
      event: column.event,
      title: column.eventLabel,
      titleFormatter: () => el("span", { title: column.event }, column.eventLabel),
      columns: [stateColumn(base, column)],
    })
  }
  return groups.map(({ event, ...group }) => group)
}

function build(region, rows) {
  const skeleton = region.querySelector("table")
  const columns = columnsOf(skeleton)
  const base = region.dataset.recordBase
  const recordHeading = skeleton.tHead.rows[0].cells[0].textContent
  // The vendored Bootstrap 5 theme condenses and borders the grid from the table classes.
  const holder = el("div", { class: "clara-record-status-grid table-sm table-bordered" })
  region.replaceChildren(holder)

  return new Tabulator(holder, tabulatorOptions({
    data: rows.slice(),
    index: "record_id",
    // Grows with the rows up to the viewport, then scrolls inside: the grid only keeps the rows
    // in view (plus a buffer) in the DOM, and the horizontal renderer does the same for columns.
    height: false,
    maxHeight: "75vh",
    layout: "fitData",
    renderHorizontal: "virtual",
    // Column keys are `<event>|<instrument>`, never paths into the row.
    nestedFieldSeparator: false,
    columnHeaderVertAlign: "bottom",
    columnDefaults: { resizable: false, vertAlign: "middle" },
    rowFormatter: (row) => {
      row.getElement().dataset.record = row.getData().record_id
    },
    columns: [
      {
        title: recordHeading,
        field: "record_id",
        titleFormatter: () => document.createTextNode(recordHeading),
        frozen: true,
        sorter: "alphanum",
        formatter: (cell) => el("a", { href: recordLink(base, cell.getValue()) }, cell.getValue()),
      },
      ...eventGroups(base, columns),
    ],
  }))
}

function failed(region, retry) {
  const line = notice(Number(region.dataset.columns), t("js.load_failed"))
  const button = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary ms-2" }, t("js.retry"))
  button.addEventListener("click", retry)
  line.querySelector("td").appendChild(button)
  fill(region.querySelector("tbody"), [line])
}

// Grids waiting for their arm's tab: one laid out in a hidden pane has no size to render
// against, so it is built the first time the pane is shown.
const pending = new Map()

function showPane(event) {
  const target = event.target.dataset.bsTarget
  const region = target ? document.querySelector(target + " [data-region='records']") : null
  if (region && pending.has(region)) {
    build(region, pending.get(region))
    pending.delete(region)
  }
}

async function load() {
  const regions = Array.from(document.querySelectorAll("[data-region='records']"))
  if (regions.length === 0) return

  let records
  try {
    // One fetch serves every arm's grid: the region is the same rows for all of them.
    records = (await fetchJson(regions[0].dataset.source)).records || []
  } catch (error) {
    for (const region of regions) failed(region, load)
    return
  }

  const rows = records.map(rowOf)
  for (const region of regions) {
    if (rows.length === 0) {
      fill(region.querySelector("tbody"), [notice(Number(region.dataset.columns), t("records.empty"))])
    } else if (region.getClientRects().length > 0) {
      build(region, rows)
    } else {
      pending.set(region, rows)
    }
  }
}

ready(() => {
  document.addEventListener("shown.bs.tab", showPane)
  load()
})
