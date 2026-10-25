// The Monitoring section's live polling (§5.11, REQ-UI-064). The server rendered the first
// paint; this module takes over: once per second while the document is visible it re-reads
// this section's data region (`GET /admin?section=monitoring` with Accept: application/json,
// REQ-UI-044 — never /api/v1/* directly, REQ-UI-002), appends one sample to an in-memory
// buffer, and redraws the eight d3 small multiples over the chart period — the window the
// buffer never grows past, `MONITORING_PERIOD_MINUTES` from the environment and ten minutes by
// default (REQ-CFG-034). The endpoint carries counters as of now, not a series: rates are the
// difference between consecutive samples, and a counter that went backwards (an API restart)
// reads as a gap rather than a spike. A tick never overlaps its predecessor, and polling
// pauses while the tab is hidden — so does the line: a stretch with no sample in it ends the curve
// and the next sample starts a new one, because nothing was observed across it. No UI string lives
// here (REQ-UI-045); d3 arrives as the UMD global from the script tag before this module
// (Design/Technology_Stack_Design.md §3).

import { ready, t, fetchJson, el, td, fill } from "../app.js"

const POLL_MS = 1000
/**
 * How far apart two samples may be and still count as one uninterrupted stretch of monitoring.
 * `generated_at` carries second resolution, so a healthy 1 Hz poll lands 1–2 s apart; anything
 * wider than this is not jitter but the page having stopped watching — the tab hidden, the machine
 * asleep, the API unreachable for a few seconds — and no sample speaks for the time in between. The
 * chart says so out loud: the line ends at the last value observed and starts again at the next.
 */
const GAP_TOLERANCE_MS = 4 * POLL_MS
const MIN_SPAN_MS = 60 * 1000 // a freshly opened page still gets a legible x axis
const DEFAULT_PERIOD_MINUTES = 10 // what the page asks for when it says nothing (REQ-CFG-034)
const MAX_PERIOD_MINUTES = 1440 // Config's ceiling: one day of 1 Hz samples is the useful end

/**
 * The chart period, as the rendered section declares it: `MONITORING_PERIOD_MINUTES` reaches this
 * module through `data-clara-mon-period-minutes` on the charts row (the browser never reads the
 * environment, REQ-UI-002). A missing or nonsensical value falls back to the default rather than
 * leaving the page without a window.
 */
function periodMinutes() {
  const node = document.querySelector("[data-clara-mon-period-minutes]")
  const minutes = node ? Number(node.dataset.claraMonPeriodMinutes) : NaN
  if (!Number.isFinite(minutes)) return DEFAULT_PERIOD_MINUTES
  return Math.min(MAX_PERIOD_MINUTES, Math.max(1, Math.floor(minutes)))
}

/** The rolling window in ms: samples older than it are dropped, so the buffer stays bounded. */
let periodMs = DEFAULT_PERIOD_MINUTES * 60 * 1000

/** "2026-10-15 12:34:56" canonical UTC (REQ-DB-005) → epoch ms; the snapshot's clock, not ours. */
function parseUtc(stamp) {
  const parsed = Date.parse(String(stamp).replace(" ", "T") + "Z")
  return Number.isNaN(parsed) ? Date.now() : parsed
}

const num = (value) => (typeof value === "number" && Number.isFinite(value) ? value : 0)

/** Σ over the per-route entries — the totals the deltas of rps, latency and bytes are taken from. */
function routeTotals(metrics) {
  let count = 0
  let sumMs = 0
  let respBytes = 0
  for (const route of (metrics && metrics.http.routes) || []) {
    count += num(route.count)
    sumMs += num(route.sum_ms)
    respBytes += num(route.resp_bytes_total)
  }
  return { count, sumMs, respBytes }
}

// --- value formatting: units are metric abbreviations, not UI strings ---------------------

const fixed = (digits) => (v) => v.toFixed(digits)
function humanBytesPerSecond(v) {
  if (v >= 1048576) return (v / 1048576).toFixed(1) + " MiB/s"
  if (v >= 1024) return (v / 1024).toFixed(1) + " KiB/s"
  return Math.round(v) + " B/s"
}
/** Compact seconds → "45s" / "3:07:41" / "2d 4:12:09", mirroring the PHP first paint. Seconds are
 * part of the format because the card is rewritten once a second: minute resolution would leave it
 * showing the same text for sixty ticks and read as a page that stopped polling. */
