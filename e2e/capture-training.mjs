// Re-captures the pictures of the training plans 01-create-project, 02-data-collection-forms,
// 03-collect-data and 04-export-data against a FRESH e2e/dev-stack.sh (a second run fails: the
// project name already exists). Run: node e2e/capture-training.mjs
//
// One walk through one project, in the order the plans tell it: create the project, add a member,
// set up events and instruments, enter one participant, export. Plan 05 needs an audit trail with
// several authors, so it stays with capture-training-05.mjs on its own fresh stack.
//
// SHOTS_DIR=<dir> writes the PNGs somewhere else than web/assets/training/ — for looking at a
// draft before replacing the committed pictures. The plans number their steps to match the file
// names, so a step added to a plan means a picture added here and a renumbered list in plan.md,
// plan.nb.md and plan.nn.md.
import { chromium } from "playwright"
import { mkdirSync } from "node:fs"

const API = "http://127.0.0.1:8085"
const WEB = "http://127.0.0.1:8090"
const SERVICE_TOKEN = "clara-dev-481516-svc" // dev-stack.sh, development only
const ADMIN = { email: "admin@example.org", password: "clara-dev-481516-pw" }
const PLANS = ["01-create-project", "02-data-collection-forms", "03-collect-data", "04-export-data"]
const ROOT = process.env.SHOTS_DIR ?? new URL("../web/assets/training/", import.meta.url).pathname
const PROJECT = "Heart Study"
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

for (const plan of PLANS) mkdirSync(`${ROOT}/${plan}`, { recursive: true })

// --- seeding the accounts a member list needs ---------------------------------------------------
// Only accounts: no project, so plan 01's first picture still shows an empty start page. The API
// verifies an e-mail address it is told about, so these are created before the project.
async function api(method, path, body, userId = 1) {
  const response = await fetch(API + path, {
    method,
    headers: {
      "X-Internal-Service-Token": SERVICE_TOKEN,
      "X-Internal-User-Id": String(userId),
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  if (!response.ok) throw new Error(`${method} ${path}: ${response.status} ${text}`)
  return text === "" ? null : JSON.parse(text)
}

for (const [email, name] of [
  ["kari.nordmann@example.org", "Kari Nordmann"],
  ["ingrid.berg@example.org", "Ingrid Berg"],
  ["ola.hansen@example.org", "Ola Hansen"],
]) {
  await api("POST", "/api/v1/users", { email, display_name: name, password: "training-pw-2026-x" })
}

// --- the browser --------------------------------------------------------------------------------
const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 860 } })

const outline = async (locator, radius = "6px") => {
  await locator.evaluate((el, corners) => {
    el.style.outline = "3px solid #fd7e14"
    el.style.outlineOffset = "2px"
    el.style.borderRadius = corners
  }, radius)
}
const clearOutline = async (locator) => {
  await locator.evaluate((el) => {
    el.style.outline = ""
    el.style.outlineOffset = ""
    el.style.borderRadius = ""
  })
}
// A picture shows the whole page as a user sees it: the control of the step outlined, the pointer
// parked out of the way, no caret and no focus ring left in a field.
const shot = async (name) => {
  await page.mouse.move(0, 0)
  await page.evaluate(() => document.activeElement?.blur())
  await page.waitForTimeout(300)
  await page.screenshot({ path: `${ROOT}/${name}` })
}
// A row of a wide table cannot carry the outline itself: it follows the table's border model and its
// right edge can sit outside the frame. Those get a drawn box, clamped to what the picture shows.
const markBox = async (locator) => {
  await locator.evaluate((el) => {
    const box = el.getBoundingClientRect()
    const overlay = document.createElement("div")
    overlay.setAttribute("data-clara-shot-mark", "")
    overlay.style.cssText = "position:absolute;border:3px solid #fd7e14;border-radius:6px;pointer-events:none;z-index:2000;"
    overlay.style.left = `${box.left + window.scrollX}px`
    overlay.style.top = `${box.top + window.scrollY}px`
    overlay.style.width = `${Math.min(box.right, document.documentElement.clientWidth - 8) - box.left - 6}px`
    overlay.style.height = `${box.height - 6}px`
    document.body.append(overlay)
  })
}
const clearMarks = () => page.evaluate(() => document.querySelectorAll("[data-clara-shot-mark]").forEach((el) => el.remove()))

