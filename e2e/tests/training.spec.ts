import { expect, test, type Page } from "@playwright/test"

// The Training page (REQ-UI-059): the header links it, the page shows one card per plan installed
// under web/assets/training/, and a card opens a modal with the plan's picture gallery above its
// text — under the application's Content-Security-Policy, with nothing fetched from outside.
// Driven as the bootstrap administrator of `e2e/dev-stack.sh`.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/training"]')).toBeVisible()
}

test.describe("Training page (REQ-UI-059)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("cards open a plan's gallery and text", async ({ page }) => {
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
    await page.locator('header a[href="/training"]').click()
    await expect(page).toHaveURL(/\/training$/)

    // One card per example plan, in directory order, each with its picture and short text.
    const cards = page.locator(".clara-training-card")
    await expect(cards).toHaveCount(5)
    await expect(cards.locator(".card-title")).toHaveText([
      "Create a new project",
      "Create data collection forms",
      "Collect data",
      "Export data",
      "Export audit trails",
    ])
    await expect(cards.first().locator("img.clara-training-cover")).toHaveJSProperty("complete", true)
    expect(await cards.first().locator("img.clara-training-cover").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0)

    // The modal: the title, the gallery above the text, and the address names the plan.
    await cards.nth(1).click()
    const modal = page.locator("#clara-training-modal")
    await expect(modal).toBeVisible()
    await expect(modal.locator(".modal-title")).toHaveText("Create data collection forms")
    await expect(page).toHaveURL(/\/training#02-data-collection-forms$/)
    const counter = modal.locator("[data-training-counter]")
    await expect(counter).toHaveText("Picture 1 of 6")
    await expect(modal.locator("[data-training-text] ol > li")).toHaveCount(6)
    await expect(modal.locator("[data-training-text] strong", { hasText: "Add event" })).toBeVisible()
    const galleryBox = await modal.locator("[data-training-gallery]").boundingBox()
    const textBox = await modal.locator("[data-training-text]").boundingBox()
    expect(galleryBox!.y).toBeLessThan(textBox!.y)

    // Paging: next, the keyboard, and a thumbnail.
    await modal.locator("[data-training-next]").click()
    await expect(counter).toHaveText("Picture 2 of 6")
    await page.keyboard.press("ArrowRight")
    await expect(counter).toHaveText("Picture 3 of 6")
    await modal.locator(".clara-training-thumb").nth(5).click()
    await expect(counter).toHaveText("Picture 6 of 6")
    await expect(modal.locator(".carousel-item.active img")).toHaveAttribute("src", "/assets/training/02-data-collection-forms/06.png")

    await modal.getByRole("button", { name: "Close" }).last().click()
    await expect(modal).toBeHidden()
    await expect(page).toHaveURL(/\/training$/)

    // A link to a plan opens it.
    await page.goto("/training#05-export-audit-trails")
    await expect(modal).toBeVisible()
    await expect(modal.locator(".modal-title")).toHaveText("Export audit trails")
    await expect(counter).toHaveText("Picture 1 of 2")

    expect(problems).toEqual([])
    expect(outside).toEqual([])
  })
})
