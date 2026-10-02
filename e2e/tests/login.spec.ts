import { expect, test, type Page } from "@playwright/test"

// The browser half of Sequence I (Authentication_Authorization_Design.md §2.9) against a running
// stack: what the source picker offers for a name, and how a directory behind that name changes the
// answer the user reads (Plan/Web_Implementation.md §8, REQ-TECH-028).
//
// `e2e/dev-stack.sh` starts an installation with three names — "Local" (the API's own account store),
// "Hospital 1" and "Hospital 2" (directories pointed at ports nothing listens on). The unreachable
// directories are the point: they are what makes "the directory was down" observable in a browser as
// something other than "wrong password" (§2.2 step 4). As in smoke.spec.ts, no CLARA_WEB_URL means no
// page tests — the suite stays green without an application to point at.

const credentialForm = (page: Page) => page.locator('form[action="/login?action=credentials"]')
const alertLine = (page: Page) => page.locator('[role="alert"]')
const sourceRadio = (page: Page, name: string) =>
  page.locator("form[data-clara-source-picker] label", { hasText: name })

async function chooseSource(page: Page, name: string): Promise<void> {
  await sourceRadio(page, name).click()
  await expect(credentialForm(page)).toBeVisible()
}

test.describe("login through a named source (§2.9)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("asks which institution before it asks for a password", async ({ page }) => {
    await page.goto("/login")

    await expect(page.locator("form[data-clara-source-picker]")).toBeVisible()
    await expect(sourceRadio(page, "Local")).toBeVisible()
    await expect(sourceRadio(page, "Hospital 1")).toBeVisible()
    await expect(sourceRadio(page, "Hospital 2")).toBeVisible()

    // Nothing can verify a password until a name is chosen, so no form is offered for the empty
    // selection — a form whose every answer is "not recognised" is what §2.2 rules out.
    await expect(credentialForm(page)).toHaveCount(0)
  })

  test("offers credentials for a directory name, and no provider button", async ({ page }) => {
    await page.goto("/login")
    await chooseSource(page, "Hospital 1")

    await expect(page.locator('#login-password')).toBeVisible()
    await expect(page.locator('form[action="/login?action=oauth"]')).toHaveCount(0)
  })

  test("reports an unreachable directory as an outage, not as a wrong password", async ({ page }) => {
    await page.goto("/login")
    await chooseSource(page, "Hospital 1")

    // A fresh address per run: repeated failures lock an address out (REQ-AUTH-035).
    await page.fill("#login-email", `researcher-${Date.now()}@example.org`)
    await page.fill("#login-password", "not the answer")
    await credentialForm(page).locator('button[type="submit"]').click()

    // The distinguishing property, visible to the user rather than only to a unit test: a directory
    // that could not be reached must never read as a rejected credential.
    await expect(alertLine(page)).toContainText("could not be reached")
    await expect(alertLine(page)).not.toContainText("not recognised")

    await expect(page).toHaveURL(/\/login/)
    await page.goto("/")
    await expect(page).toHaveURL(/\/login/, { message: "a failed directory login leaves no session" })
  })

  test("keeps the chosen name after a failure and never echoes the password back", async ({ page }) => {
    await page.goto("/login")
    await chooseSource(page, "Hospital 2")

    // A fresh address per run: repeated failures lock an address out (REQ-AUTH-035).
    const email = `researcher-${Date.now()}@example.org`
    await page.fill("#login-email", email)
    await page.fill("#login-password", "not the answer")
    await credentialForm(page).locator('button[type="submit"]').click()

    await expect(alertLine(page)).toBeVisible()
    // The selection rides in the session, so the re-render is still Hospital 2's page (§2.9).
    await expect(sourceRadio(page, "Hospital 2")).toBeChecked()
    // REQ-AUTH-036: the page re-renders after a rejected attempt; the password does not come back.
    await expect(page.locator("#login-password")).toHaveValue("")
    await expect(page.locator("#login-email")).toHaveValue(email)
  })

  test("answers the local name with the credential line instead", async ({ page }) => {
    await page.goto("/login")
    await chooseSource(page, "Local")

    // A fresh address per run: the API locks an address out after repeated failures (REQ-AUTH-035),
    // which a long-lived stack would otherwise reach and answer with "too many attempts".
    await page.fill("#login-email", `nobody-${Date.now()}@example.org`)
    await page.fill("#login-password", "not the answer")
    await credentialForm(page).locator('button[type="submit"]').click()

    // Same page, same form, different reason: the name selected decides which source answered, and
    // a local rejection is the credential line rather than the outage line.
    await expect(alertLine(page)).toContainText("not recognised")
  })

  test("preselects nothing and waits for a choice; choosing applies at once", async ({ page }) => {
    // DEV-UI-13: the login page requires JavaScript — the picker has no button, no name is
    // preselected, and nothing renders below it until a name is chosen.
    await page.goto("/login")

    await expect(page.locator("form[data-clara-source-picker] input[type=radio]:checked")).toHaveCount(0)
    await expect(page.locator("form[data-clara-source-picker] button")).toHaveCount(0)
    await expect(page.locator('form[action="/login?action=oauth"]')).toHaveCount(0)
    await expect(page.locator(".clara-sources")).not.toHaveClass(/clara-sources-chosen/)

    await chooseSource(page, "Hospital 2")
    await expect(sourceRadio(page, "Hospital 2")).toBeChecked()
    await expect(page.locator("form[data-clara-source-picker] input[type=radio]:checked")).toHaveCount(1)
    await expect(page.locator(".clara-sources")).toHaveClass(/clara-sources-chosen/)
  })
})
