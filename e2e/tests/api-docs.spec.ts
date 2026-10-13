import { expect, test, type Page } from "@playwright/test"

// The Control Panel's API documentation section (REQ-UI-057): the left panel links it, and the
// section renders the API's OpenAPI document with the vendored Swagger UI — read-only, under the
// application's Content-Security-Policy, with nothing fetched from outside the application.
// Driven as the bootstrap administrator of `e2e/dev-stack.sh`.

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

test.describe("API documentation in the Control Panel (REQ-UI-057)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the left panel opens the OpenAPI document, rendered read-only", async ({ page }) => {
    const problems: string[] = []
    const outside: string[] = []
    page.on("console", (message) => {
      if (message.type() === "error") problems.push(message.text())
    })
    const origin = new URL(process.env.CLARA_WEB_URL ?? "http://127.0.0.1:8090").origin
    page.on("request", (request) => {
      const url = new URL(request.url())
      if (url.protocol.startsWith("http") && url.origin !== origin) outside.push(request.url())
    })

    await signInAsAdmin(page)
    await page.locator('header a[href="/admin"]').click()
    await page.locator("#clara-nav").getByRole("link", { name: "API documentation" }).click()
    await expect(page).toHaveURL(/\/admin\?section=api$/)
    await expect(page.locator('#clara-nav a[href="/admin?section=api"]')).toHaveAttribute("aria-current", "page")

    const docs = page.locator(".clara-api-docs .swagger-ui")
    await expect(docs.locator(".info .title")).toContainText("CLARA API")
    await expect(docs.locator(".opblock").first()).toBeVisible()

    // An operation opens with its description, and offers no way to send it.
    const move = docs.locator('.opblock-summary-path[data-path="/api/v1/projects/{id}/instruments/{iid}/fields/{fid}/move"]')
    await move.first().click()
    await expect(docs.locator(".opblock.is-open")).toContainText("REQ-API-157")
    await expect(docs.locator(".try-out__btn")).toHaveCount(0)

    expect(problems).toEqual([])
    expect(outside).toEqual([])
  })
})
