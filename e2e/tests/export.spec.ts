import { expect, test, type APIRequestContext, type Browser, type Page } from "@playwright/test"

// M6 — the export action (User_Interface_Design.md §6.4, REQ-UI-020): the page states the
// sensitivity level before the download starts, the level follows the arm selection (the most
// protective of the selected arms, REQ-EXP-003), and the file streams through the PHP route
// (REQ-TECH-011) as an attachment in the chosen format — for CSV with the chosen delimiter,
// decimal separator and byte-order mark (REQ-EXP-013/015/016). Beside it, a second control
// downloads the data dictionary as a REDCap instrument ZIP, design only — the file the
// instrument import reads, which a fresh project here takes whole (REQ-API-153).
//
// The structure is seeded through the administration API with the development service token
// of e2e/dev-stack.sh — what this spec asserts is the page, not the designer. Every name is
// unique per run.

const API_URL = process.env.CLARA_API_URL ?? "http://127.0.0.1:8085"
const SERVICE_TOKEN = process.env.CLARA_SERVICE_TOKEN ?? "clara-dev-481516-svc" // dev-stack.sh, development only
const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

async function admin(request: APIRequestContext, method: string, path: string, body?: unknown): Promise<any> {
  const response = await request.fetch(`${API_URL}${path}`, {
    method,
    headers: { "X-Internal-Service-Token": SERVICE_TOKEN, "X-Internal-User-Id": "1", "Content-Type": "application/json" },
    data: body === undefined ? undefined : JSON.stringify(body),
  })
  expect(response.ok(), `${method} ${path}: ${response.status()} ${await response.text()}`).toBeTruthy()
  const text = await response.text()
  return text === "" ? null : JSON.parse(text)
}

async function signIn(page: Page, email: string, password: string): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", email)
  await page.fill("#login-password", password)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator("footer")).toContainText("Sign out")
}

async function newPage(browser: Browser): Promise<Page> {
  return (await browser.newContext()).newPage()
}

