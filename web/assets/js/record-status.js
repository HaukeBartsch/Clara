// The Record Status Dashboard's behaviour (User_Interface_Design.md §6.3): bind the rows of
// the `records` data region — served by the page's own route on Accept (REQ-UI-044) — into a
// Tabulator grid per arm (AGENTS.md "use Tabulator"). The grid renders virtually in both
// directions, so only the rows and columns in view are in the DOM however many participants
// and (event, instrument) pairs a project holds. The server rendered each arm's column
// structure as a table head whose headers carry the `<event>|<instrument>` key of the cells
// under them; this module reads that head and builds the grid in the table's place, so it only
// places states and never decides which columns exist. The rows stream in (REQ-UI-074): the
// grid shows the first of them while a large project is still being read, and counts the rest
// in. Each indicator is the master spec's 3×3 counter in the state's colouring, its "on" tiles
// showing how many of the instrument's fields hold a value (REQ-UI-081); those counts are a
// second read of the same region, started beside the rows' and filled into the drawn rows as
// they arrive. A repeating instrument's column
// (REQ-UI-055) shows one indicator per instance — the first MAX_DOTS of them, then a link to the
// record view for the rest — and "+" opening the next instance where the header offers it. One
// module for one surface (REQ-UI-045); every label comes from the injected data-i18n block
// (REQ-UI-008), all content is built as DOM nodes — Tabulator writes a string title or cell
// value as HTML, so none is handed to it (REQ-UI-004) — and no field value is ever in the
// payload (REQ-API-074).

import { el, fill, notice, ready, t, tabulatorOptions } from "../app.js"
import { TabulatorFull as Tabulator } from "../vendor/tabulator/tabulator_esm.min.js"

const STATES = ["no_data", "some_data", "finished"]

// A cell lists at most this many instance dots; the rest are one link away (REQ-UI-055).
const MAX_DOTS = 5

const safeState = (state) => (STATES.includes(state) ? state : "no_data")

// A counter's tiles (REQ-UI-081).
const TILES = 9

// How many of the nine tiles a [filled, visible] count turns on (master spec "Visual
// appearence"): none while no counted field holds a value, all nine only once every one does,
// and between the two the share rounded — but never none and never all, however close a
// partial count comes. In integers, so the same two numbers always turn on the same tiles.
const litTiles = ([filled, visible]) =>
  filled <= 0 || visible <= 0 ? 0
    : filled >= visible ? TILES
    : Math.min(TILES - 1, Math.max(1, Math.floor((2 * TILES * filled + visible) / (2 * visible))))

// A state's colouring of the counter: grey is the counter's own, amber and green a class each.
const COLORING = { no_data: [], some_data: ["counter-yellow"], finished: ["counter-green"] }

// The spec's counting classes: `counter-<n>` turns on n + 1 tiles, and no class none.
const countClass = (lit) => (lit > 0 ? ["counter-" + (lit - 1)] : [])

// The indicator: a marker cloned from the page's template (the view's partials/counter.php) —
// one `<use>` of the page's one counter SVG — given the state's colouring and the counter class
// for as many tiles on as its count says; markers differ only in their classes. Without a count
// yet (null) none is on and `data-lit` is absent.
const counterIcon = (safe, count) => {
  const svg = document.getElementById("clara-counter-marker").content.firstElementChild.cloneNode(true)
  const lit = count === null ? 0 : litTiles(count)
  svg.classList.add(...COLORING[safe], ...countClass(lit))
  if (count !== null) svg.dataset.lit = String(lit)
  return svg
}

// The state as an indicator's label names it, with the count once it is known — so neither is
// told by colour or dots alone (REQ-UI-081).
const stateLabel = (safe, count) =>
  t("records.state." + safe) + (count === null ? "" : ", " + (count[0] > 0
    ? t("records.count", { filled: String(count[0]), visible: String(count[1]) })
    : t("records.count_none")))

