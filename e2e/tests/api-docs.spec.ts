import { expect, test, type Page } from "@playwright/test"

// The Control Panel's API documentation section (REQ-UI-057): the left panel links it, and the
// section renders the API's OpenAPI document with the vendored Swagger UI — read-only, the Data
// API first and the only section open, under the application's Content-Security-Policy, with
// nothing fetched from outside the application. Above it sits the relevance Sankey (REQ-UI-060),
// drawn by the vendored d3 pair from the JSON embedded in the page. Driven as the bootstrap
// administrator of `e2e/dev-stack.sh`.

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

/** Console errors and requests to other origins — the section must produce neither. */
function watchForProblems(page: Page): { problems: string[]; outside: string[] } {
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

  return { problems, outside }
}

test.describe("API documentation in the Control Panel (REQ-UI-057)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the left panel opens the OpenAPI document, rendered read-only", async ({ page }) => {
    const { problems, outside } = watchForProblems(page)

    await signInAsAdmin(page)
    await page.locator('header a[href="/admin"]').click()
    await page.locator("#clara-nav").getByRole("link", { name: "API documentation" }).click()
    await expect(page).toHaveURL(/\/admin\?section=api$/)
    await expect(page.locator('#clara-nav a[href="/admin?section=api"]')).toHaveAttribute("aria-current", "page")

    const docs = page.locator(".clara-api-docs .swagger-ui")
    await expect(docs.locator(".info .title")).toContainText("CLARA API")
    await expect(docs.locator(".opblock").first()).toBeVisible()

    // The Data API section comes first and is the only one open; the rest wait collapsed.
    const sections = docs.locator(".opblock-tag-section")
    await expect(sections.first().locator(".opblock-tag")).toHaveAttribute("data-tag", "Data API")
    await expect(sections.first()).toHaveClass(/is-open/)
    await expect(docs.locator(".opblock-tag-section.is-open")).toHaveCount(1)
    await expect(sections.first().locator('.opblock-summary-path[data-path="/api/"]')).toHaveCount(2)

    // A collapsed section opens on its heading; an operation opens with its description, and
    // offers no way to send it.
    await docs.locator('.opblock-tag[data-tag="Admin: Structure"]').click()
    const move = docs.locator('.opblock-summary-path[data-path="/api/v1/projects/{id}/instruments/{iid}/fields/{fid}/move"]')
    await move.first().click()
    await expect(docs.locator(".opblock.is-open")).toContainText("REQ-API-157")
    await expect(docs.locator(".try-out__btn")).toHaveCount(0)

    expect(problems).toEqual([])
    expect(outside).toEqual([])
  })

  test("the relevance Sankey sits above the document and draws from the embedded payload (REQ-UI-060)", async ({ page }) => {
    const { problems, outside } = watchForProblems(page)

    await signInAsAdmin(page)
    await page.goto("/admin?section=api")

    // The vendored d3 + d3-sankey pair renders the embedded structure: bands, nodes, labels.
    const canvas = page.locator("[data-clara-relevance-sankey]")
    await expect(canvas.locator("svg.clara-sankey path.clara-sankey-link").first()).toBeVisible()
    // Every node of the three columns gets a rect and a label (the payload carries 44).
    await expect(canvas.locator("g.clara-sankey-node")).toHaveCount(44)
    await expect(canvas.locator(".clara-sankey-label", { hasText: "Data exchange with study systems" })).toBeVisible()

    // Only the last column is pinned: the relevance classes keep the classification's order —
    // read top to bottom, their payload `order` never decreases. Areas and families follow d3.
    const keptOrder = await page.evaluate(() => {
      const data = JSON.parse(document.querySelector("script[data-clara-sankey-data]")!.textContent!)
      const orderById = new Map(data.nodes.map((n: { id: string; order: number }) => [n.id, n.order]))
      const orders = [...document.querySelectorAll("g.clara-sankey-col-2")]
        .sort((a, b) => Number(a.querySelector("rect")?.getAttribute("y")) - Number(b.querySelector("rect")?.getAttribute("y")))
        .map((node) => orderById.get(node.getAttribute("data-id")))
      return orders.every((order, i, all) => i === 0 || (all[i - 1] as number) <= order)
    })
    expect(keptOrder).toBe(true)

    // The five relevance classes, in the classification's own order, are the legend.
    await expect(canvas.locator(".clara-sankey-legend-item")).toHaveCount(5)
    await expect(canvas.locator(".clara-sankey-legend-item").first()).toContainText("security")

    // At the top of the section: above the Swagger host.
    const box = await canvas.boundingBox()
    const docsBox = await page.locator("[data-clara-api-docs]").boundingBox()
    expect(box?.y).toBeLessThan(docsBox?.y ?? 0)

    expect(problems).toEqual([])
    expect(outside).toEqual([])
  })
})
