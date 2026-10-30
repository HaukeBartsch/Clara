import { expect, test, type APIRequestContext, type Page } from "@playwright/test"

// Record Status Dashboard display time for a large project (User_Interface_Design.md §6.3,
// REQ-UI-019) — a measurement, so it runs only when CLARA_PERF_PROJECT names a project that is
// already filled: the Incoming project stress/incoming_stress.py builds (400,000 records by
// default). Each run opens /projects/{id}/record-status cold and reads the page's own clocks
// (Navigation and Resource Timing): the HTML, the `records` data region — PHP relaying the API's
// record-status read line by line as it streams (REQ-UI-044, REQ-UI-074) — the first animation
// frame in which the Tabulator grid shows a row, and the frame in which the load ended. The
// numbers are printed and attached as JSON; the load has to end complete with every record in
// the grid (CLARA_PERF_RECORDS, when set), and a data region that fails is a failed test, not a
// slow one.
//
//   CLARA_WEB_URL=http://127.0.0.1:8090 CLARA_PERF_PROJECT=Incoming CLARA_PERF_RECORDS=400000 \
//     npx playwright test --config e2e/playwright.config.ts incoming-dashboard --project chromium
//
// Driven as the bootstrap administrator of `e2e/dev-stack.sh`.

const API_URL = process.env.CLARA_API_URL ?? "http://127.0.0.1:8085"
const SERVICE_TOKEN = process.env.CLARA_SERVICE_TOKEN ?? "clara-dev-481516-svc" // dev-stack.sh, development only
const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

const PROJECT = process.env.CLARA_PERF_PROJECT ?? ""
const RUNS = Number(process.env.CLARA_PERF_RUNS ?? 3)
const RECORDS = process.env.CLARA_PERF_RECORDS ? Number(process.env.CLARA_PERF_RECORDS) : null
const TIMEOUT = Number(process.env.CLARA_PERF_TIMEOUT_MS ?? 600_000)

async function admin(request: APIRequestContext, path: string): Promise<any> {
  const response = await request.fetch(`${API_URL}${path}`, {
    headers: { "X-Internal-Service-Token": SERVICE_TOKEN, "X-Internal-User-Id": "1" },
  })
  expect(response.ok(), `GET ${path}: ${response.status()}`).toBeTruthy()
  return response.json()
}

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

const seconds = (ms: number | undefined) => (ms === undefined ? "—" : (ms / 1000).toFixed(2) + " s")

test.describe("record status dashboard display time (§6.3)", () => {
  test.skip(!process.env.CLARA_WEB_URL || !PROJECT,
    "CLARA_WEB_URL and CLARA_PERF_PROJECT not set — no running stack with a filled project to measure")

  test(`display of ${PROJECT || "the project"}`, async ({ page, request }, testInfo) => {
    test.setTimeout(RUNS * TIMEOUT + 120_000)
    const projects: Array<{ id: number; project_name: string }> = await admin(request, "/api/v1/projects")
    const project = projects.find((p) => p.project_name === PROJECT)
    expect(project, `no project named "${PROJECT}"`).toBeTruthy()
    const path = `/projects/${project!.id}/record-status`

    await signInAsAdmin(page)
    const runs = []
    for (let run = 1; run <= RUNS; run++) {
      // The region is the page's own URL, fetched as NDJSON (record-status.js, readRecords).
      const region = page.waitForResponse(
        (r) => new URL(r.url()).pathname === path && r.request().resourceType() === "fetch",
        { timeout: TIMEOUT })
      await page.goto(path, { waitUntil: "commit" })
      const response = await region
      const failure = response.status() === 200 ? "" : ": " + (await response.text()).slice(0, 400)
      expect(response.status(), `the records data region (PHP relaying GET …/record-status)${failure}`).toBe(200)

      // Polled once per animation frame: the time is the frame the first row was painted in.
      const firstRow = await (await page.waitForFunction(
        () => (document.querySelector(".clara-record-status .tabulator-row") ? performance.now() : null),
        null, { polling: "raf", timeout: TIMEOUT })).jsonValue() as number

      // record-status.js marks the region when the load ends: the end line arrived naming every
      // record, or the stream broke off (REQ-UI-074).
      const [outcome, loaded] = await (await page.waitForFunction(() => {
        const region = document.querySelector(".clara-record-status[data-loaded]") as HTMLElement | null
        return region ? [region.dataset.loaded, performance.now()] : null
      }, null, { polling: "raf", timeout: TIMEOUT })).jsonValue() as [string, number]
      expect(outcome, "the load ended with its end line").toBe("complete")

      const timing = await page.evaluate((regionPath) => {
        const nav = performance.getEntriesByType("navigation")[0] as PerformanceNavigationTiming
        const fetched = (performance.getEntriesByType("resource") as PerformanceResourceTiming[])
          .find((e) => e.initiatorType === "fetch" && new URL(e.name).pathname === regionPath)
        return {
          htmlFirstByte: nav.responseStart,
          html: nav.responseEnd,
          domContentLoaded: nav.domContentLoadedEventEnd,
          regionStart: fetched?.startTime,
          regionFirstByte: fetched?.responseStart,
          region: fetched?.responseEnd,
          regionBytes: fetched?.encodedBodySize,
          regionDecodedBytes: fetched?.decodedBodySize,
        }
      }, path)

      // The grid's own row count, from the module instance record-status.js built it with.
      const rows = await page.evaluate(async () => {
        const url = "/assets/vendor/tabulator/tabulator_esm.min.js"
        const { TabulatorFull } = await import(url)
        const tables = TabulatorFull.findTable(".clara-record-status-grid") || []
        return tables.length ? tables[0].getDataCount() : 0
      })
      if (RECORDS !== null) expect(rows, "rows in the grid").toBe(RECORDS)

      const result = { run, rows, firstRow, loaded, ...timing }
      runs.push(result)
      console.log(`run ${run}/${RUNS}: ${rows} rows — HTML ${seconds(timing.html)}, ` +
        `region ${seconds(timing.regionStart)} → ${seconds(timing.region)} ` +
        `(first byte ${seconds(timing.regionFirstByte)}, ${((timing.regionBytes ?? 0) / 1048576).toFixed(1)} MB), ` +
        `first row painted ${seconds(firstRow)}, all rows loaded ${seconds(loaded)}`)
    }

    const median = (values: number[]) => [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)]
    const summary = {
      project: PROJECT,
      browser: testInfo.project.name,
      median_first_row_ms: median(runs.map((r) => r.firstRow)),
      median_loaded_ms: median(runs.map((r) => r.loaded)),
      median_region_ms: median(runs.map((r) => (r.region ?? 0) - (r.regionStart ?? 0))),
      runs,
    }
    console.log(`median: region fetch ${seconds(summary.median_region_ms)}, first row painted ` +
      `${seconds(summary.median_first_row_ms)}, all rows loaded ${seconds(summary.median_loaded_ms)}`)
    await testInfo.attach("dashboard-timing.json", { body: JSON.stringify(summary, null, 2), contentType: "application/json" })
  })
})
