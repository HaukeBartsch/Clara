import { expect, test, type APIRequestContext, type Locator, type Page } from "@playwright/test"

// The overview's search and starred projects (User_Interface_Design.md §4): the search beside the
// project count filters every group by name, and a group with nothing left goes with its heading
// (REQ-UI-086); a star under a box's mode badge is stored for the account, and "Starred only" hides
// every other project, remembered across reloads (REQ-UI-087). Both hold under the two-second refresh
// (REQ-UI-073).
//
// Members, projects and memberships are seeded through the administration API with the development
// service token of e2e/dev-stack.sh, so each test sees exactly the projects it made.

const API_URL = process.env.CLARA_API_URL ?? "http://127.0.0.1:8085"
const SERVICE_TOKEN = process.env.CLARA_SERVICE_TOKEN ?? "clara-dev-481516-svc" // dev-stack.sh, development only

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

/** A new local member with a password and a region of their own, unique per run. */
async function member(request: APIRequestContext, label: string, region: string): Promise<{ id: number, email: string, password: string }> {
  const tag = `${label}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`
  const email = `${tag}@example.org`
  const password = `${tag}-pw`
  const user = await api(request, "POST", "/api/v1/users", { email, display_name: tag, password })
  await api(request, "PUT", "/api/v1/users/me/region", { region }, String(user.id))
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

function grid(page: Page): Locator {
  return page.locator("[data-region='projects']")
}

function box(page: Page, name: string): Locator {
  return grid(page).locator(".clara-project-cell").filter({ has: page.getByRole("link", { name, exact: true }) })
}

async function headings(page: Page): Promise<string[]> {
  return grid(page).locator(".clara-project-group h2:visible").allTextContents()
}

test.describe("overview search and starred projects (REQ-UI-086/087)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the search filters every group by name and hides a group with nothing left", async ({ browser, request }) => {
    const user = await member(request, "search", "OUS")
    const tag = Date.now()
    await project(request, `Search Alpha ${tag}`, "OUS", [user.id])
    await project(request, `Search Beta ${tag}`, "OUS", [user.id])
    await project(request, `Search Alpha Ahus ${tag}`, "Ahus", [user.id])

    const page = await (await browser.newContext()).newPage()
    await signIn(page, user.email, user.password)
    await page.goto("/")
    await expect.poll(() => headings(page)).toEqual(["Your region: OUS · 2", "Ahus · 1"])

    // The controls sit in the heading row beside the project count, outside the grid.
    const search = page.getByRole("searchbox", { name: "Search projects by name" })
    await expect(grid(page).locator("[data-clara-project-search]")).toHaveCount(0)
    await search.fill("ALPHA")
    await expect(box(page, `Search Alpha ${tag}`)).toBeVisible()
    await expect(box(page, `Search Beta ${tag}`)).toBeHidden()
    await expect(box(page, `Search Alpha Ahus ${tag}`)).toBeVisible()
    await expect.poll(() => headings(page)).toEqual(["Your region: OUS · 1 of 2", "Ahus · 1"])

    // Another region's group is searched too: with no match there, it goes with its heading.
    await search.fill("beta")
    await expect(box(page, `Search Alpha Ahus ${tag}`)).toBeHidden()
    await expect.poll(() => headings(page)).toEqual(["Your region: OUS · 1 of 2"])

    // A project added while the search is active arrives under it on the next refresh (REQ-UI-073).
    await project(request, `Search Gamma ${tag}`, "OUS", [user.id])
    await expect.poll(() => headings(page), { timeout: 10_000 }).toEqual(["Your region: OUS · 1 of 3"])
    await expect(box(page, `Search Gamma ${tag}`)).toBeHidden()
    await expect(box(page, `Search Beta ${tag}`)).toBeVisible()
    await expect(search).toBeFocused()
    await expect(search).toHaveValue("beta")

    await search.fill("zzz")
    await expect.poll(() => headings(page)).toEqual([])
    await expect(page.locator("[data-clara-project-filter-empty]")).toHaveText("No project has “zzz” in its name.")

    await search.fill("")
    await expect(page.locator("[data-clara-project-filter-empty]")).toBeHidden()
    await expect.poll(() => headings(page)).toEqual(["Your region: OUS · 3", "Ahus · 1"])
  })

  test("a star is stored, and Starred only shows the starred projects alone across reloads", async ({ browser, request }) => {
    const user = await member(request, "stars", "OUS")
    const tag = Date.now()
    await project(request, `Star One ${tag}`, "OUS", [user.id])
    const two = await project(request, `Star Two ${tag}`, "OUS", [user.id])
    await project(request, `Star Three ${tag}`, "Ahus", [user.id])

    const page = await (await browser.newContext()).newPage()
    await signIn(page, user.email, user.password)
    await page.goto("/")
    const star = page.getByRole("button", { name: `Star Star Two ${tag}`, exact: true })
    await expect(star).toHaveAttribute("aria-pressed", "false")

    // The star is a control of its own: it toggles in place and never opens the project.
    await star.click()
    await expect(star).toHaveAttribute("aria-pressed", "true")
    await expect(page).toHaveURL(/\/$/)
    await expect.poll(async () => {
      const list: Array<{ id: number, starred: boolean }> = await api(request, "GET", "/api/v1/projects", undefined, String(user.id))
      return list.find((p) => p.id === two)?.starred
    }).toBe(true)
    // Starring is not using the project: the box keeps its "never" line (REQ-UI-071).
    await expect(box(page, `Star Two ${tag}`).locator(".clara-project-used")).toHaveText("Never opened")
    // A refresh confirms the star rather than flicking it back.
    await page.waitForTimeout(2_500)
    await expect(star).toHaveAttribute("aria-pressed", "true")

    const starredOnly = page.getByRole("switch", { name: "Starred only" })
    await starredOnly.check()
    await expect(box(page, `Star Two ${tag}`)).toBeVisible()
    await expect(box(page, `Star One ${tag}`)).toBeHidden()
    await expect(box(page, `Star Three ${tag}`)).toBeHidden()
    // The Ahus group has nothing starred, so its heading goes too.
    await expect.poll(() => headings(page)).toEqual(["Your region: OUS · 1 of 2"])

    await page.reload()
    await expect(page.getByRole("switch", { name: "Starred only" })).toBeChecked()
    await expect(box(page, `Star Two ${tag}`)).toBeVisible()
    await expect(box(page, `Star One ${tag}`)).toBeHidden()

    // Both filters at once: a name that only an unstarred project carries leaves nothing.
    const search = page.getByRole("searchbox", { name: "Search projects by name" })
    await search.fill("one")
    await expect(page.locator("[data-clara-project-filter-empty]")).toHaveText("No starred project has “one” in its name.")
    await search.fill("")
    await expect(box(page, `Star Two ${tag}`)).toBeVisible()

    // Unstarring under the switch hides the box at once and says nothing is starred. Pressed
    // from the keyboard, so the star holds focus in every engine (Safari does not focus a clicked
    // button): focus moves on to the switch instead of dropping onto the page.
    const unstar = page.getByRole("button", { name: `Star Star Two ${tag}`, exact: true })
    await unstar.focus()
    await unstar.press("Enter")
    await expect(box(page, `Star Two ${tag}`)).toBeHidden()
    await expect(page.locator("[data-clara-project-filter-empty]")).toContainText("No starred project.")
    await expect.poll(() => headings(page)).toEqual([])
    await expect(page.getByRole("switch", { name: "Starred only" })).toBeFocused()

    await page.getByRole("switch", { name: "Starred only" }).uncheck()
    for (const name of [`Star One ${tag}`, `Star Two ${tag}`, `Star Three ${tag}`]) {
      await expect(box(page, name)).toBeVisible()
    }
    await expect.poll(() => headings(page)).toEqual(["Your region: OUS · 2", "Ahus · 1"])
  })
})