function fmtUptime(seconds) {
  const whole = Math.floor(num(seconds))
  if (whole < 60) return whole + "s"
  const d = Math.floor(whole / 86400)
  const h = Math.floor((whole % 86400) / 3600)
  const m = Math.floor((whole % 3600) / 60)
  const clock = h + ":" + String(m).padStart(2, "0") + ":" + String(whole % 60).padStart(2, "0")
  return d > 0 ? d + "d " + clock : clock
}

// --- the chart table: one entry per small multiple -----------------------------------------
// pick reads one series out of a sample (null = gap); stat formats the card's current value.

const CHARTS = [
  { key: "rps", pick: (d) => d.rps, axisFormat: fixed(1) },
  { key: "latency", pick: (d) => d.latencyMs, format: fixed(1), unit: " ms" },
  { key: "goroutines", pick: (d) => d.goroutines, format: (v) => String(Math.round(v)) },
  { key: "heap", pick: (d) => d.heapMiB, format: fixed(1), unit: " MiB" },
  { key: "pool", pick: (d) => d.poolInUse, format: (v) => String(Math.round(v)), ceiling: (d) => d.poolMax },
  { key: "waits", pick: (d) => d.waitsPerS, format: fixed(2) },
  { key: "gc", pick: (d) => d.gcMs, format: fixed(2), unit: " ms" },
  { key: "throughput", pick: (d) => d.bytesPerS, statFormat: humanBytesPerSecond },
]

// --- the sample buffer ----------------------------------------------------------------------

const samples = [] // { t, gap, rps, latencyMs, goroutines, heapMiB, poolInUse, poolMax, waitsPerS, gcMs, bytesPerS }
let prev = null // raw counters of the previous accepted snapshot

/**
 * One snapshot → one sample. Cumulative counters become per-interval rates against the
 * previous snapshot's clock (`generated_at`, so both ends of a delta are the API's own time);
 * any counter that went backwards means the API restarted — the rate series break at a gap,
 * the gauges stay valid, and the baseline resets to this snapshot.
 *
 * A sample that arrives too late to be the next poll (`GAP_TOLERANCE_MS`) is flagged `gap`: it does
 * not continue the curve its predecessor left off, because the interval between them went
 * unsampled — while the tab was hidden nothing was read at all. Its rates are null for the same
 * reason: a rate measured across a stretch nobody watched is not a measurement of this interval, so
 * it stays out of the chart and out of the axis extrema alike. Its gauges are still true at their
 * own timestamp, which is why they are kept and simply start a new line.
 */
function pushSample(metrics) {
  const totals = routeTotals(metrics)
  const pool = (metrics && metrics.db && metrics.db.pool) || {}
  const now = parseUtc(metrics.generated_at)
  const gap = prev !== null && now - prev.t > GAP_TOLERANCE_MS
  const gauge = {
    t: now,
    gap,
    rps: null,
    latencyMs: null,
    goroutines: num(metrics.process.goroutines),
    heapMiB: num(metrics.process.heap_alloc_bytes) / 1048576,
    poolInUse: num(pool.in_use),
    poolMax: num(pool.max_open),
    waitsPerS: null,
    gcMs: num(metrics.process.gc_pause_last_ms),
    bytesPerS: null,
    uptimeSeconds: num(metrics.uptime_seconds),
  }

  // Any cumulative counter that went backwards means the API restarted since the last
  // snapshot: this sample is a gap (rates null, gauges valid) and becomes the new baseline.
  const restarted =
    prev !== null &&
    (now < prev.t ||
      totals.count < prev.count ||
      totals.sumMs < prev.sumMs ||
      totals.respBytes < prev.respBytes ||
      num(pool.wait_count) < prev.waitCount)

  if (!restarted && !gap && prev !== null && now > prev.t) {
    const seconds = (now - prev.t) / 1000
    gauge.rps = Math.max(0, totals.count - prev.count) / seconds
    const countDelta = totals.count - prev.count
    gauge.latencyMs = countDelta > 0 ? Math.max(0, totals.sumMs - prev.sumMs) / countDelta : 0
    gauge.waitsPerS = Math.max(0, num(pool.wait_count) - prev.waitCount) / seconds
    gauge.bytesPerS = Math.max(0, totals.respBytes - prev.respBytes) / seconds
  }

  samples.push(gauge) // one sample per snapshot; the first one only establishes the baseline
  prev = { t: now, count: totals.count, sumMs: totals.sumMs, respBytes: totals.respBytes, waitCount: num(pool.wait_count) }

  prune(now)
}