// Two ways of framing a step on a long page: scroll as little as possible so that the control is
// in view (a button, a row), or bring a tall block to the top when the whole of it should show.
const reveal = async (locator, margin = 40) => {
  await locator.evaluate((el, gap) => {
    const box = el.getBoundingClientRect()
    if (box.top < 0) window.scrollBy(0, box.top - 20)
    else if (box.bottom > window.innerHeight - gap) window.scrollBy(0, box.bottom - window.innerHeight + gap)
  }, margin)
  await page.waitForTimeout(150)
}
const revealAtTop = async (locator, gap = 24) => {
  await locator.evaluate((el, fromTop) => {
    window.scrollTo(0, window.scrollY + el.getBoundingClientRect().top - fromTop)
  }, gap)
  await page.waitForTimeout(150)
}

// Selects an option by the text it shows, so a re-run does not depend on the exact wording around it.
const selectContaining = async (selector, text) => {
  const value = await page.locator(selector).locator("option").evaluateAll((options, want) => {
    const hit = options.find((o) => o.textContent.includes(want))
    return hit === undefined ? null : hit.value
  }, text)
  if (value === null) throw new Error(`no option containing "${text}" in ${selector}`)
  await page.selectOption(selector, value)
}

await page.goto(`${WEB}/login`)
await page.getByRole("radio", { name: "Local", exact: true }).check()
await page.fill("#login-email", ADMIN.email)
await page.fill("#login-password", ADMIN.password)
await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
await page.locator('header a[href="/admin"]').waitFor()

// ============================================================================= 01 create project
// 1. The start page: the Control Panel button, and nothing in the project list yet.
await outline(page.locator('header a[href="/admin"]'))
await shot("01-create-project/01.png")
await clearOutline(page.locator('header a[href="/admin"]'))

// 2. Control Panel → Projects → New project, filled in.
await page.locator('header a[href="/admin"]').click()
await page.locator("details.clara-create summary").click()
const createForm = page.locator('form[action*="action=create_project"]')
await createForm.waitFor()
await page.fill("#project-project_name", PROJECT)
await page.selectOption("#project-organization", "OUS")
await page.fill("#project-pi_name", "Kari Nordmann")
await page.fill("#project-pi_email", "kari.nordmann@example.org")
await outline(createForm.locator('button[type="submit"]'))
await shot("01-create-project/02.png")
await clearOutline(createForm.locator('button[type="submit"]'))

// 3. Created: the link into the new project.
await createForm.locator('button[type="submit"]').click()
const openLink = page.getByRole("link", { name: `Open ${PROJECT}` })
await openLink.waitFor()
const projectId = Number((await page.url()).match(/projects\/(\d+)/)?.[1] ?? (await openLink.getAttribute("href")).match(/\d+/)[0])
await outline(openLink)
await shot("01-create-project/03.png")
await clearOutline(openLink)

// Roles and the member come next; the roles are created over the API because the picture of adding
// a member should show a role to pick.
const base = `/api/v1/projects/${projectId}`
await api("POST", `${base}/roles`, { name: "Study nurse", project_admin: false, grants: [],
  arms: { 1: { data: "view_edit", export: "export_de_identified" } } })
await api("POST", `${base}/roles`, { name: "Monitor", project_admin: false, grants: [],
  arms: { 1: { data: "read_only", export: "export_none" } } })

// 4. The project's Members section: an account and a role, then Add member.
await openLink.click()
await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
const addMember = page.locator('form[action*="action=add_member"]')
await addMember.waitFor()
await selectContaining("#member-user", "Kari Nordmann")
await selectContaining("#member-role", "Study nurse")
await outline(addMember.locator('button[type="submit"]'))
await shot("01-create-project/04.png")
await clearOutline(addMember.locator('button[type="submit"]'))
await addMember.locator('button[type="submit"]').click()
await page.locator(".clara-members tbody tr").first().waitFor()

// The administrator has to be a member too before the record pages open (§5.3) — added after the
// picture, which shows the account and role a plan step names.
await selectContaining("#member-user", ADMIN.email)
await addMember.locator('button[type="submit"]').click()
await page.locator(".clara-members tbody tr").nth(1).waitFor()

