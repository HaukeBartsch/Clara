import { expect, test, type Page } from "@playwright/test"

// The project overview keeps itself current (REQ-UI-073, User_Interface_Design.md §4): a project
// created elsewhere appears on an overview that is already open, with the heading's count, and
// without a reload. Driven as the bootstrap administrator of `e2e/dev-stack.sh` in two tabs of one
// session — one watching the overview, one creating the project in the Control Panel. The project
// name is unique per run, so the spec also holds on a long-lived stack.

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

test.describe("project overview refresh (REQ-UI-073)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("a project created in another tab appears on the open overview without a reload", async ({ page, context }) => {
    const projectName = `Refresh ${Date.now()}`

    await signInAsAdmin(page)
    await page.goto("/")
    const grid = page.locator("[data-region='projects']")
    await expect(grid.locator(".clara-project-cell").first()).toBeVisible()
    await expect(grid).not.toContainText(projectName)
    const before = Number(await page.locator("[data-clara-project-count]").textContent())

    // A reload would also show the project; this flag tells the two apart.
    await page.evaluate(() => { (window as unknown as { claraNoReload: boolean }).claraNoReload = true })

    const other = await context.newPage()
    await other.goto("/admin?section=projects")
    await other.locator("details.clara-create summary").click()
    await other.fill("#project-project_name", projectName)
    await other.selectOption("#project-organization", "OUS")
    await other.fill("#project-pi_name", "Principal Investigator")
    await other.fill("#project-pi_email", "pi@example.org")
    await other.locator('form[action*="action=create_project"] button[type="submit"]').click()
    await expect(other.locator('[role="alert"]')).toContainText(projectName)
    await other.close()

    // One refresh period is two seconds; allow a few for a slow stack.
    await expect(grid.getByRole("link", { name: projectName, exact: true })).toBeVisible({ timeout: 10_000 })
    // Other specs create projects on the same stack in parallel, so the count is checked against the
    // grid it heads rather than against a fixed increment.
    const count = page.locator("[data-clara-project-count]")
    await expect.poll(async () => Number(await count.textContent())).toBeGreaterThan(before)
    await expect.poll(async () => Number(await count.textContent()))
      .toBe(await grid.locator(".clara-project-open").count())
    expect(await page.evaluate(() => (window as unknown as { claraNoReload?: boolean }).claraNoReload)).toBe(true)
  })
})