// The counts of the current load by record (REQ-API-171), kept beside the grids' rows: either
// may arrive first.
let counts = new Map()

// A record's count as the `?counts=1` region relays it: cells as [filled, visible] by key, and
// a repeating cell's instances by number.
const countsOf = (record) => ({
  cells: record.counts || {},
  instances: Object.fromEntries(Object.entries(record.instances || {}).map(([key, list]) =>
    [key, new Map((Array.isArray(list) ? list : []).map(([n, filled, visible]) => [n, [filled, visible]]))])),
})

// A cell's count: [filled, visible] as the read lists it; [0, 0] once the record's count has
// arrived without the cell — none of its counted fields holds a value; null until then.
const countOf = (record, key, instance) => {
  const known = counts.get(record)
  if (known === undefined) return null
  const listed = instance > 1 ? known.instances[key]?.get(instance) : known.cells[key]
  return listed || [0, 0]
}

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

const stateDot = (href, label, safe, count) =>
  el("a", { href, class: "clara-state-link", title: label, "aria-label": label }, [counterIcon(safe, count)])

function stateCell(base, record, column, state) {
  const safe = safeState(state)
  const count = countOf(record, column.key, 1)
  const label = t("records.open_cell", {
    record,
    instrument: column.instrument,
    event: column.eventLabel,
    state: stateLabel(safe, count),
  })
  return stateDot(recordLink(base, record, column), label, safe, count)
}

// A repeating instrument's cell: an indicator per instance that holds something — or one grey
// one for the first when none does — then "+" for the instance after the highest (REQ-UI-055).
function instancesCell(base, record, column, instances) {
  const listed = (instances || []).filter(([n]) => Number.isInteger(n) && n > 0)
  const shown = listed.length ? listed : [[1, "no_data"]]
  const dots = shown.slice(0, MAX_DOTS).map(([n, instanceState]) => {
    const safe = safeState(instanceState)
    const count = countOf(record, column.key, n)
    const label = t("records.open_instance", {
      record,
      instrument: column.instrument,
      event: column.eventLabel,
      instance: String(n),
      state: stateLabel(safe, count),
    })
    return stateDot(recordLink(base, record, column, n), label, safe, count)
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

// The streamed load (REQ-UI-074): the region as newline-delimited JSON, or — from a server that
// does not stream it — as the one JSON body every region has.
const ACCEPT = "application/x-ndjson, application/json;q=0.9"

// Later rows join a grid at most this often: each addData walks the grid's every row, so a
// call per arriving chunk would grow costly as a large project fills in.
const APPEND_EVERY_MS = 250

// onRow hears of every row the grid draws, each time it draws it.
function build(region, rows, sortable, onRow) {
  const skeleton = region.querySelector("table")
  const columns = columnsOf(skeleton)
  const base = region.dataset.recordBase
  const recordHeading = skeleton.tHead.rows[0].cells[0].textContent
  // The vendored Bootstrap 5 theme condenses and borders the grid from the table classes.
  const holder = el("div", { class: "clara-record-status-grid table-sm table-bordered" })
  region.replaceChildren(holder)

  return new Tabulator(holder, tabulatorOptions({
    data: rows,
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
      onRow(row)
    },
    columns: [
      {
        title: recordHeading,
        field: "record_id",
        titleFormatter: () => document.createTextNode(recordHeading),
        frozen: true,
        sorter: "alphanum",
        // Off while rows are still arriving: they are appended, and would land out of order
        // under a sorted column.
        headerSort: sortable,
        formatter: (cell) => el("a", { href: recordLink(base, cell.getValue()) }, cell.getValue()),
      },
      ...eventGroups(base, columns),
    ],
  }))
}

function failed(region, retry) {
  const line = notice(Number(region.dataset.columns), t("js.load_failed"))
  line.querySelector("td").appendChild(retryButton(retry))
  fill(region.querySelector("tbody"), [line])
}

const retryButton = (retry) => {
  const button = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary ms-2" }, t("js.retry"))
  button.addEventListener("click", retry)
  return button
}

// Reads a region — the rows, or their counts — and hands its records to onRows batch by batch
// as they arrive, each shaped by `shape`. Resolves to whether the load is complete: the end
// line arrived and names as many records as came. A stream that breaks off or carries a line
// that does not parse is incomplete; a refusal before the body rejects, like any region read
// (§3.4).
async function readRegion(source, shape, onRows) {
  const response = await fetch(source, { headers: { Accept: ACCEPT }, credentials: "same-origin" })
  if (!response.ok) {
    throw new Error(response.status === 401 || response.status === 403 ? "forbidden" : "internal")
  }
  if (!(response.headers.get("Content-Type") || "").startsWith("application/x-ndjson")) {
    onRows(((await response.json()).records || []).map(shape))
    return true
  }

  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
  let tail = ""
  let received = 0
  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) return false // closed before its end line
      // A chunk may end inside a line; that tail waits for the next chunk.
      const lines = (tail + value).split("\n")
      tail = lines.pop()
      const items = lines.filter((line) => line.trim() !== "").map((line) => JSON.parse(line))
      const batch = items.filter((item) => item.end !== true).map(shape)
      received += batch.length
      if (batch.length > 0) onRows(batch)
      const end = items.find((item) => item.end === true)
      if (end) return end.records === received
    }
  } catch (error) {
    return false
  }
}

