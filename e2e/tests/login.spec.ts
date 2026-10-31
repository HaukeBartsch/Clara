import { expect, test, type Page } from "@playwright/test"

// The browser half of Sequence I (Authentication_Authorization_Design.md §2.9) against a running
// stack: what the source picker offers for a name, and how a directory behind that name changes the
// answer the user reads (Design/Technology_Stack_Design.md §6, REQ-TECH-028).
//
// `e2e/dev-stack.sh` starts an installation with four names — "Ahus (Akerhus)" and "OUS (Oslo)"
// (directories pointed at ports nothing listens on), "PingFed" (a PingFederate OIDC provider) and
// "Local" (the API's own account store). The
// unreachable directories are the point: they are what makes "the directory was down" observable in a browser as
// something other than "wrong password" (§2.2 step 4). As in smoke.spec.ts, no CLARA_WEB_URL means no
// page tests — the suite stays green without an application to point at.

const AHUS = "Ahus (Akerhus)"
const OUS = "OUS (Oslo)"
const PINGFED = "PingFed"
// The dev stack's own bootstrap account (dev-stack.sh: "${stack_seed}-pw"), development only.
const ADMIN_PASSWORD = "clara-dev-481516-pw"

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
    // The institutions by name, the installation's own account store last (REQ-UI-042).
    await expect(page.locator("form[data-clara-source-picker] label")).toHaveText([AHUS, OUS, PINGFED, "Local"])

    // Nothing can verify a password until a name is chosen, so no form is offered for the empty
    // selection — a form whose every answer is "not recognised" is what §2.2 rules out.
    await expect(credentialForm(page)).toHaveCount(0)
  })

  test("offers credentials for a directory name, and no provider button", async ({ page }) => {
    await page.goto("/login")
    await chooseSource(page, AHUS)

    await expect(page.locator('#login-password')).toBeVisible()
    await expect(page.locator('form[action="/login?action=oauth"]')).toHaveCount(0)
  })

  test("reports an unreachable directory as an outage, not as a wrong password", async ({ page }) => {
    await page.goto("/login")
    await chooseSource(page, AHUS)

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
    await chooseSource(page, OUS)

    // A fresh address per run: repeated failures lock an address out (REQ-AUTH-035).
    const email = `researcher-${Date.now()}@example.org`
    await page.fill("#login-email", email)
    await page.fill("#login-password", "not the answer")
    await credentialForm(page).locator('button[type="submit"]').click()

    await expect(alertLine(page)).toBeVisible()
    // The selection rides in the session, so the re-render is still OUS's page (§2.9).
    await expect(sourceRadio(page, OUS)).toBeChecked()
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

  test("takes a username where an address would go, and submits it", async ({ page }) => {
    // REQ-AUTH-050: the field is deliberately not type="email". As it was, the browser refused a
    // bare username before the form could be submitted, so the shorthand existed only in the copy.
    await page.goto("/login")
    await chooseSource(page, "Local")

    await expect(page.locator("#login-email")).toHaveAttribute("type", "text")
    await expect(page.locator('label[for="login-email"]')).toContainText("username")

    // A fresh name per run: repeated failures lock an identifier out (REQ-AUTH-035).
    await page.fill("#login-email", `nobody-${Date.now()}`)
    await page.fill("#login-password", "not the answer")
    await credentialForm(page).locator('button[type="submit"]').click()

    // The submission is what this asserts: an answer from the server, not a validation bubble.
    // Which account the name expands to is the API's business, and it answers with the same
    // credential line an unknown address draws.
    await expect(alertLine(page)).toContainText("not recognised")
  })

  test("signs the bootstrap administrator in by username alone", async ({ page }) => {
    // The positive half of REQ-AUTH-087, against an account the dev stack always has: `admin`
    // is short for admin@example.org, and the session that follows is that account's — the
    // shorthand is a way in, not a second identity.
    await page.goto("/login")
    await chooseSource(page, "Local")

    await page.fill("#login-email", "admin")
    await page.fill("#login-password", ADMIN_PASSWORD)
    await credentialForm(page).locator('button[type="submit"]').click()

    await expect(page.locator("header a[href='/admin']")).toBeVisible()
    await expect(alertLine(page)).toHaveCount(0)
  })

  test("preselects nothing and waits for a choice; choosing applies at once", async ({ page }) => {
    // DEV-UI-13: the login page requires JavaScript — the picker has no button, no name is
    // preselected, and nothing renders below it until a name is chosen.
    await page.goto("/login")

    await expect(page.locator("form[data-clara-source-picker] input[type=radio]:checked")).toHaveCount(0)
    await expect(page.locator("form[data-clara-source-picker] button")).toHaveCount(0)
    await expect(page.locator('form[action="/login?action=oauth"]')).toHaveCount(0)
    await expect(page.locator(".clara-sources")).not.toHaveClass(/clara-sources-chosen/)

    await chooseSource(page, OUS)
    await expect(sourceRadio(page, OUS)).toBeChecked()
    await expect(page.locator("form[data-clara-source-picker] input[type=radio]:checked")).toHaveCount(1)
    await expect(page.locator(".clara-sources")).toHaveClass(/clara-sources-chosen/)
  })

  test("a provider that cannot be reached leads back to the picker, not into a loop", async ({ page }) => {
    // PingFed is that name's only source, so choosing it starts the provider's round trip at
    // once (REQ-AUTH-066); the dev stack's default issuer never answers, so discovery fails.
    await page.goto("/login")
    await sourceRadio(page, PINGFED).click()

    await expect(page.getByRole("heading", { level: 1, name: "Service unavailable" })).toBeVisible()
    await expect(page.locator("body")).toContainText("The sign-in service could not be reached")
    // What the page keeps for debugging (REQ-UI-050): the reference always, and on this
    // development stack the API's reason, the failing call and the provider it could not reach.
    await expect(page.locator("body")).toContainText(/Reference: \S+/)
    await expect(page.locator("body")).toContainText("could not be reached at POST /api/v1/auth/oauth/start")
    await expect(page.locator("body")).toContainText(/\(OAUTH2_1_ISSUER=https?:\/\/\S+\)/)

    // The failed start forgot the name: the picker waits for a choice again rather than
    // sending this session back into the same failing start (§2.2).
    await page.getByRole("link", { name: "Back to sign-in" }).click()
    // A followed link, unlike goto(), returns before login.js runs — and without it a chosen
    // name is never submitted.
    await page.waitForLoadState("load")
    await expect(page.locator("form[data-clara-source-picker]")).toBeVisible()
    await expect(page.locator("form[data-clara-source-picker] input[type=radio]:checked")).toHaveCount(0)

    await chooseSource(page, "Local")
    await expect(page.locator("#login-password")).toBeVisible()
  })
})
