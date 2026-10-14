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

// The selector lives on the account page (§2.8); the spec comes back to the page it was on.
async function selectTheme(page: Page, value: string): Promise<void> {
  const back = page.url()
  await page.goto("/account")
  await page.selectOption("#clara-theme", value)
  await page.locator('form[action="/theme"] button[type="submit"]').click()
  // Redirects back to `next`, so assert on the selector itself rather than a URL.
  await expect(page.locator("#clara-theme")).toHaveValue(value)
  await page.goto(back)
}

test.describe("theme selector (REQ-UI-040)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  async function themeOptions(page: Page): Promise<string[]> {
    const back = page.url()
    await page.goto("/account")
    const options = await page.locator("#clara-theme option").evaluateAll((els) =>
      els.map((el) => el.getAttribute("value")).filter((v): v is string => v !== null),
    )
    expect(options.length).toBeGreaterThan(1) // "default" plus the installed themes
    await page.goto(back)
    return options
  }

  test("the navbar brand stays legible in every installed theme", async ({ page }) => {
    await signInAsAdmin(page)

    for (const value of await themeOptions(page)) {
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

  // Darkly compiles its component palette behind [data-bs-theme=dark]; if <html> does not
  // declare the scheme, --bs-tertiary-bg stays light and the hover highlight turns the
  // white sidebar text white-on-white.
  test("sidebar links stay legible while hovered in every installed theme", async ({ page }) => {
    await signInAsAdmin(page)
    // The home page has no section panel; Control Panel always renders the sidebar.
    await page.goto("/admin")
    await expect(page.locator("#clara-nav .nav-link").first()).toBeVisible()

    for (const value of await themeOptions(page)) {
      await selectTheme(page, value)
      const link = page.locator("#clara-nav .nav-link:not(.active)").first()
      await expect(link).toBeVisible()
      await link.hover()
      // .nav-link transitions color and background over .15s; sampling mid-transition
      // reads a partially faded highlight, so let it settle first.
      await page.waitForTimeout(300)
      const colors = await link.evaluate((el) => ({
        color: getComputedStyle(el).color,
        bg: getComputedStyle(el).backgroundColor,
      }))
      // The hover rule paints an opaque background; a transparent one would measure as
      // invisible-on-invisible rather than the bug, so assert it changed first.
      expect(colors.bg, `theme ${value}: hover painted no background`).not.toContain("rgba(0, 0, 0, 0)")
      expect(contrastRatio(colors.color, colors.bg), `theme ${value} hover: ${colors.color} on ${colors.bg}`).toBeGreaterThanOrEqual(4.5)
    }
  })
})