/**
 * Drop every timepoint outside the period `[now − periodMs, now]`, so a page that stays open keeps
 * one period of history rather than everything since it was opened. The newest sample always stays:
 * a page whose snapshots all predate the window still has to draw something. A snapshot whose
 * `generated_at` never advances (a cached or proxied response) leaves the time cutoff unable to
 * fire at all, so the length is capped as well — at the ≥ 1 s cadence the polling loop keeps, that
 * ceiling is above what a whole period of in-range samples can reach.
 */
function prune(now) {
  const cutoff = now - periodMs
  while (samples.length > 1 && samples[0].t < cutoff) samples.shift()

  const maxSamples = Math.floor(periodMs / POLL_MS) + 2
  while (samples.length > maxSamples) samples.shift()
}

// --- d3 small multiples ---------------------------------------------------------------------

const W = 240
const H = 72
const PLOT_LEFT = 42
const PLOT_RIGHT = W - 4
const PLOT_TOP = 6
const PLOT_BOTTOM = H - 6

/** One figure gets its SVG once; each redraw only rebinds the paths (cheap: tens of points). */
function chartNodes(figure) {
  let nodes = figure.__claraMon
  if (nodes) return nodes
  const svg = d3.select(figure).insert("svg", "figcaption")
    .attr("viewBox", `0 0 ${W} ${H}`)
    .attr("preserveAspectRatio", "none")
    .attr("aria-hidden", "true")
  const axis = svg.append("g").attr("class", "clara-mon-axis")
  axis.append("line")
    .attr("class", "clara-mon-axis-line")
    .attr("x1", PLOT_LEFT).attr("x2", PLOT_LEFT)
    .attr("y1", PLOT_TOP).attr("y2", PLOT_BOTTOM)
  const tick = () => {
    const group = axis.append("g").attr("class", "clara-mon-axis-tick")
    group.append("line")
      .attr("x1", PLOT_LEFT - 3).attr("x2", PLOT_LEFT)
    group.append("text").attr("x", PLOT_LEFT - 5).attr("text-anchor", "end")
    return group
  }
  nodes = {
    svg,
    line: svg.append("path").attr("class", "clara-mon-line"),
    maxTick: tick().attr("class", "clara-mon-axis-tick clara-mon-axis-max"),
    minTick: tick().attr("class", "clara-mon-axis-tick clara-mon-axis-min"),
  }
  nodes.ceiling = svg.append("path").attr("class", "clara-mon-ceiling")
  figure.__claraMon = nodes
  return nodes
}

/**
 * One path out of the points in view: a subpath per contiguous run, so an unsampled stretch reads as
 * the line ending and starting again instead of a straight segment drawn across time nobody
 * observed. A point flagged `gap` opens a new run; a null value is not a point at all and closes the
 * run before it — which is how an API restart reads on the rate charts. A run of one point still
 * contributes its moveto: invisible, but the attribute keeps its shape while the buffer holds a
 * single sample. d3's own `defined` filter stays on the generator as the backstop that no path ever
 * receives a coordinate for a value it does not have.
 */
function linePath(line, points) {
  const runs = []
  let run = null
  for (const point of points) {
    const drawable = point.value !== null && point.value !== undefined
    if (run !== null && (!drawable || point.gap)) {
      runs.push(run)
      run = null
    }
    if (!drawable) {
      continue
    }
    if (run === null) {
      run = []
    }
    run.push(point)
  }
  if (run !== null) runs.push(run)

  return runs.map((each) => line(each)).join("")
}

