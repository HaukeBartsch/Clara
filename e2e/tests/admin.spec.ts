import { expect, test, type Page } from "@playwright/test"

// M3 exit criterion (Plan/Web_Implementation.md §6, charter success criterion 3): a full project
// lifecycle — create project, add role, add member, show token once, rotate, remove — entirely
// through the UI, as the bootstrap administrator of `e2e/dev-stack.sh`. Every name is unique per
// run, so the spec also holds on a long-lived stack.

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

// Destructive actions confirm in a modal first (§3.5); only its confirm button sends the form.
async function confirmDialog(page: Page): Promise<void> {
  const dialog = page.locator("#clara-confirm")
  await expect(dialog).toBeVisible()
  await dialog.locator("[data-clara-confirm-ok]").click()
}

test.describe("administration surface (M3)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("project lifecycle entirely through the UI: create, role, member, token once, rotate, remove", async ({ page }) => {
    const tag = Date.now()
    const email = `m3-member-${tag}@example.org`
    const projectName = `M3 ${tag}`
    const roleName = `entry-${tag}`

    await signInAsAdmin(page)

    // An account to make a member (Control Panel → Users).
    await page.locator('header a[href="/admin"]').click()
    await page.locator('#clara-nav a[href="/admin?section=users"]').click()
    await page.locator("details.clara-create summary").click()
    await page.fill("#user-email", email)
    await page.fill("#user-name", `Member ${tag}`)
    await page.fill("#user-password", "m3-member-password-1")
    await page.fill("#user-repeat", "m3-member-password-1")
    await page.locator('form[action*="action=create_user"] button[type="submit"]').click()
    await expect(page.locator('[role="alert"]')).toContainText(email)

    // Create the project (Control Panel → Projects), then open it from the success line.
    await page.locator('#clara-nav a[href="/admin?section=projects"]').click()
    await page.locator("details.clara-create summary").click()
    await page.fill("#project-project_name", projectName)
    await page.selectOption("#project-organization", "HBE")
    await page.fill("#project-pi_name", "Principal Investigator")
    await page.fill("#project-pi_email", "pi@example.org")
    await page.fill("#project-participant_names", "M3[0-9][0-9][0-9]")
    await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
    await page.getByRole("link", { name: `Open ${projectName}` }).click()
    await expect(page.locator("header")).toContainText(projectName)

    // Add a role (project page → Roles).
    await page.locator("#clara-nav").getByRole("link", { name: "Roles" }).click()
    await page.fill("#role-name", roleName)
    // The arm default its pairs inherit; a fresh project has no events yet, so the matrix has
    // no rows here (DEV-DB-14) and design.spec.ts covers the mapped case.
    await page.selectOption("#arm-data-1", "view_edit")
    await page.locator('form[action*="action=save_role"] button[type="submit"]').click()
    await expect(page.locator("table.clara-roles")).toContainText(roleName)

    // Add the member with that role: the token is shown exactly once (§3.5).
    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    await page.locator("#member-user").selectOption({ label: `${email} — Member ${tag}` })
    await page.selectOption("#member-role", roleName)
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
    const reveal = page.locator("#clara-new-token")
    await expect(reveal).toBeVisible()
    const firstToken = await reveal.inputValue()
    expect(firstToken).toMatch(/^[0-9a-f-]{36}$/)
    const row = page.locator(`table.clara-members tr:has-text("${email}")`)
    await expect(row).toContainText("issued")
    await page.reload()
    await expect(page.locator("#clara-new-token")).toHaveCount(0)
    await expect(page.locator("body")).not.toContainText(firstToken)

    // Rotate (confirm): a new token, again shown once.
    await row.locator('form[action*="action=rotate_token"] button').click()
    await confirmDialog(page)
    await expect(page.locator("#clara-new-token")).toBeVisible()
    const secondToken = await page.locator("#clara-new-token").inputValue()
    expect(secondToken).not.toBe(firstToken)

    // Remove (confirm): the member is gone.
    await page.locator(`table.clara-members tr:has-text("${email}") form[action*="action=remove_member"] button`).click()
    await confirmDialog(page)
    await expect(page.locator('[role="alert"]')).toContainText("removed")
    await expect(page.locator("table.clara-members")).not.toContainText(email)
  })
})
