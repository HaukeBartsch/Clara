import { expect, test, type Page } from "@playwright/test"

// The instrument import end to end (REQ-UI-053, REQ-API-152): two REDCap dictionaries chosen in
// one go are previewed in file-name order, reordered — which moves the record identifier — and
// imported into a fresh project, whose empty first instrument receives the first field. The
// imported design then works in the form: a @READONLY field shows no input (REQ-DB-046), and
// branching written in REDCap's spelling over text choice codes shows and hides a field
// (REQ-VAL-044, REQ-VAL-022). Driven as the bootstrap administrator of `e2e/dev-stack.sh`;
// every name is unique per run.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

const HEADER =
  '"Variable / Field Name","Form Name","Section Header","Field Type","Field Label","Choices, Calculations, OR Slider Labels","Field Note","Text Validation Type OR Show Slider Number","Text Validation Min","Text Validation Max",Identifier?,"Branching Logic (Show field only if...)","Required Field?","Custom Alignment","Question Number (surveys only)","Matrix Group Name","Matrix Ranking?","Field Annotation"\n'

const STUDIES =
  HEADER +
  'study_uid,studies,,text,"Study Instance UID",,,,,,,,,,,,,\n' +
  'study_kind,studies,,dropdown,"Modality","MR, Magnetic resonance | CT, Computed tomography | US, Ultrasound",,,,,,,,,,,,\n' +
  "study_note,studies,,notes,\"Scanner notes<br/>\nfor MR and CT only\",,,,,,,\"[study_kind] = 'MR' or [study_kind] = 'CT'\",,,,,,\n" +
  'study_status,studies,,text,"Filled in by FIONA",,,,,,,,,,,,," @READONLY"\n'

const SERIES =
  HEADER +
  'series_uid,series,,text,"Series Instance UID",,,,,,,,,,,,,\n' +
  'series_flags,series,,checkbox,"Flags","1, Localizer | 2, Derived",,,,,,,,,,,,\n'

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

const success = (page: Page) => page.locator(".alert-success").last()

test.describe("instrument import (REQ-UI-053)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("preview, reorder and import two dictionaries, then enter data on the imported design", async ({ page }) => {
    test.setTimeout(120_000)
    const tag = Date.now()
    const projectName = `Import ${tag}`

    await signInAsAdmin(page)
    await page.locator('header a[href="/admin"]').click()
    // The Control Panel opens on Projects (REQ-UI-047); clicking its entry again would
    // reload the page under the next step.
    await expect(page.locator('#clara-nav a[href="/admin?section=projects"]')).toHaveAttribute("aria-current", "page")
    await page.locator("details.clara-create summary").click()
    await page.fill("#project-project_name", projectName)
    await page.selectOption("#project-organization", "OUS")
    await page.fill("#project-pi_name", "Principal Investigator")
    await page.fill("#project-pi_email", "pi@example.org")
    await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
    await page.getByRole("link", { name: `Open ${projectName}` }).click()
    await expect(page).toHaveURL(/\/projects\/\d+\/setup$/)

    // --- preview: the files in file-name order, whatever order they were chosen in ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    await page.setInputFiles("#import-files", [
      { name: "b_series.csv", mimeType: "text/csv", buffer: Buffer.from(SERIES) },
      { name: "a_studies.csv", mimeType: "text/csv", buffer: Buffer.from(STUDIES) },
    ])
    await page.getByRole("button", { name: "Preview" }).click()
    const files = page.locator("[data-clara-import-files] li")
    await expect(files.nth(0)).toContainText("a_studies.csv")
    await expect(files.nth(1)).toContainText("b_series.csv")
    const identifier = page.locator("[data-clara-import-identifier]")
    await expect(identifier).toHaveText("study_uid moves into instrument and becomes the record identifier.")

    // The order decides the record identifier: moving the series up moves it, and back.
    await page.getByRole("button", { name: "Move b_series.csv up" }).click()
    await expect(identifier).toHaveText("series_uid moves into instrument and becomes the record identifier.")
    await page.getByRole("button", { name: "Move b_series.csv down" }).click()
    await expect(identifier).toHaveText("study_uid moves into instrument and becomes the record identifier.")
    await expect(page.locator("[data-clara-import-problems]")).toHaveCount(0)

    // --- import: nothing is mapped, and the confirmation says so ---
    await page.getByRole("button", { name: "Import", exact: true }).click()
    await expect(success(page)).toContainText("instruments created: 2, fields: 6")
    await expect(success(page)).toContainText("collect no data until they are assigned to an event")
    const instruments = page.locator("table.clara-design-instruments tbody tr")
    await expect(instruments).toHaveCount(3)
    await expect(instruments.nth(1)).toContainText("studies")
    await expect(instruments.nth(2)).toContainText("series")

    // --- map the two instruments to baseline, take a token, open a participant ---
    await page.locator("#clara-nav").getByRole("link", { name: "Setup" }).click()
    const matrix = page.locator('form.clara-mapping[data-arm="1"]')
    for (const name of ["instrument", "studies"]) {
      const pair = matrix.locator(`input[data-pair="${name}|baseline_arm_1"]`)
      if (!(await pair.isChecked())) await pair.check()
    }
    await matrix.locator("button[type=submit]").click()
    await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")

    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()

    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await page.fill("#new-record-id", "IMP001")
    await page.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/records\/IMP001/)
    await page.locator(".clara-instrument-nav").getByRole("link", { name: /studies/ }).click()

    const form = page.locator("form[data-clara-record-form]")
    const field = (name: string) => form.locator(`[data-clara-field="${name}"]`)

    // @READONLY: the value is shown, no input is offered (REQ-DB-046).
    await expect(field("study_status").locator("input, textarea, select")).toHaveCount(0)
    await expect(field("study_status")).toContainText("read-only")

    // REDCap's branching over text codes, as imported: shown for MR and CT only.
    await expect(field("study_note")).toBeHidden()
    await page.selectOption("#f-study_kind", "MR")
    await expect(field("study_note")).toBeVisible()
    // The <br/> of the REDCap label is a line break on the form.
    await expect(field("study_note").locator("label")).toContainText("Scanner notes")
    await expect(field("study_note").locator("label br")).toHaveCount(1)
    await page.selectOption("#f-study_kind", "US")
    await expect(field("study_note")).toBeHidden()
  })
})
