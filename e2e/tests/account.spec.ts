import { expect, test, type APIRequestContext, type Page } from "@playwright/test"

// The account page (User_Interface_Design.md §2.8, REQ-UI-063): reached from the header's
// account menu, it carries what the footer used to — language, theme, two-factor, password,
// Sign out — and the member's own project tokens, masked until an audited Show
// (REQ-API-162, REQ-AUD-033).
//
// A fresh member account and project are seeded through the administration API with the
// development service token of e2e/dev-stack.sh. Every name is unique per run.

const API_URL = process.env.CLARA_API_URL ?? "http://127.0.0.1:8085"
const SERVICE_TOKEN = process.env.CLARA_SERVICE_TOKEN ?? "clara-dev-481516-svc" // dev-stack.sh, development only
const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

async function admin(request: APIRequestContext, method: string, path: string, body?: unknown): Promise<any> {
  const response = await request.fetch(`${API_URL}${path}`, {
    method,
    headers: { "X-Internal-Service-Token": SERVICE_TOKEN, "X-Internal-User-Id": "1", "Content-Type": "application/json" },
    data: body === undefined ? undefined : JSON.stringify(body),
  })
  expect(response.ok(), `${method} ${path}: ${response.status()} ${await response.text()}`).toBeTruthy()
  const text = await response.text()
  return text === "" ? null : JSON.parse(text)
}

async function signIn(page: Page, email: string, password: string): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", email)
  await page.fill("#login-password", password)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator("#clara-account-menu")).toBeVisible()
}

test.describe("account page (REQ-UI-063)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the header menu reaches the account page and signs out; the footer is gone", async ({ page }) => {
    await signIn(page, ADMIN_EMAIL, ADMIN_PASSWORD)
    await expect(page.locator("footer")).toHaveCount(0)

    await page.locator("#clara-account-menu").click()
    await page.locator(".clara-account-menu").getByRole("link", { name: "Account" }).click()
    await expect(page).toHaveURL(/\/account$/)
    await expect(page.locator("#clara-language")).toBeVisible()
    await expect(page.locator("#clara-theme")).toBeVisible()
    for (const section of ["/account", "/account/tokens", "/account/two-factor", "/account/password"]) {
      await expect(page.locator(`#clara-nav a[href="${section}"]`)).toHaveCount(1)
    }

    // Sign out from the menu is one click away on every page.
    await page.locator("#clara-account-menu").click()
    await page.locator(".clara-account-menu").getByRole("button", { name: "Sign out" }).click()
    await expect(page).toHaveURL(/\/login/)
  })

  test("a member's own tokens are masked; each Show is audited and Hide masks again", async ({ browser, page, request }) => {
    const tag = Date.now()
    const email = `account-${tag}@example.org`
    const password = `account-${tag}-pw`
    const user = await admin(request, "POST", "/api/v1/users", { email, display_name: `Account ${tag}`, password })
    const project = await admin(request, "POST", "/api/v1/projects",
      { project_name: `Account ${tag}`, organization: "Org", participant_names: "A[0-9][0-9][0-9]" })
    const { token } = await admin(request, "PUT", `/api/v1/projects/${project.id}/users/${user.id}`, { role: null })

    const member = await (await browser.newContext()).newPage()
    await signIn(member, email, password)
    await member.goto("/account/tokens")
    const row = member.locator(`tr[data-project-id="${project.id}"]`)
    await expect(row).toContainText(`Account ${tag}`)
    await expect(row.locator(`#clara-token-${project.id}`)).toHaveValue("")
    expect(await member.content()).not.toContain(token)

    await row.locator("[data-clara-token-show]").click()
    await expect(member.locator(`#clara-token-${project.id}`)).toHaveValue(token)
    await expect(member.locator(`[data-clara-copy="#clara-token-${project.id}"]`)).toBeVisible()

    await member.locator("[data-clara-token-hide]").click()
    await expect(member.locator(`#clara-token-${project.id}`)).toHaveValue("")
    expect(await member.content()).not.toContain(token)

    // The administrator's Audits section holds the one view, by the member, of their own token.
    await signIn(page, ADMIN_EMAIL, ADMIN_PASSWORD)
    await page.goto(`/admin?section=audits&project=${project.id}&event_type=token_viewed`)
    const views = page.locator("table.clara-audits tbody tr", { hasText: "token_viewed" })
    await expect(views).toHaveCount(1)
    await expect(views.first()).toContainText(email)
  })
})
