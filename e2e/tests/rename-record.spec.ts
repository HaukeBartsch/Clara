import { expect, test, type Page } from "@playwright/test"

// An imported instrument renamed with spaces and mixed case, end to end: a REDCap instrument export (.zip with
// instrument.csv + OriginID.txt) is imported into a fresh project, the record identifier moves
// back out of the empty first instrument into the imported one (REQ-UI-056), `instrument` goes
// empty and is removed, the surviving instrument keeps spaces and mixed case (§7.1), the
// acting user gets permissions, the instrument maps to baseline and a first record is entered on
// it — which works whatever the case of the instrument name: neither event nor instrument names
// carry a case rule (REQ-DB-011); lower-case belongs to field names (REQ-DB-013). This last save
// failed while RecordController::isName only accepted [a-z0-9_]. Driven as the bootstrap
// administrator of `e2e/dev-stack.sh`; every name is unique per run.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

const HEADER =
  '"Variable / Field Name","Form Name","Section Header","Field Type","Field Label","Choices, Calculations, OR Slider Labels","Field Note","Text Validation Type OR Show Slider Number","Text Validation Min","Text Validation Max",Identifier?,"Branching Logic (Show field only if...)","Required Field?","Custom Alignment","Question Number (surveys only)","Matrix Group Name","Matrix Ranking?","Field Annotation"\n'

const SERIES =
  HEADER +
  'series_uid,"Series Data",,text,"Series Instance UID",,,,,,,,,,,,,\n' +
  'series_title,"Series Data",,text,"Series title",,,,,,,,,,,,,\n' +
  'series_kind,"Series Data",,dropdown,"Modality","MR, Magnetic resonance | CT, Computed tomography",,,,,,,,,,,,\n'

const FORM = "Series Data" // the dictionary's one Form Name; renamed again below
const RENAMED = "Series Data_2026"

// A minimal ZIP writer: STORED entries only — an uncompressed instrument.csv keeps the fixture
// readable here, and archive/zip accepts stored entries like any other.
const crc32 = (() => {
  const table = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    table[n] = c
  }
  return (data: Buffer): number => {
    let c = ~0
    for (const byte of data) c = table[(c ^ byte) & 0xff] ^ (c >>> 8)
    return ~c >>> 0
  }
})()

const zipStore = (entries: [string, string][]): Buffer => {
  const dosDate = ((2026 - 1980) << 9) | (1 << 5) | 1 // 2026-01-01; the time of day stays 0
  const parts: Buffer[] = []
  const directory: Buffer[] = []
  let offset = 0
  for (const [name, text] of entries) {
    const data = Buffer.from(text, "utf8")
    const crc = crc32(data)
    const local = Buffer.alloc(30 + name.length)
    local.writeUInt32LE(0x04034b50, 0)
    local.writeUInt16LE(20, 4) // version needed to extract
    local.writeUInt16LE(dosDate, 12)
    local.writeUInt32LE(crc, 14)
    local.writeUInt32LE(data.length, 18)
    local.writeUInt32LE(data.length, 22) // stored: the two sizes are one
    local.writeUInt16LE(name.length, 26)
    local.write(name, 30, "utf8")
    parts.push(local, data)
    const central = Buffer.alloc(46 + name.length)
    central.writeUInt32LE(0x02014b50, 0)
    central.writeUInt16LE(20, 4) // version made by, version needed
    central.writeUInt16LE(20, 6)
    central.writeUInt16LE(dosDate, 14)
    central.writeUInt32LE(crc, 16)
    central.writeUInt32LE(data.length, 20)
    central.writeUInt32LE(data.length, 24)
    central.writeUInt16LE(name.length, 28)
    central.writeUInt32LE(offset, 42) // where this entry's local header stands
    central.write(name, 46, "utf8")
    directory.push(central)
    offset += local.length + data.length
  }
  const dir = Buffer.concat(directory)
  const end = Buffer.alloc(22)
  end.writeUInt32LE(0x06054b50, 0)
  end.writeUInt16LE(entries.length, 8)
  end.writeUInt16LE(entries.length, 10)
  end.writeUInt32LE(dir.length, 12)
  end.writeUInt32LE(offset, 16)
  return Buffer.concat([...parts, dir, end])
}

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

const success = (page: Page) => page.locator(".alert-success").last()
const fieldOrder = (page: Page) => page.locator("table.clara-fields tbody tr[data-field]")

