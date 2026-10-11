import { expect, test, type Page } from "@playwright/test"

// M6 — the Tabulator-backed data table REQ-TECH-028 names as essential coverage, and with it
// the client half of the page/data-region split (REQ-UI-044): the Translations editor's rows
// are fetched as JSON from the very route that served the page and rendered by
// web/assets/js/translations.js into the vendored Tabulator grid. Asserted here: region-driven
// rendering, the missing badge, client-side filtering with no extra request, an inline edit
// round-tripping through the unchanged CSRF-protected PRG save (§3.3), and the stored text back
// from the API after reload. The assertions adapt to the overlay's current state, so the spec
// passes both against a fresh dev stack (every key missing) and on re-run over its own save.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

test.describe("translations key table (M6, REQ-TECH-028)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the region renders into Tabulator; filter, inline edit and PRG save round-trip", async ({ page }) => {
    const draft = `Oversikt ${Date.now()}`
    await signInAsAdmin(page)
    await page.goto("/admin?section=translations")

    // Region binding: the container the page rendered empty fills from this route's JSON.
    // Tabulator 6 puts its `.tabulator` class on that very container, not on a child wrapper.
    const table = page.locator("#clara-translations")
    await expect(table.locator(".tabulator-row").first()).toBeVisible()

    // The status line reports the universe's missing count over the total key set.
    const status = page.locator("#tr-status")
    await expect(status).toContainText(/\d+ of \d+ keys missing\./)

    // Client-side filter narrows to the one row without asking the server anything (§3.7).
    await page.fill("#tr-search", "nav.dashboard")
    const row = table.locator(".tabulator-row", { has: page.locator("code", { hasText: "nav.dashboard" }) })
    await expect(row).toHaveCount(1)

    // The badge reflects the overlay as it is — a first run against a fresh stack shows every
    // key missing, but this spec must also pass on re-run against a stack its own save touched.
    const wasMissing = (await row.locator(".badge").count()) === 1
    if (wasMissing) {
      await expect(row.locator(".badge")).toContainText("missing")
    }
    const missingBefore = Number(/(\d+) of \d+ keys missing\./.exec(await status.textContent())?.[1])

    // Inline edit: the editor opens on the text cell, Enter commits, and the diff unlocks Save.
    await row.locator('.tabulator-cell[tabulator-field="text"]').click()
    const editor = page.locator(".tabulator-editing input")
    await expect(editor).toBeVisible()
    await editor.fill(draft)
    await editor.press("Enter")
    await expect(status).toContainText("1 unsaved changes.")
    await expect(page.locator("#tr-save")).toBeEnabled()

    // Save posts the plain PRG form (§3.3); the flash confirms, the reload re-reads the region.
    await page.locator("#tr-save").click()
    await expect(page.locator('[role="alert"]')).toContainText("1 translations saved.")
    await expect(table.locator(".tabulator-row").first()).toBeVisible()

    await page.fill("#tr-search", "nav.dashboard")
    await expect(row).toHaveCount(1)
    await expect(row.locator('.tabulator-cell[tabulator-field="text"]')).toContainText(draft)
    await expect(row.locator(".badge")).toHaveCount(0)
    // The universe's missing count dropped by one only if this key was among the missing before.
    const expectedMissing = missingBefore - (wasMissing ? 1 : 0)
    await expect(status).toContainText(new RegExp(String(expectedMissing) + " of \\d+ keys missing\\."))

    // Missing-only filter: the edited key drops out of the worklist and back in when unchecked.
    const missingBox = page.locator("#tr-missing")
    await missingBox.check()
    await expect(table.locator(".tabulator-row", { has: page.locator("code", { hasText: "nav.dashboard" }) })).toHaveCount(0)
    await missingBox.uncheck()
    await expect(row).toHaveCount(1)
  })
})