// One arm's grid while the rows arrive (§6.3 Progressive load). It is built from every row
// received so far once its pane is visible — a grid laid out in a hidden pane has no size to
// render against — and then takes the rows it has not seen yet, at most every APPEND_EVERY_MS
// and in an animation frame. Counts that arrive meanwhile (REQ-UI-081) are drawn on the same
// cadence into the rows it has already drawn; a row drawn later reads its count then. A line
// under it counts the records loaded until the load ends: then sorting comes on, or the line
// says the list is incomplete and offers the retry.
function streamedGrid(region, rows, retry) {
  let table = null
  let built = false
  let taken = 0
  const drawn = new Map() // record_id → the RowComponent of each row the grid has drawn
  const recount = new Set() // records whose counts arrived since the last flush
  let scheduled = false
  let last = 0
  let outcome = null // null while loading, then "complete" or "failed"
  let settled = false // the outcome is shown on the grid
  const progress = el("div", { class: "small text-body-secondary mt-1", role: "status" })

  const settle = () => {
    settled = true
    if (outcome === "complete") {
      progress.remove()
      table.updateColumnDefinition("record_id", { headerSort: true })
      return
    }
    // The rows shown stay, under a line saying the list is incomplete (REQ-UI-074).
    progress.className = "small text-danger mt-1"
    progress.replaceChildren(t("records.load_incomplete", { count: String(taken) }), retryButton(retry))
  }

  // A drawn row whose count arrived is formatted afresh: at once where it is in view, on its
  // next draw where it is not (Tabulator's reformat).
  const recounted = () => {
    for (const record of recount) drawn.get(record)?.reformat()
    recount.clear()
  }

  const flush = () => {
    scheduled = false
    last = performance.now()
    if (table === null) {
      if (outcome === "failed" || rows.length === 0 || region.getClientRects().length === 0) return
      // A grid built after the end line is sorted from the start and needs no progress line.
      settled = outcome === "complete"
      table = build(region, rows.slice(), settled, (row) => drawn.set(row.getData().record_id, row))
      taken = rows.length
      if (!settled) region.appendChild(progress)
      table.on("tableBuilt", () => {
        built = true
        flush()
      })
    } else if (built && taken < rows.length) {
      table.addData(rows.slice(taken))
      taken = rows.length
    }
    if (!built) return
    recounted()
    if (settled) return
    if (outcome === null) progress.textContent = t("records.loading_count", { count: String(taken) })
    else settle()
  }

  const schedule = () => {
    if (scheduled) return
    scheduled = true
    setTimeout(() => requestAnimationFrame(flush), Math.max(0, last + APPEND_EVERY_MS - performance.now()))
  }

  return {
    added: schedule,
    shown: schedule,
    counted(records) {
      for (const record of records) recount.add(record)
      schedule()
    },
    finish(complete) {
      outcome = complete ? "complete" : "failed"
      // How the load ended, on the region itself: what a test or a style can wait for.
      region.dataset.loaded = outcome
      if (complete && rows.length === 0) {
        fill(region.querySelector("tbody"), [notice(Number(region.dataset.columns), t("records.empty"))])
      } else if (table === null) {
        // Not built yet: a complete grid is built sortable when its pane shows; a failed load
        // shows the failure line in place of the rows.
        if (!complete) failed(region, retry)
      } else {
        flush()
      }
    },
    // A retry replaces the grid; a flush still scheduled for it then finds nothing to do.
    destroy() {
      if (table !== null) table.destroy()
      table = null
      outcome = "failed"
    },
  }
}