function drawCharts() {
  if (samples.length === 0) return
  const end = samples[samples.length - 1].t
  let start = end - periodMs // one period wide: the whole window, whatever the buffer holds
  if (end - start < MIN_SPAN_MS) start = end - MIN_SPAN_MS

  // Only the period's own timepoints are drawn and scaled — an older sample would stretch the y
  // axis for a value that is no longer on the chart. `prune` already drops them; this is the same
  // rule where it matters, so a snapshot that arrived out of order cannot reach the plot either.
  const visible = samples.filter((s) => s.t >= start && s.t <= end)
  if (visible.length === 0) return

  for (const chart of CHARTS) {
    const figure = document.querySelector(`[data-clara-mon-chart="${chart.key}"]`)
    if (!figure) continue
    const nodes = chartNodes(figure)
    const values = visible.map(chart.pick).filter((v) => v !== null && v !== undefined)
    if (values.length === 0) {
      nodes.maxTick.attr("display", "none")
      nodes.minTick.attr("display", "none")
      nodes.line.attr("d", null)
      if (chart.ceiling) nodes.ceiling.attr("display", "none")
      continue
    }
    const observedMin = Math.min(...values)
    const observedMax = Math.max(...values)
    const ceilingValue = chart.ceiling ? Math.max(...visible.map(chart.ceiling).filter((v) => v > 0), 0) : 0
    const extent = Math.max(observedMax, ceilingValue) - observedMin
    const padding = Math.max(extent * 0.08, Math.abs(observedMax) * 0.05, 0.01)

    const x = d3.scaleLinear().domain([start, end]).range([0, W])
    const y = d3.scaleLinear()
      .domain([observedMin - padding, Math.max(observedMax, ceilingValue) + padding])
      .range([PLOT_BOTTOM, PLOT_TOP])
    const line = d3.line()
      .defined((d) => d.value !== null && d.value !== undefined)
      .x((d) => PLOT_LEFT + (x(d.t) / W) * (PLOT_RIGHT - PLOT_LEFT))
      .y((d) => y(d.value))

    const points = visible.map((s) => ({ t: s.t, value: chart.pick(s), gap: s.gap === true }))
    nodes.line.attr("d", linePath(line, points))

    const format = chart.axisFormat || chart.format || chart.statFormat || String
    const unit = chart.unit || ""
    nodes.maxTick
      .attr("display", null)
      .attr("transform", `translate(0,${y(observedMax)})`)
      .select("text")
      .attr("dy", observedMax === observedMin ? "-3" : "0")
      .text(format(observedMax) + unit)
    nodes.minTick
      .attr("display", null)
      .attr("transform", `translate(0,${y(observedMin)})`)
      .select("text")
      .attr("dy", observedMax === observedMin ? "8" : "0")
      .text(format(observedMin) + unit)

    if (chart.ceiling && ceilingValue > 0) {
      // the dashed max_open ceiling: a flat line the in-use curve must stay under
      nodes.ceiling
        .attr("d", `M${PLOT_LEFT},${y(ceilingValue)}L${PLOT_RIGHT},${y(ceilingValue)}`)
        .attr("display", null)
    } else if (chart.ceiling) {
      nodes.ceiling.attr("display", "none")
    }
  }
}

// --- the rest of the section: stat cards, freshness line, blocked table ----------------------

const EM_DASH = "—"

function currentValues() {
  const last = samples.length > 0 ? samples[samples.length - 1] : {}
  // A rate the newest sample does not carry — its interval went unsampled (a gap, REQ-UI-064), or this
  // is the first snapshot after the page opened — renders as an em dash, not as the number that was
  // there a minute ago: a card that never changes is indistinguishable from one that is live. The four
  // gauges always carry a value, so they never fall back to the dash.
  const measured = (value, format) => (value === null || value === undefined ? EM_DASH : format(value))
  const byKey = {
    rps: measured(last.rps, fixed(1)),
    latency: measured(last.latencyMs, (v) => fixed(1)(v) + " ms"),
    goroutines: String(Math.round(last.goroutines || 0)),
    heap: fixed(1)(last.heapMiB || 0) + " MiB",
    pool: (last.poolInUse === undefined ? "0" : String(last.poolInUse)) + " / " + String(last.poolMax || 0),
    waits: measured(last.waitsPerS, fixed(2)),
    throughput: measured(last.bytesPerS, humanBytesPerSecond),
    uptime: fmtUptime(last.uptimeSeconds),
  }
  for (const node of document.querySelectorAll("[data-clara-mon-stat]")) {
    const value = byKey[node.dataset.claraMonStat]
    if (value === undefined) {
      continue // a card this function does not own — `blocked` belongs to the rate-limiter render
    }
    node.textContent = value
    node.classList.toggle("text-body-secondary", value === EM_DASH) // the first paint's muted dash
  }
}

