import { expect, test, type APIRequestContext, type Page } from "@playwright/test"

// The preferred region (master spec "Organization by hospital regions"): the overview groups the
// projects by region, the user's own first and recency inside each group (REQ-UI-076); the
// account page sets the region (REQ-UI-075); "last used" stays the acting user's own (REQ-API-049);
// and the Control Panel lists the Regions section (REQ-UI-078).
//
// Members, projects and memberships are seeded through the administration API with the development
// service token of e2e/dev-stack.sh, so each test sees exactly the projects it made — the bootstrap
// administrator sees every project, and its region is left alone because other specs share it.
// The dev stack's one OAuth2 provider (PingFed) reads no roles claim, so the role mapping itself is
// covered by the Go and PHP suites (regions_test.go, regions_test.php); here the section shows its
// unconfigured state.

const API_URL = process.env.CLARA_API_URL ?? "http://127.0.0.1:8085"
const SERVICE_TOKEN = process.env.CLARA_SERVICE_TOKEN ?? "clara-dev-481516-svc" // dev-stack.sh, development only
const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

async function api(request: APIRequestContext, method: string, path: string, body?: unknown, actor = "1"): Promise<any> {
  const response = await request.fetch(`${API_URL}${path}`, {
    method,
    headers: { "X-Internal-Service-Token": SERVICE_TOKEN, "X-Internal-User-Id": actor, "Content-Type": "application/json" },
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

/** A new local member with a password, unique per run. */
async function member(request: APIRequestContext, label: string): Promise<{ id: number, email: string, password: string }> {
  const tag = `${label}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`
  const email = `${tag}@example.org`
  const password = `${tag}-pw`
  const user = await api(request, "POST", "/api/v1/users", { email, display_name: tag, password })
  return { id: user.id, email, password }
}

/** A new project in one organization with the given members. */
async function project(request: APIRequestContext, name: string, organization: string, members: number[]): Promise<number> {
  const created = await api(request, "POST", "/api/v1/projects",
    { project_name: name, organization, participant_names: "R[0-9][0-9][0-9]" })
  for (const id of members) {
    await api(request, "PUT", `/api/v1/projects/${created.id}/users/${id}`, { role: null })
  }
  return created.id
}

/** The overview's group headings, in page order. */
async function headings(page: Page): Promise<string[]> {
  return page.locator("[data-region='projects'] .clara-project-group").allTextContents()
}

test.describe("preferred region (REQ-UI-075/076/078)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the overview groups by region, the chosen one first, and regroups after a change", async ({ browser, request }) => {
    const user = await member(request, "region")
    const tag = Date.now()
    await project(request, `Region SUN ${tag}`, "SUN", [user.id])
    await project(request, `Region OUS ${tag}`, "OUS", [user.id])
    await project(request, `Region Ahus ${tag}`, "Ahus", [user.id])

    const page = await (await browser.newContext()).newPage()
    await signIn(page, user.email, user.password)
    await page.goto("/")
    // No region yet: every group in REQ-DB-006 order.
    await expect.poll(() => headings(page)).toEqual(["OUS · 1", "Ahus · 1", "SUN · 1"])

    // The account page sets the region; the overview follows on its next read.
    await page.goto("/account")
    await expect(page.locator("#clara-region option").first()).toHaveText("Automatic — none")
    await page.selectOption("#clara-region", "SUN")
    await page.locator('form[action="/account?action=set_region"] button[type="submit"]').click()
    await expect(page.locator("#clara-region")).toHaveValue("SUN")
    await page.goto("/")
    await expect.poll(() => headings(page)).toEqual(["Your region: SUN · 1", "OUS · 1", "Ahus · 1"])

    // A change made in another tab regroups the open overview without a reload (REQ-UI-073).
    const other = await page.context().newPage()
    await other.goto("/account")
    await other.selectOption("#clara-region", "Ahus")
    await other.locator('form[action="/account?action=set_region"] button[type="submit"]').click()
    await expect(other.locator("#clara-region")).toHaveValue("Ahus")
    await other.close()
    await expect.poll(() => headings(page), { timeout: 10_000 })
      .toEqual(["Your region: Ahus · 1", "OUS · 1", "SUN · 1"])
    const firstBox = page.locator("[data-region='projects'] .clara-project-open").first()
    await expect(firstBox).toHaveText(`Region Ahus ${tag}`)
  })

  test("no heading when every project shares one region", async ({ browser, request }) => {
    const user = await member(request, "oneregion")
    const tag = Date.now()
    await project(request, `One A ${tag}`, "OUS", [user.id])
    await project(request, `One B ${tag}`, "OUS", [user.id])

    const page = await (await browser.newContext()).newPage()
    await signIn(page, user.email, user.password)
    await page.goto("/")
    await expect(page.locator("[data-region='projects'] .clara-project-open")).toHaveCount(2)
    await expect(page.locator("[data-region='projects'] .clara-project-group")).toHaveCount(0)
  })

  test("another member working in a project today never makes it read \"today\" for this one", async ({ browser, request }) => {
    const first = await member(request, "today-first")
    const second = await member(request, "today-second")
    const name = `Today ${Date.now()}`
    const id = await project(request, name, "OUS", [first.id, second.id])

    // The second member works in the project today: revealing their own token is an audited,
    // project-scoped event of theirs (REQ-API-162).
    await api(request, "POST", `/api/v1/users/me/projects/${id}/token-reveal`, undefined, String(second.id))

    const firstPage = await (await browser.newContext()).newPage()
    await signIn(firstPage, first.email, first.password)
    await firstPage.goto("/")
    const firstBox = firstPage.locator("[data-region='projects'] .clara-project-cell", { hasText: name })
    await expect(firstBox.locator(".clara-project-used")).toHaveText("Never opened")

    const secondPage = await (await browser.newContext()).newPage()
    await signIn(secondPage, second.email, second.password)
    await secondPage.goto("/")
    const secondBox = secondPage.locator("[data-region='projects'] .clara-project-cell", { hasText: name })
    await expect(secondBox.locator(".clara-project-used")).toHaveText("Last used today")
  })

  test("the Control Panel lists Regions and says when no provider reads roles", async ({ page }) => {
    await signIn(page, ADMIN_EMAIL, ADMIN_PASSWORD)
    await page.goto("/admin?section=regions")
    await expect(page.locator('#clara-nav a[href="/admin?section=regions"]')).toHaveAttribute("aria-current", "page")
    await expect(page.locator("main")).toContainText("OAUTH2_N_ROLES_ATTR")
    await expect(page.locator('form[action*="action=add_role"]')).toHaveCount(0)

    await page.goto("/admin?section=users")
    await expect(page.locator("table").first().locator("thead")).toContainText("Region")
  })
})