test.describe("an imported instrument with spaces in its name (REQ-UI-053, REQ-UI-056)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("import a .zip, move the identifier back, drop `instrument`, rename, map and enter a record", async ({ page }) => {
    test.setTimeout(120_000)
    const projectName = `Rename ${Date.now()}`

    await signInAsAdmin(page)

    // --- a fresh project (§6.2): baseline exists, `instrument` is the empty first instrument ---
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

    // --- the import (REQ-UI-053): REDCap's instrument export as a .zip ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    const dictionary = zipStore([
      ["instrument.csv", SERIES],
      ["OriginID.txt", "E2E-REDCap"],
    ])
    await page.setInputFiles("#import-files", [{ name: "series_instrument.zip", mimeType: "application/zip", buffer: dictionary }])
    await page.getByRole("button", { name: "Preview" }).click()
    const imported = page.locator("[data-clara-import-files] li")
    await expect(imported).toHaveCount(1)
    // The OriginID.txt of REDCap's export names the exporting server next to the file.
    await expect(imported.first()).toContainText(/series_instrument\.zip\s*—\s*E2E-REDCap/)
    await expect(page.locator("[data-clara-import-identifier]")).toHaveText("series_uid moves into instrument and becomes the record identifier.")
    await page.getByRole("button", { name: "Import", exact: true }).click()
    await expect(success(page)).toContainText("instruments created: 1, fields: 3")
    const instruments = page.locator("table.clara-design-instruments tbody tr")
    await expect(instruments).toHaveCount(2)
    await expect(instruments.nth(1)).toContainText(FORM)

    // --- move the first instrument's first field back into the imported instrument (REQ-UI-056):
    //     no records exist, so GD-8 lets the identifier go (the move.spec refusal needs records) ---
    await instruments.getByRole("link", { name: "instrument", exact: true }).click()
    await expect(page.getByRole("button", { name: "Move field series_uid", exact: true })).toBeEnabled()
    await page.getByRole("button", { name: "Move field series_uid", exact: true }).click()
    const dialog = page.locator("#clara-move")
    await expect(dialog).toBeVisible()
    await dialog.locator("#move-instrument").selectOption({ label: FORM })
    await expect(dialog.locator("#move-before option")).toHaveText(["series_title — Series title", "series_kind — Modality", "(end of the instrument)"])
    await dialog.locator("#move-before").selectOption({ label: "series_title — Series title" }) // it stays the identifier
    await dialog.getByRole("button", { name: "Move", exact: true }).click()
    await expect(success(page)).toContainText('Field "series_uid" moved.')
    await expect(fieldOrder(page)).toHaveCount(3)
    expect(await fieldOrder(page).evaluateAll((rows) => rows.map((r) => r.getAttribute("data-field")))).toEqual(["series_uid", "series_title", "series_kind"])

    // --- remove the now empty first instrument (§7.1): its row's Remove button, then the modal ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    await page.locator(`tr[data-instrument="instrument"]`).getByRole("button", { name: "Remove" }).click()
    await expect(page.locator("#clara-confirm")).toBeVisible()
    await page.locator("#clara-confirm [data-clara-confirm-ok]").click()
    await expect(success(page)).toContainText('Instrument "instrument" removed.')
    // The imported instrument is the first instrument now — its first field holds GD-8.
    await expect(page.locator("table[data-instrument], tr[data-instrument]")).toHaveCount(1)

    // --- rename with spaces, mixed case and an underscore (§7.1, REQ-API-130) ---
    await page.locator(`tr[data-instrument="${FORM}"]`).getByRole("link", { name: "Edit" }).click()
    await page.fill("#instrument-edit-name", RENAMED)
    await page.locator("#instrument-edit button[type=submit]").click()
    await expect(success(page)).toContainText(`Instrument "${RENAMED}" updated.`)
    await expect(page.locator(`tr[data-instrument="${RENAMED}"]`)).toHaveCount(1)

    // --- permissions for the acting user (§8.6, REQ-AUTH-022): a member with no role holds full
    //     permissions; membership is also what issues the token data entry needs (REQ-API-102) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
    await expect(page.locator("#clara-new-token")).toBeVisible()

    // --- assign the renamed instrument to baseline (§6.2 block D) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Setup" }).click()
    const matrix = page.locator('form.clara-mapping[data-arm="1"]')
    const pair = matrix.locator(`input[data-pair="${RENAMED}|baseline_arm_1"]`)
    if (!(await pair.isChecked())) await pair.check()
    await matrix.locator("button[type=submit]").click()
    await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")

    // --- the first record (REQ-UI-052 manual naming): opened on the renamed instrument, filled
    //     and saved — the save posts the instrument name with spaces and stores it ---
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await page.fill("#new-record-id", "UP001")
    await page.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/records\/UP001/)
    await page.locator(`.clara-instrument-nav [data-clara-instrument="${RENAMED}"] a.nav-link`).click()

    const form = page.locator("form[data-clara-record-form]")
    // The identifier is shown, never edited (GD-8): the record's own name in a read-only input.
    await expect(form.locator("#f-series_uid")).toHaveValue("UP001")
    await expect(form.locator("#f-series_uid")).toHaveAttribute("readonly")
    await form.getByLabel("Series title").fill("T1-weighted")
    await page.selectOption("#f-series_kind", "MR")
    await form.locator("[data-clara-save]").click()

    // The record exists once its first save stores it (REQ-API-033) — whatever the instrument's
    // name is written in.
    await expect(success(page)).toContainText("UP001")
  })
})