function renderRateLimit(rateLimit) {
  const settings = document.querySelector("[data-clara-mon-rate-settings]")
  if (settings && rateLimit) {
    const params = { rpm: String(num(rateLimit.rpm)), minutes: String(num(rateLimit.block_minutes)) }
    settings.textContent = t(rateLimit.enabled ? "admin.monitoring.blocked.on" : "admin.monitoring.blocked.off", params)
  }

  const body = document.querySelector("[data-clara-mon-blocked-body]")
  if (!body) return
  const blocked = (rateLimit && rateLimit.blocked) || []
  const statCard = document.querySelector('[data-clara-mon-stat="blocked"]')
  if (statCard) statCard.textContent = String(blocked.length)
  if (blocked.length === 0) {
    fill(body, [el("tr", { "data-clara-mon-blocked-empty": "" }, el("td", { colspan: "3", class: "text-body-secondary" }, t("admin.monitoring.blocked.none")))])
    return
  }
  fill(
    body,
    blocked.map((entry) =>
      el("tr", null, [
        td(String(entry.ip || "")),
        td(String(entry.blocked_until || ""), "text-nowrap small"),
        td(String(num(entry.seconds_remaining)), "text-nowrap small"),
      ])
    )
  )
}

function showFailed(errorCode) {
  const line = document.querySelector("[data-clara-mon-failed]")
  if (!line) return
  if (errorCode) line.textContent = t("admin.monitoring.failed", { error: errorCode })
  line.hidden = !errorCode
}

const isRunning = (stressTest) => Boolean(stressTest) && stressTest.status === "running"

/** The last **Start Stress Test** press came back without a running worker; this is where the page says so. */
let startFailed = false

/** When the page asked for a run that has not shown up yet (`null` = nothing pending). */
let startingSince = null

/**
 * How long to wait for a requested run to appear in the polled status before calling the start
 * failed. The action writes its `running` status before it launches the worker, so the very next
 * tick would see it; five covers a poll lost on the way.
 */
const START_GRACE_MS = 5 * POLL_MS

function renderStressTest(stressTest) {
  const section = document.querySelector("[data-clara-mon-stress]")
  if (!section) return

  const button = section.querySelector("[data-clara-mon-stress-button]")
  const message = section.querySelector("[data-clara-mon-stress-message]")
  const progress = section.querySelector("[data-clara-mon-stress-progress]")
  const status = stressTest && typeof stressTest.status === "string" ? stressTest.status : ""
  const phases = ["startup", "setup", "import", "export", "complete", "failed"]
  const phase = stressTest && phases.includes(stressTest.phase) ? stressTest.phase : "failed"
  section.dataset.status = status
  if (isRunning(stressTest)) {
    startFailed = false // a worker is up: an earlier press that did not take has been overtaken
    startingSince = null
  } else if (startingSince !== null && Date.now() - startingSince > START_GRACE_MS) {
    startFailed = true // asked for a run, and after a grace of several ticks nothing is running
    startingSince = null
  }
  if (button) button.disabled = status === "running"
  if (progress) {
    progress.hidden = !stressTest
    progress.value = Math.max(0, Math.min(100, num(stressTest && stressTest.progress)))
  }
  if (message) {
    message.hidden = !stressTest
    if (startFailed && status !== "running") {
      // Nothing reloaded the page to carry the server's flash line, so this is where it lives.
      message.hidden = false
      message.textContent = t("admin.monitoring.stress.start_failed")
    } else if (stressTest) {
      const phaseText = t(`admin.monitoring.stress.phase.${phase}`)
      const percent = String(Math.round(progress ? progress.value : 0))
      const knownErrors = ["status_unavailable", "worker_failed"]
      const errorCode = knownErrors.includes(stressTest.error) ? stressTest.error : "worker_failed"
      const error = status === "failed" ? t(`admin.monitoring.stress.error.${errorCode}`) : ""
      message.textContent = [phaseText, t("admin.monitoring.stress.progress_percent", { percent }), error]
        .filter(Boolean)
        .join(" — ")
    }
  }
}

