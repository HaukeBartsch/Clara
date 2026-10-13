// Re-captures web/assets/training/01-create-project/0{1..4}.png against a FRESH e2e/dev-stack.sh
// (a second run fails: the project name already exists). Run: node e2e/capture-training-01.mjs
import { chromium } from "playwright"

const WEB = "http://127.0.0.1:8090"
const OUT = new URL("../web/assets/training/01-create-project/", import.meta.url).pathname

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 860 } })

const mark = async (selector) => {
  await page.locator(selector).first().evaluate((el) => {
    el.style.outline = "3px solid #fd7e14"
    el.style.outlineOffset = "2px"
  })
}
const unmark = async (selector) => {
  await page.locator(selector).first().evaluate((el) => { el.style.outline = ""; el.style.outlineOffset = "" })
}
const shot = async (name) => {
  await page.mouse.move(0, 0)
  await page.evaluate(() => document.activeElement?.blur())
  await page.waitForTimeout(300)
  await page.screenshot({ path: OUT + name })
}

await page.goto(WEB + "/login")
await page.getByRole("radio", { name: "Local", exact: true }).check()
await page.fill("#login-email", "admin@example.org")
await page.fill("#login-password", "clara-dev-481516-pw")
await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
await page.locator('header a[href="/admin"]').waitFor()

// 1. Start page: the Control Panel button.
await mark('header a[href="/admin"]')
await shot("01.png")
await unmark('header a[href="/admin"]')

// 2. Control Panel → Projects → New project, form filled.
await page.locator('header a[href="/admin"]').click()
await page.locator("details.clara-create summary").click()
await page.fill("#project-project_name", "Heart Study")
await page.selectOption("#project-organization", "OUS")
await page.fill("#project-pi_name", "Kari Nordmann")
await page.fill("#project-pi_email", "kari.nordmann@example.org")
const submit = 'form[action*="action=create_project"] button[type="submit"]'
await mark(submit)
await shot("02.png")
await unmark(submit)

// 3. Created: the Open link.
await page.locator(submit).click()
const open = page.getByRole("link", { name: "Open Heart Study" })
await open.waitFor()
await open.evaluate((el) => { el.style.outline = "3px solid #fd7e14"; el.style.outlineOffset = "2px"; el.style.borderRadius = "4px" })
await shot("03.png")

// 4. The project's Members section: Add member.
await open.click()
await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
const add = 'form[action*="action=add_member"] button[type="submit"]'
await page.locator(add).waitFor()
await page.selectOption("#member-user", { label: "admin@example.org — Administrator" })
await mark(add)
await shot("04.png")

await browser.close()