// Each arm's column structure and loading state as the server rendered them, kept so a retry
// starts again from them after a grid has taken the table's place.
const skeletons = new Map()

// The current load's grid of each arm, by region.
let grids = new Map()

function showPane(event) {
  const target = event.target.dataset.bsTarget
  const region = target ? document.querySelector(target + " [data-region='records']") : null
  if (region && grids.has(region)) grids.get(region).shown()
}

async function load() {
  const regions = Array.from(document.querySelectorAll("[data-region='records']"))
  if (regions.length === 0) return

  for (const grid of grids.values()) grid.destroy()
  for (const region of regions) {
    if (!skeletons.has(region)) skeletons.set(region, region.firstElementChild.cloneNode(true))
    region.replaceChildren(skeletons.get(region).cloneNode(true))
    delete region.dataset.loaded
    delete region.dataset.counted
  }

  // One read serves every arm's grid: the region is the same rows for all of them. The count's
  // read starts with the first rows, so it never holds them back.
  const rows = []
  const known = new Map()
  counts = known
  grids = new Map(regions.map((region) => [region, streamedGrid(region, rows, load)]))
  const current = grids
  let complete
  try {
    complete = await readRegion(regions[0].dataset.source, rowOf, (batch) => {
      if (rows.length === 0) loadCounts(regions, known, current)
      for (const row of batch) rows.push(row)
      for (const grid of grids.values()) grid.added()
    })
  } catch (error) {
    for (const region of regions) {
      region.dataset.loaded = "failed"
      failed(region, load)
    }
    return
  }
  for (const grid of grids.values()) grid.finish(complete)
}

// The completeness count (§6.3 The count's read, REQ-UI-081), started once the first rows have
// arrived — their request has released the session by then, so this one cannot hold the grid
// back — and handed to every arm's grid as it arrives. A read that fails or breaks off says so
// under the legend — the dots then show the state only — and the rows' retry reloads both.
// `known` and `current` are this load's, so a read a retry has overtaken changes nothing on the
// page.
async function loadCounts(regions, known, current) {
  const note = document.querySelector(".clara-counts-failed")
  if (note) note.hidden = true
  let complete
  try {
    complete = await readRegion(regions[0].dataset.countSource, (record) => [String(record.record_id), countsOf(record)], (batch) => {
      for (const [record, count] of batch) known.set(record, count)
      const records = batch.map(([record]) => record)
      for (const grid of current.values()) grid.counted(records)
    })
  } catch (error) {
    complete = false
  }
  if (counts !== known) return
  for (const region of regions) region.dataset.counted = complete ? "complete" : "failed"
  if (note) note.hidden = complete
}

ready(() => {
  document.addEventListener("shown.bs.tab", showPane)
  load()
})
