import { expect, test, type Locator, type Page } from "@playwright/test"

// Field branching against one record's values (GD-13, REQ-VAL-029, Data_Validation_Design.md
// §7.3/§7.4, User_Interface_Design.md §8.4). branching.spec.mjs proves the evaluator on its
// own; this spec proves the page around it: which values each expression reads, when it is
// re-run, and what hiding leaves alone. On one form the inputs decide while they change, and the
// stored values decide when the record is reopened; a field of another instrument reads this
// record's stored values — label constants and a bare yes/no answer included — and never those
// of another record. Hiding is display-only (DEV-VAL-5): a hidden answer stays stored. The
// design comes in as two REDCap dictionaries (REQ-UI-053). Driven as the bootstrap
// administrator of `e2e/dev-stack.sh`; every name is unique per run.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

const HEADER =
  '"Variable / Field Name","Form Name","Section Header","Field Type","Field Label","Choices, Calculations, OR Slider Labels","Field Note","Text Validation Type OR Show Slider Number","Text Validation Min","Text Validation Max",Identifier?,"Branching Logic (Show field only if...)","Required Field?","Custom Alignment","Question Number (surveys only)","Matrix Group Name","Matrix Ranking?","Field Annotation"\n'

type Column = { choices?: string; validation?: string; branching?: string }

const quote = (value: string) => '"' + value.replaceAll('"', '""') + '"'

// One dictionary row: name, form, section, type, label, choices, note, validation, min, max,
// identifier, branching, then the six columns this spec leaves empty.
const row = (name: string, form: string, type: string, label: string, column: Column = {}) =>
  [name, form, "", type, quote(label), quote(column.choices ?? ""), "", column.validation ?? "", "", "", "",
    quote(column.branching ?? ""), "", "", "", "", "", ""].join(",") + "\n"

// The intake form: every expression reads fields of this same form.
const INTAKE =
  HEADER +
  row("pid", "intake", "text", "Participant ID") +
  row("age", "intake", "text", "Age", { validation: "integer" }) +
  row("sex", "intake", "radio", "Sex", { choices: "1, Male | 2, Female" }) +
  row("consent", "intake", "yesno", "Consent given") +
  row("complaint", "intake", "text", "Complaint") +
  row("adult_note", "intake", "text", "Adult note", { branching: "[age] >= 18" }) +
  row("guardian", "intake", "text", "Guardian", { branching: "[age] < 18 or [consent] = '0'" }) +
  row("preg", "intake", "yesno", "Pregnant", { branching: "[sex] = 'Female' and [age] >= 12" }) +
  row("why_not", "intake", "notes", "Why no consent?", { branching: "[consent] = 'No'" }) +
  row("pain_scale", "intake", "text", "Pain scale", { branching: "text_contains([complaint], 'pain')" })

// The follow-up form: every expression reads intake's fields, so its values come from the
// record as stored, not from an input on the page.
const FOLLOWUP =
  HEADER +
  row("fu_note", "followup", "text", "Follow-up note") +
  row("fu_pregnancy", "followup", "text", "Pregnancy follow-up", { branching: '[baseline_arm_1][sex] = "Female"' }) +
  row("fu_minor", "followup", "text", "Minor follow-up", { branching: "[baseline_arm_1][age] < 18" }) +
  row("fu_answered", "followup", "text", "Consent was answered", { branching: "[consent]" }) +
  row("fu_refused", "followup", "text", "Consent was refused", { branching: "[consent] = 'No'" })

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

const success = (page: Page) => page.locator(".alert-success").last()

