import { expect, test, type Page } from "@playwright/test"

// Theme regression (REQ-UI-040): every installed theme must keep the header legible.
// Bootswatch compiles the navbar brand for a light navbar and only flips it under
// data-bs-theme="dark", which web/ never sets — so a theme whose body is dark turns
// the "CLARA" brand invisible without the re-point in assets/app.css (.clara-brand).

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

// WCAG relative-luminance contrast ratio of two "rgb(r, g, b)" computed colors.
function contrastRatio(fg: string, bg: string): number {
  const channel = (v: number): number => {
    const c = v / 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  const luminance = (rgb: string): number => {
    const [r, g, b] = rgb.match(/\d+/g)!.slice(0, 3).map(Number)
    return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
  }
  const [hi, lo] = [luminance(fg), luminance(bg)].sort((a, b) => b - a)
  return (hi + 0.05) / (lo + 0.05)
}

async function selectTheme(page: Page, value: string): Promise<void> {
  await page.selectOption("#clara-theme", value)
  await page.locator('form[action="/theme"] button[type="submit"]').click()
  await expect(page).toHaveURL(/\/$/)
}

test.describe("theme selector (REQ-UI-040)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the navbar brand stays legible in every installed theme", async ({ page }) => {
    await signInAsAdmin(page)

    const options = await page.locator("#clara-theme option").evaluateAll((els) =>
      els.map((el) => el.getAttribute("value")).filter((v): v is string => v !== null),
    )
    expect(options.length).toBeGreaterThan(1) // "default" plus the installed themes

    for (const value of options) {
      await selectTheme(page, value)
      const brand = page.locator(".navbar-brand")
      await expect(brand).toHaveText("CLARA")
      const colors = await brand.evaluate((el) => ({
        color: getComputedStyle(el).color,
        bodyBg: getComputedStyle(document.body).backgroundColor,
      }))
      // 4.5:1 is the WCAG AA bar for normal text; the brand is larger than that already.
      expect(contrastRatio(colors.color, colors.bodyBg), `theme ${value}: ${colors.color} on ${colors.bodyBg}`).toBeGreaterThanOrEqual(4.5)
    }
  })
})