// --- the polling loop -------------------------------------------------------------------------

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

/** Resolves as soon as the document is visible — polling pauses while the tab is hidden. */
function whenVisible() {
  if (!document.hidden) return Promise.resolve()
  return new Promise((resolve) => document.addEventListener("visibilitychange", () => resolve(), { once: true }))
}

let lastSuccessAt = null

/**
 * One poll: read this section's data region and render everything it carries. This is the loop's own
 * rhythm — an action taken on the page never fires a poll of its own, so a tick never overlaps its
 * predecessor; it waits for the next one, which is at most a second away.
 */
async function tick() {
  try {
    const facts = await fetchJson("/admin?section=monitoring")
    if (facts.metrics) {
      pushSample(facts.metrics)
      drawCharts()
      currentValues()
    }
    renderRateLimit(facts.rate_limit || null)
    renderStressTest(facts.stress_test || null)
    showFailed(facts.metrics_error || facts.rate_limit_error || null) // one failing endpoint is the finding, not an error page
    if (facts.metrics) lastSuccessAt = Date.now()
  } catch (error) {
    showFailed(error.message || "internal") // includes "forbidden": the session guard decides on the next navigation
  }

  const updated = document.querySelector("[data-clara-mon-updated]")
  if (updated && lastSuccessAt) {
    updated.textContent = t("admin.monitoring.updated", { seconds: String(Math.round((Date.now() - lastSuccessAt) / 1000)) })
  }
}

async function loop() {
  for (;;) {
    await whenVisible()
    const started = Date.now()
    await tick()
    await sleep(Math.max(100, POLL_MS - (Date.now() - started)))
  }
}

// --- starting the stress test without leaving the page ---------------------------------------------

/**
 * **Start Stress Test** posts its form in place of navigating. The charts are what a stress test
 * exists to watch, and a page load drops the sample buffer — the curve would begin again at empty at
 * the moment the load starts, which is the one stretch worth seeing. So the click posts and stays
 * put: same `?action=`, same CSRF field, and deliberately no `Accept: application/json` (this
 * mutation has no data region, REQ-UI-044), so the server answers with the redirect it already
 * answers a navigation with. Following that redirect is what spends the flash message the action
 * sets — nothing turns up later on whatever page the administrator opens next — and its HTML is
 * discarded; whether a worker came up arrives with the loop's own next read, like everything else on
 * this section. With scripting off the form posts as it always did, reload included.
 */
function watchStressTestForm() {
  const form = document.querySelector("[data-clara-mon-stress-form]")
  if (!form) return
  form.addEventListener("submit", async (event) => {
    event.preventDefault()
    const button = form.querySelector("[data-clara-mon-stress-button]")
    if (button) button.disabled = true
    startFailed = false
    startingSince = Date.now()
    try {
      // The form holds the CSRF token and nothing else, so urlencoded is what the browser would have
      // sent; following the redirect is deliberate — see this function's comment.
      const response = await fetch(form.action, {
        method: "POST",
        body: new URLSearchParams(new FormData(form)),
        credentials: "same-origin",
      })
      if (!response.ok) {
        startFailed = true // refused before the action ran: there is no worker to wait for
        startingSince = null
      }
    } catch {
      startFailed = true
      startingSince = null
    }
  })
}

ready(() => {
  if (!document.querySelector("[data-clara-mon-chart]")) return
  periodMs = periodMinutes() * 60 * 1000 // the configured window, taken before the first sample
  watchStressTestForm() // starting a run leaves the polling loop, and its history, alone
  loop()
})