/** A fresh project carrying both dictionaries at baseline, the admin a member with a token. */
async function setUpProject(page: Page, projectName: string): Promise<void> {
  await signInAsAdmin(page)
  await page.locator('header a[href="/admin"]').click()
  // The Control Panel opens on Projects (REQ-UI-047).
  await expect(page.locator('#clara-nav a[href="/admin?section=projects"]')).toHaveAttribute("aria-current", "page")
  await page.locator("details.clara-create summary").click()
  await page.fill("#project-project_name", projectName)
  await page.selectOption("#project-organization", "OUS")
  await page.fill("#project-pi_name", "Principal Investigator")
  await page.fill("#project-pi_email", "pi@example.org")
  await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
  await page.getByRole("link", { name: `Open ${projectName}` }).click()
  await expect(page).toHaveURL(/\/projects\/\d+\/setup$/)

  // The first field of the first file becomes the record identifier, in the empty first
  // instrument the new project carries (GD-8).
  await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
  await page.setInputFiles("#import-files", [
    { name: "a_intake.csv", mimeType: "text/csv", buffer: Buffer.from(INTAKE) },
    { name: "b_followup.csv", mimeType: "text/csv", buffer: Buffer.from(FOLLOWUP) },
  ])
  await page.getByRole("button", { name: "Preview" }).click()
  await expect(page.locator("[data-clara-import-problems]")).toHaveCount(0)
  await page.getByRole("button", { name: "Import", exact: true }).click()
  await expect(success(page)).toContainText("instruments created: 2")

  await page.locator("#clara-nav").getByRole("link", { name: "Setup" }).click()
  const matrix = page.locator('form.clara-mapping[data-arm="1"]')
  for (const name of ["instrument", "intake", "followup"]) {
    const pair = matrix.locator(`input[data-pair="${name}|baseline_arm_1"]`)
    if (!(await pair.isChecked())) await pair.check()
  }
  await matrix.locator("button[type=submit]").click()
  await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")

  await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
  const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
  await page.locator("#member-user").selectOption(adminOption ?? "")
  await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
}

/** Opens a new participant on the intake form, from the Record Status Dashboard. */
async function openNewOnIntake(page: Page, record: string): Promise<void> {
  await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
  await page.fill("#new-record-id", record)
  await page.getByRole("button", { name: "Create" }).click()
  await expect(page).toHaveURL(new RegExp(`/records/${record}`))
  await openInstrument(page, "intake")
}

async function openInstrument(page: Page, name: string): Promise<void> {
  await page.locator(".clara-instrument-nav").getByRole("link", { name: new RegExp(name) }).click()
  await expect(page.locator("form[data-clara-record-form]")).toHaveAttribute("data-instrument", name)
}

/** The same (event, instrument) view, over another record of the project. */
async function switchRecord(page: Page, record: string): Promise<void> {
  const url = new URL(page.url())
  url.pathname = url.pathname.replace(/\/records\/[^/]+$/, `/records/${record}`)
  const instrument = url.searchParams.get("instrument")
  await page.goto(url.toString())
  await expect(page.locator("form[data-clara-record-form]")).toHaveAttribute("data-instrument", instrument ?? "")
}

const form = (page: Page) => page.locator("form[data-clara-record-form]")
const field = (page: Page, name: string) => form(page).locator(`[data-clara-field="${name}"]`)
// A choice of one radio or yes/no field: "Yes" and "No" repeat across fields on one form.
const choice = (page: Page, name: string, label: string): Locator =>
  field(page, name).getByLabel(label, { exact: true })

async function expectShown(page: Page, shown: string[], hidden: string[]): Promise<void> {
  for (const name of shown) await expect(field(page, name), `${name} shown`).toBeVisible()
  for (const name of hidden) await expect(field(page, name), `${name} hidden`).toBeHidden()
}

async function save(page: Page, record: string): Promise<void> {
  await form(page).locator("[data-clara-save]").click()
  await expect(success(page)).toContainText(`Record ${record}`)
}