// ===================================================================== 02 data collection forms
const setupUrl = `${WEB}/projects/${projectId}/setup`
const designUrl = `${WEB}/projects/${projectId}/design`

// The record identifier is the first field of the earliest instrument that has fields (GD-8), so the
// instrument the project starts with gets its real shape before the pictures of building on it:
// `demographics`, with `record_id` on top. Over the API — naming and filling this one is not what any
// of the four plans walks a user through.
const [firstInstrument] = await api("GET", `${base}/instruments`)
await api("PUT", `${base}/instruments/${firstInstrument.id}`, { name: "demographics" })
await api("POST", `${base}/instruments/${firstInstrument.id}/fields/bulk`, {
  fields: [
    { field_name: "record_id", field_type: "text", field_label: "Participant ID" },
    { field_name: "age", field_type: "text", validation_type: "integer", field_label: "Age" },
    { field_name: "sex", field_type: "radio", choices: "1$Male##2$Female", field_label: "Sex" },
  ],
})

// 1. Setup: the second event, with its timepoint.
await page.goto(setupUrl)
const addEvent = page.locator("form.clara-add-event")
await addEvent.waitFor()
await page.fill("#event-name-1", "follow_up")
await page.fill("#event-period-1", "30")
await reveal(addEvent, 60)
await outline(addEvent)
await shot("02-data-collection-forms/01.png")
await clearOutline(addEvent)
await addEvent.locator('button[type="submit"]').click()
await page.locator("tr[data-event=follow_up_arm_1]").waitFor()

// 2. Design: the second instrument. (Instruments are made here, not on Setup — REQ-UI-067.)
await page.goto(designUrl)
const addInstrument = page.locator('form[action*="action=add_instrument"]')
await addInstrument.waitFor()
await page.fill("#instrument-name", "vitals")
await outline(addInstrument)
await shot("02-data-collection-forms/02.png")
await clearOutline(addInstrument)
await addInstrument.locator('button[type="submit"]').click()
await page.locator('tr[data-instrument=vitals]').waitFor()

// 3. Setup: which instrument is filled out at which event.
await page.goto(setupUrl)
const mapping = page.locator("form.clara-mapping")
await mapping.waitFor()
await mapping.locator('[data-pair="demographics|baseline_arm_1"]').check()
await mapping.locator('[data-pair="vitals|baseline_arm_1"]').check()
await mapping.locator('[data-pair="vitals|follow_up_arm_1"]').check()
await revealAtTop(mapping, 70)
await outline(mapping, "8px")
await shot("02-data-collection-forms/03.png")
await clearOutline(mapping)
await mapping.locator('button[type="submit"]').click()
await page.locator("form.clara-mapping").waitFor()

// 4. Design: the first instrument holds the record identifier as its first field.
const instruments = await api("GET", `${base}/instruments`)
const instrumentId = instruments[0].id
await page.goto(`${designUrl}/instruments/${instrumentId}`)
const fieldsTable = page.locator("table.clara-fields")
await fieldsTable.waitFor()
const identifierRow = fieldsTable.locator("tr[data-field=record_id]")
await markBox(identifierRow)
await shot("02-data-collection-forms/04.png")
await clearMarks()

// 5. Design: a field of one's own — name, label, type, and the choices of a radio group. The
//    instrument starts with its first field, `weight`, so that step 6 shows a list to order.
const vitalsId = instruments.find((instrument) => instrument.name === "vitals").id
await api("POST", `${base}/instruments/${vitalsId}/fields`, {
  field_name: "weight", field_type: "text", validation_type: "floating point",
  field_label: "Weight (kg)", required: true,
})
await page.goto(`${designUrl}/instruments/${vitalsId}?field=new#field-form`)
const fieldForm = page.locator("#field-form")
await fieldForm.waitFor()
await page.fill("#field-name", "smoker")
await page.fill("#field-label", "Smoker")
await page.selectOption("#field-type", "radio")
// The form always offers two empty choice rows (design_instrument.php); the Add choice button only
// appears once design.js has wired it, so filling the two rows is what a user without JavaScript sees.
const codes = page.locator('input[name="choice_code[]"]')
const labels = page.locator('input[name="choice_label[]"]')
await codes.nth(0).waitFor()
await codes.nth(0).fill("0")
await labels.nth(0).fill("No")
await codes.nth(1).fill("1")
await labels.nth(1).fill("Yes")
await revealAtTop(fieldForm, 20)
await outline(fieldForm, "8px")
await shot("02-data-collection-forms/05.png")
await clearOutline(fieldForm, "8px")
await fieldForm.locator('button[type="submit"]').click()
await page.locator("tr[data-field=smoker]").waitFor()

