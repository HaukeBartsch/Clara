import { expect, test } from "@playwright/test"

// Harness smoke tests (REQ-TECH-028): they prove Playwright drives a real browser before any
// `web/` page exists to point it at, and pin the two client assumptions the UI rests on — DOM
// population from a payload (REQ-UI-032) and vanilla ES2020 with no framework (REQ-TECH-001).
// Replace these with page-level specs as M0 lands; keep the last test as the app-entry probe.

test("chromium launches and reports a version", async ({ browser }) => {
  expect(browser.version()).toMatch(/^\d+\.\d+/)
})

test("a script populates a rendering target from a payload", async ({ page }) => {
  await page.setContent('<table class="table table-sm"><tbody id="rows"></tbody></table>')
  const records = [
    { id: "rec-1", label: "Participant A" },
    { id: "rec-2", label: "Participant B" },
  ]

  await page.evaluate((rows) => {
    const target = document.querySelector("#rows")
    for (const record of rows) {
      const row = document.createElement("tr")
      row.innerHTML = `<td>${record.id}</td><td>${record.label}</td>`
      target?.appendChild(row)
    }
  }, records)

  await expect(page.locator("#rows tr")).toHaveCount(2)
  await expect(page.locator("#rows tr").first()).toContainText("Participant A")
})

test("vanilla ES2020 runs untranspiled in the browser", async ({ page }) => {
  const result = await page.evaluate(() => {
    const scores: Array<{ label: string; score: number | null }> = [
      { label: "A", score: null },
      { label: "B", score: 3 },
    ]
    const total = scores.reduce((sum, entry) => sum + (entry.score ?? 0), 0)
    return { total, formatted: new Intl.NumberFormat("nb-NO").format(1234.5) }
  })

  expect(result.total).toBe(3)
  // Thousands grouping (a non-breaking space here) depends on the bundled ICU data, so assert the
  // locale property that matters for the UI: nb-NO separates decimals with a comma.
  expect(result.formatted).toContain(",")
  expect(result.formatted).not.toContain(".")
})

// Probe for the running PHP application; skipped until a base URL is supplied so the suite
// stays green without `php -S` (Plan/Web_Implementation.md §8, integration + browser tiers).
test("the web application answers at its base URL", async ({ request }) => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application")
  const response = await request.get("/")
  expect(response.ok()).toBe(true)
})