test.describe("field branching against one record's values (GD-13, REQ-VAL-029)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("on one form, fields follow the inputs they read, and the stored values decide on reopening", async ({ page }) => {
    test.setTimeout(120_000)
    await setUpProject(page, `Branching form ${Date.now()}`)
    await openNewOnIntake(page, "BR001")

    // Nothing entered: a missing value makes every comparison 0, so every governed field waits
    // hidden — the or included, since neither side holds (ASM-VAL-6).
    const governed = ["adult_note", "guardian", "preg", "why_not", "pain_scale"]
    await expectShown(page, ["age", "sex", "consent", "complaint"], governed)

    // A number compares as a number, re-evaluated on every keystroke (§7.4).
    await page.fill("#f-age", "17")
    await expectShown(page, ["guardian"], ["adult_note"])
    await page.fill("#f-age", "18")
    await expectShown(page, ["adult_note"], ["guardian"])

    // and: a label constant resolves to the radio's code (§7.3), and both sides must hold.
    await choice(page, "sex", "Female").check()
    await expectShown(page, ["preg"], [])
    await page.fill("#f-age", "11")
    await expectShown(page, ["guardian"], ["preg", "adult_note"])
    await page.fill("#f-age", "30")
    await expectShown(page, ["preg", "adult_note"], ["guardian"])
    await choice(page, "sex", "Male").check()
    await expectShown(page, [], ["preg"])
    await choice(page, "sex", "Female").check()
    await expectShown(page, ["preg"], [])

    // A yes/no answer by its code ('0') and by its label ('No'): or holds on either side.
    await choice(page, "consent", "No").check()
    await expectShown(page, ["why_not", "guardian"], [])
    await choice(page, "consent", "Yes").check()
    await expectShown(page, [], ["why_not", "guardian"])
    await choice(page, "consent", "No").check()

    // text_contains reads the text as it is typed.
    await page.fill("#f-complaint", "headache")
    await expectShown(page, [], ["pain_scale"])
    await page.fill("#f-complaint", "back pain")
    await expectShown(page, ["pain_scale"], [])

    await choice(page, "preg", "Yes").check()
    await page.fill("#f-why_not", "Prefers to decide later.")
    await save(page, "BR001")

    // Reopened, the stored values decide before anything is touched.
    await page.reload()
    await expect(page.locator("#f-age")).toHaveValue("30")
    await expectShown(page, ["adult_note", "guardian", "preg", "why_not", "pain_scale"], [])

    // Display-only (DEV-VAL-5): hiding a field neither clears nor drops its answer. Save while
    // Pregnant is hidden, reopen, and show it again — the stored Yes is still there.
    await choice(page, "sex", "Male").check()
    await expectShown(page, [], ["preg"])
    await save(page, "BR001")
    await page.reload()
    await expect(choice(page, "sex", "Male")).toBeChecked()
    await expectShown(page, [], ["preg"])
    await choice(page, "sex", "Female").check()
    await expectShown(page, ["preg"], [])
    await expect(choice(page, "preg", "Yes")).toBeChecked()
  })

  test("on another instrument, fields follow this record's stored values and no other record's", async ({ page }) => {
    test.setTimeout(150_000)
    await setUpProject(page, `Branching record ${Date.now()}`)

    // Three participants whose intake answers differ, each saved on its own form.
    const participants: { record: string; age?: string; sex?: string; consent?: string }[] = [
      { record: "BR101", age: "30", sex: "Female", consent: "No" },
      { record: "BR102", age: "15", sex: "Male", consent: "Yes" },
      { record: "BR103" },
    ]
    for (const p of participants) {
      await openNewOnIntake(page, p.record)
      if (p.age) await page.fill("#f-age", p.age)
      if (p.sex) await choice(page, "sex", p.sex).check()
      if (p.consent) await choice(page, "consent", p.consent).check()
      await page.fill("#f-complaint", "none")
      await save(page, p.record)
    }

    // BR103 answered none of the three: every follow-up field waits hidden (ASM-VAL-6).
    await openInstrument(page, "followup")
    await expectShown(page, ["fu_note"], ["fu_pregnancy", "fu_minor", "fu_answered", "fu_refused"])

    // BR101 — Female, 30, consent No. The label "Female" resolves against intake's choices,
    // and a bare yes/no reference is 1 for any answer, No ('0') included (§7.3).
    await switchRecord(page, "BR101")
    await expectShown(page, ["fu_note", "fu_pregnancy", "fu_answered", "fu_refused"], ["fu_minor"])

    // BR102 — Male, 15, consent Yes: the same page over another record reads that record.
    await switchRecord(page, "BR102")
    await expectShown(page, ["fu_note", "fu_minor", "fu_answered"], ["fu_pregnancy", "fu_refused"])
  })
})