// 6. The field list of the instrument, in form order.
await reveal(fieldsTable, 140)
await markBox(fieldsTable)
await shot("02-data-collection-forms/06.png")
await clearMarks()

// ================================================================================ 03 collect data
const statusUrl = `${WEB}/projects/${projectId}/record-status`

// 1. The Record Status Dashboard: a new participant.
await page.goto(statusUrl)
const newRecord = page.locator("form.clara-new-record")
await newRecord.waitFor()
const typed = page.locator("#new-record-id")
const hasTyped = await typed.isVisible()
if (hasTyped) {
  await typed.fill("HEART001")
}
if (hasTyped) {
  await outline(typed, "4px")
}
await outline(newRecord.locator('button[type="submit"]'))
await shot("03-collect-data/01.png")
await clearOutline(typed)
await clearOutline(newRecord.locator('button[type="submit"]'))
await newRecord.locator('button[type="submit"]').click()
await page.locator("form.clara-record-form").waitFor()

// 2. The record: the vitals form at baseline, filled and set to finished. A field's input carries
//    its own id (#f-<field_name>), one per choice for a coded field.
const recordUrl = page.url()
await selectContaining("#record-event", "baseline").catch(() => {})
await page.locator("[data-clara-instrument=vitals] a").first().click()
await page.locator("#f-weight").waitFor()
await page.fill("#f-weight", "72.5")
await page.locator("#f-smoker-0").check()
await selectContaining("#record-completion", "Finished")
await outline(page.locator("[data-clara-save]"))
await shot("03-collect-data/02.png")
await clearOutline(page.locator("[data-clara-save]"))
await page.locator("[data-clara-save]").click()
await page.locator(".alert-success").first().waitFor()

// 3. The confirmation that the form was saved.
await shot("03-collect-data/03.png")

// 4. Back on the dashboard: one row per record, one dot per form at one event. Saving demographics
//    without finishing it gives the middle colour of the legend.
await page.goto(`${recordUrl.split("?")[0]}?event=baseline_arm_1`)
await page.locator("[data-clara-instrument=demographics] a").first().click()
await page.locator("form.clara-record-form").waitFor()
await page.locator("[data-clara-save]").click()
await page.locator(".alert-success").first().waitFor()
await page.goto(statusUrl)
const recordRow = page.locator("table tbody tr").first()
await recordRow.waitFor()
await markBox(recordRow)
await shot("03-collect-data/04.png")
await clearMarks()

// ================================================================================= 04 export data
const exportPageUrl = `${WEB}/projects/${projectId}/export`

// 1. Export in the project's left panel.
await page.goto(exportPageUrl)
await page.locator(".clara-export-form").waitFor()
await outline(page.locator('#clara-nav a[href$="/export"]'))
await shot("04-export-data/01.png")
await clearOutline(page.locator('#clara-nav a[href$="/export"]'))

// 2. The data file: format, value of the coded fields, headers, delimiter.
await selectContaining("#export-values", "Labels")
await outline(page.locator("[data-clara-export-download]"))
await shot("04-export-data/02.png")
await clearOutline(page.locator("[data-clara-export-download]"))

// 3. The CDISC ODM file, with its version. All three Export pictures keep the page at the top: the
//    whole form and both other download blocks fit in one frame.
const odm = page.locator(".clara-odm-export")
await odm.waitFor()
await outline(odm.locator("[data-clara-odm-download]"))
await shot("04-export-data/03.png")
await clearOutline(odm.locator("[data-clara-odm-download]"))

// 4. The data dictionary: design only, no record values.
const dictionary = page.locator(".clara-dictionary-export")
await dictionary.waitFor()
await outline(dictionary.locator("[data-clara-dictionary-download]"))
await shot("04-export-data/04.png")
await clearOutline(dictionary.locator("[data-clara-dictionary-download]"))

await browser.close()
console.log(`project ${PROJECT} #${projectId} → ${ROOT}`)