test.describe("export (M6)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("states the applied level, follows the arm selection, and streams the file", async ({ browser, request }) => {
    test.setTimeout(120_000)
    const tag = Date.now()
    const memberEmail = `exporter-${tag}@example.org`
    const memberPassword = `exporter-${tag}-pw`

    // --- two arms: the member may export arm 1 in full, arm 2 only de-identified ---
    const project = await admin(request, "POST", "/api/v1/projects",
      { project_name: `M6 export ${tag}`, organization: "Org", participant_names: "E[0-9][0-9][0-9]" })
    const base = `/api/v1/projects/${project.id}`
    await admin(request, "POST", `${base}/arms`, { name: "second" })
    await admin(request, "POST", `${base}/events`, { arm_num: 2, event_name: "visit" })
    const [instrument] = await admin(request, "GET", `${base}/instruments`)
    await admin(request, "POST", `${base}/instruments/${instrument.id}/fields/bulk`, { fields: [
      { field_name: "record_id", field_type: "text" },
      { field_name: "age", field_type: "text", validation_type: "integer", field_label: "Age" },
      { field_name: "sex", field_type: "radio", choices: "1$Male##2$Female, other", field_label: "Sex" },
      { field_name: "weight", field_type: "text", validation_type: "floating point", field_label: "Weight" },
    ] })
    await admin(request, "PUT", `${base}/instrument-event-mapping`, { arm_num: 1, mapping: { instrument: ["baseline_arm_1"] } })
    await admin(request, "PUT", `${base}/instrument-event-mapping`, { arm_num: 2, mapping: { instrument: ["visit_arm_2"] } })
    await admin(request, "POST", `${base}/roles`, { name: "exporter", project_admin: false, grants: [],
      arms: { "1": { data: "view_edit", export: "export_full" }, "2": { data: "view_edit", export: "export_de_identified" } } })
    const user = await admin(request, "POST", "/api/v1/users", { email: memberEmail, display_name: "Exporter", password: memberPassword })
    await admin(request, "PUT", `${base}/users/${user.id}`, { role: "exporter" })
    const { token } = await admin(request, "PUT", `${base}/users/1`, { role: null })
    const imported = await request.post(`${API_URL}/api/`, { form: {
      token, content: "record", format: "json",
      "data[0][record_id]": "E001", "data[0][form_name]": "instrument", "data[0][event_name]": "baseline_arm_1",
      "data[0][age]": "42", "data[0][sex]": "2", "data[0][weight]": "71.5",
      "data[1][record_id]": "E002", "data[1][form_name]": "instrument", "data[1][event_name]": "baseline_arm_1",
      "data[1][age]": "37", "data[1][sex]": "1",
    } })
    expect(JSON.stringify(await imported.json())).not.toContain('"import_record_id":0')

    // --- the administrator: full level on every arm; the files stream as attachments ---
    const page = await newPage(browser)
    await signIn(page, ADMIN_EMAIL, ADMIN_PASSWORD)
    await page.goto(`/projects/${project.id}/overview`)
    await page.locator("#clara-nav").getByRole("link", { name: "Export" }).click()
    await expect(page).toHaveURL(new RegExp(`/projects/${project.id}/export$`))
    const level = page.locator("[data-clara-export-level]")
    await expect(level).toHaveText("Full dataset")
    const csvOptions = page.locator("[data-clara-export-csv]")
    await expect(csvOptions).toHaveCount(3) // delimiter, decimal separator, byte-order mark
    for (const option of await csvOptions.all()) await expect(option).toBeVisible()

    await page.selectOption("#export-values", "label")
    const [csv] = await Promise.all([page.waitForEvent("download"), page.locator("[data-clara-export-download]").click()])
    expect(csv.suggestedFilename()).toMatch(/^M6_export_\d+_\d{4}-\d{2}-\d{2}\.csv$/)
    const csvText = await (await csv.createReadStream()).toArray().then((parts) => Buffer.concat(parts).toString("utf8"))
    const [header, ...lines] = csvText.trim().split(/\r?\n/)
    expect(header).toContain("record_id")
    expect(header).toContain("age")
    expect(lines.join("\n")).toContain("Female, other") // a label, not the code, and quoted intact
    expect(lines.join("\n")).toContain("42")
    expect(lines.join("\n")).toContain("71.5") // English defaults: comma, stored period, no mark
    expect(csvText.charCodeAt(0)).not.toBe(0xfeff)

    // The spreadsheet options: semicolon, decimal comma and the mark ahead of the first byte.
    await page.selectOption("#export-delimiter", "semicolon")
    await page.selectOption("#export-decimal", "comma")
    await page.check("#export-bom")
    const [sheet] = await Promise.all([page.waitForEvent("download"), page.locator("[data-clara-export-download]").click()])
    const sheetText = await (await sheet.createReadStream()).toArray().then((parts) => Buffer.concat(parts).toString("utf8"))
    expect(sheetText.charCodeAt(0)).toBe(0xfeff)
    const [sheetHeader, ...sheetLines] = sheetText.slice(1).trim().split(/\r?\n/)
    expect(sheetHeader.split(";")).toContain("record_id")
    expect(sheetLines.join("\n")).toContain(";71,5")

    await page.check("#export-format-json")
    for (const option of await csvOptions.all()) await expect(option).toBeHidden()
    const [json] = await Promise.all([page.waitForEvent("download"), page.locator("[data-clara-export-download]").click()])
    expect(json.suggestedFilename()).toMatch(/\.json$/)
    const rows = JSON.parse(await (await json.createReadStream()).toArray().then((parts) => Buffer.concat(parts).toString("utf8")))
    expect(rows.map((row: Record<string, string>) => row.record_id).sort()).toEqual(["E001", "E002"])

    // --- the second download: the data dictionary as a REDCap instrument ZIP (REQ-UI-020, REQ-API-153) ---
    const [dictionary] = await Promise.all([page.waitForEvent("download"), page.locator("[data-clara-dictionary-download]").click()])
    expect(dictionary.suggestedFilename()).toMatch(/^M6_export_\d+_\d{4}-\d{2}-\d{2}_\d{4}\.zip$/)
    const archive = await (await dictionary.createReadStream()).toArray().then((parts) => Buffer.concat(parts))
    expect(archive.subarray(0, 4).toString("binary")).toBe("PK\x03\x04")
    // The file the instrument import reads: a fresh project takes it whole — every designed
    // field, the record identifier first — and it carries no record (design only).
    const copy = await admin(request, "POST", "/api/v1/projects",
      { project_name: `M6 export copy ${tag}`, organization: "Org", participant_names: "C[0-9][0-9][0-9]" })
    const preview = await admin(request, "POST", `/api/v1/projects/${copy.id}/instruments/import`,
      { files: [{ name: dictionary.suggestedFilename(), content: archive.toString("base64") }], dry_run: true })
    expect(preview.importable).toBe(true)
    expect(preview.record_identifier).toEqual({ field: "record_id", instrument: "instrument", moved: true })
    const copied = preview.instruments.flatMap((i: { fields: Array<{ field_name: string }> }) => i.fields.map((f) => f.field_name))
    expect(copied).toHaveLength(4) // one row per designed field, not per record
    expect(copied).toContain("age")

    // --- the member: the most protective selected level is stated, and it follows the selection ---
    const member = await newPage(browser)
    await signIn(member, memberEmail, memberPassword)
    await member.goto(`/projects/${project.id}/export`)
    const memberLevel = member.locator("[data-clara-export-level]")
    await expect(memberLevel).toHaveText("De-identified")
    await member.uncheck("#export-arm-2")
    await expect(memberLevel).toHaveText("Full dataset")
    await member.uncheck("#export-arm-1")
    await expect(member.locator("[data-clara-export-no-arm]")).toBeVisible()
    await expect(member.locator("[data-clara-export-download]")).toBeDisabled()
    await member.check("#export-arm-2")
    await expect(memberLevel).toHaveText("De-identified")
    const [deidentified] = await Promise.all([member.waitForEvent("download"), member.locator("[data-clara-export-download]").click()])
    expect(deidentified.suggestedFilename()).toMatch(/\.csv$/)
  })
})
