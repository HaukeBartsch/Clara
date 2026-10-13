// Screenshot capture for the "Intro to CLARA" presentation.
//
//   ./e2e/dev-stack.sh                                    # fresh stack (or alternative ports, below)
//   CLARA_WEB_URL=http://127.0.0.1:8190 \
//   CLARA_API_URL=http://127.0.0.1:8185 \
//     node docs/intro-to-Clara/tools/capture_screenshots.mjs
//
// The script signs in as the dev-stack bootstrap administrator, seeds two demo projects — a FIONA
// data-transfer registry built from FIONA's real dictionaries (IncomingStudies, Series, Transfers)
// and a pr2mask research project — fills representative records through the REDCap-compatible data
// API (the way FIONA writes), and captures every page at iPad landscape resolution (1024×768 @2x)
// into docs/intro-to-Clara/screenshots/.
//
// Two deliberate deviations from the stock assets, both on purpose:
//
// 1. dictionaries/incoming_studies.csv renames the first field study_instance_uid → record_id and
//    keeps its label "Study Instance UID". Record import takes `record_id` as the row key but
//    rejects it as UNKNOWN_FIELD whenever the dictionary names the identifier differently, which
//    also breaks web data entry for such projects (web/app/Controllers/RecordController.php posts
//    record_id). That is a real bug worth fixing at the API level; until then the demo uses the
//    REDCap-conventional name so the standard flows work.
// 2. The completed transfer's read-only bookkeeping fields go in through the data API — no form
//    can write them; FIONA fills them the same way in production.

import fs from "node:fs"
import path from "node:path"
import { createRequire } from "node:module"
import { fileURLToPath } from "node:url"

const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(here, "..", "..", "..")
const require = createRequire(path.join(repoRoot, "e2e", "package.json"))
const { chromium } = require("@playwright/test")

const WEB = process.env.CLARA_WEB_URL ?? "http://127.0.0.1:8190"
const API = process.env.CLARA_API_URL ?? "http://127.0.0.1:8185"
const OUT = path.resolve(here, "..", "screenshots")
const DICT = path.join(here, "dictionaries")
const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh development-only value

const registryName = "FIONA Data Transfer Registry"
const pr2maskName = "PR2MASK — Pancreatic Cancer Imaging"

const studies = [
  {
    uid: "1.3.12.2.1107.5.1.4.89822.300000261005081247001",
    institution: "Haukeland University Hospital",
    senderAet: "PACS01_HBH",
    addressedAet: "FIONA_INCOMING",
    patientName: "Demo, Patient One",
    patientId: "DEMO-2026-0417",
    description: "CT Thorax Abdomen Pelves w/contrast",
    date: "2026-10-05",
    time: "08:12:47",
    accession: "HBO-2026-088134",
    arrival: "2026-10-06",
  },
  {
    uid: "1.3.12.2.1107.5.1.4.89822.300000261006094512002",
    institution: "St. Olavs Hospital",
    senderAet: "PACS_STOLAVS",
    addressedAet: "FIONA_INCOMING",
    patientName: "Demo, Patient Two",
    patientId: "DEMO-2026-0512",
    description: "MR Abdomen Pancreas protocol",
    date: "2026-10-06",
    time: "09:45:12",
    accession: "STO-2026-041877",
    arrival: "2026-10-06",
  },
  {
    uid: "1.3.12.2.1107.5.1.4.89822.300000261007140233003",
    institution: "Haukeland University Hospital",
    senderAet: "PACS02_HBH",
    addressedAet: "FIONA_INCOMING",
    patientName: "Demo, Patient Three",
    patientId: "DEMO-2026-0533",
    description: "CT Chest low dose",
    date: "2026-10-07",
    time: "14:02:33",
    accession: "HBO-2026-088902",
    arrival: "2026-10-07",
  },
]

fs.mkdirSync(OUT, { recursive: true })

const failures = []
const step = async (label, fn) => {
  try {
    await fn()
    console.log(`ok    ${label}`)
  } catch (error) {
    failures.push(`${label}: ${String(error).split("\n")[0]}`)
    console.log(`FAIL  ${label} — ${String(error).split("\n")[0]}`)
  }
}

const browser = await chromium.launch()
const context = await browser.newContext({ viewport: { width: 1024, height: 768 }, deviceScaleFactor: 2 })
context.on("download", (d) => d.cancel().catch(() => {}))
const page = await context.newPage()

const settle = async () => {
  await page.waitForLoadState("networkidle").catch(() => {})
  await page.waitForTimeout(400)
}

const shot = async (name) => {
  await settle()
  await page.screenshot({ path: path.join(OUT, `${name}.png`) })
  console.log(`shot  ${name}.png`)
}

// --- sign in ---------------------------------------------------------------------

await step("login page", async () => {
  await page.goto(`${WEB}/login`)
  await page.locator("form[data-clara-source-picker]").waitFor()
  await shot("01-login")
})

await page.goto(`${WEB}/login`)
await page.getByRole("radio", { name: "Local", exact: true }).check()
await page.fill("#login-email", ADMIN_EMAIL)
await page.fill("#login-password", ADMIN_PASSWORD)
await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
await page.locator('header a[href="/admin"]').waitFor()

// --- projects --------------------------------------------------------------------

const createProject = async (name, { organization, piName, piEmail }) => {
  await page.goto(`${WEB}/admin?section=projects`)
  await settle()
  await page.locator("details.clara-create summary").click()
  await page.fill("#project-project_name", name)
  if (organization) await page.selectOption("#project-organization", organization)
  if (piName) await page.fill("#project-pi_name", piName)
  if (piEmail) await page.fill("#project-pi_email", piEmail)
  await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
  await page.getByRole("link", { name: `Open ${name}` }).click()
  await page.waitForURL(/\/projects\/\d+/, { timeout: 15_000 })
  return /\/projects\/(\d+)/.exec(page.url())?.[1]
}

const importDictionaries = async (files) => {
  await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
  await settle()
  await page.setInputFiles("#import-files", files)
  await page.getByRole("button", { name: "Preview" }).click()
  await settle()
  await page.getByRole("button", { name: "Import", exact: true }).click()
  await page.locator(".alert-success").last().waitFor({ timeout: 30_000 })
}

const mapAllInstruments = async (instrumentNames) => {
  await page.locator("#clara-nav").getByRole("link", { name: "Setup" }).click()
  await settle()
  const matrix = page.locator('form.clara-mapping[data-arm="1"]')
  for (const name of ["instrument", ...instrumentNames]) {
    const pair = matrix.locator(`input[data-pair="${name}|baseline_arm_1"]`)
    if ((await pair.count()) > 0 && !(await pair.isChecked())) await pair.check()
  }
  await matrix.locator("button[type=submit]").click()
  await page.locator(".alert-success").last().waitFor({ timeout: 15_000 })
}

const addSelfAsMember = async () => {
  await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
  await settle()
  const option = page.locator("#member-user option", { hasText: ADMIN_EMAIL })
  if ((await option.count()) === 0) return // already a member
  await page.locator("#member-user").selectOption((await option.first().getAttribute("value")) ?? "")
  await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
}

let registryId = null
let pr2maskId = null

await step(`create project ${registryName}`, async () => {
  registryId = await createProject(registryName, {
    organization: "OUS",
    piName: "FIONA System Administrator",
    piEmail: "fiona@example.org",
  })
})

await step("import FIONA dictionaries", () =>
  importDictionaries([
    path.join(DICT, "incoming_studies.csv"),
    path.join(DICT, "series.csv"),
    path.join(DICT, "transfers.csv"),
  ]),
)

await step("map FIONA instruments", () => mapAllInstruments(["incoming_studies", "series", "transfers"]))
await step("add admin member (registry)", addSelfAsMember)

await step(`create project ${pr2maskName}`, async () => {
  pr2maskId = await createProject(pr2maskName, {
    organization: "OUS",
    piName: "Principal Investigator",
    piEmail: "pi@example.org",
  })
})

await step("import pr2mask dictionary", () =>
  importDictionaries([path.join(repoRoot, "assets", "Example_data_dictionary_Pr2mask_instrument.zip")]),
)

await step("map pr2mask instruments", async () => {
  await page.locator("#clara-nav").getByRole("link", { name: "Setup" }).click()
  await settle()
  const matrix = page.locator('form.clara-mapping[data-arm="1"]')
  const inputs = matrix.locator("input[data-pair]")
  const count = await inputs.count()
  for (let i = 0; i < count; i++) {
    const input = inputs.nth(i)
    if (!(await input.isChecked())) await input.check()
  }
  await matrix.locator("button[type=submit]").click()
  await page.locator(".alert-success").last().waitFor({ timeout: 15_000 })
})
await step("add admin member (pr2mask)", addSelfAsMember)

// --- records -----------------------------------------------------------------------

const createRecords = async (projectId, ids) => {
  await page.goto(`${WEB}/projects/${projectId}/record-status`)
  await settle()
  for (const id of ids) {
    const existing = page.locator(`a[href="/projects/${projectId}/records/${id}"]`)
    if ((await existing.count()) > 0) continue
    await page.fill("#new-record-id", id)
    await page.getByRole("button", { name: "Create" }).click({ timeout: 15_000 })
    await page.waitForURL(/\/records\//, { timeout: 15_000 })
    await page.goBack()
    await settle()
  }
}

await step("create study records", () => createRecords(registryId, studies.map((s) => s.uid)))
await step("create pr2mask records", () => createRecords(pr2maskId, ["IMP001", "IMP002", "IMP003"]))

// A project token, confirmed through the Bootstrap modal and verified before use — a second
// rotation riding on the same click can invalidate the reveal. Plain node fetch: the API answers
// Playwright's request context with 401 for reasons of its own.
let token = null
await step("issue registry token", async () => {
  await page.goto(`${WEB}/projects/${registryId}/members`)
  for (let attempt = 0; attempt < 4 && !token; attempt++) {
    await settle()
    if ((await page.locator("#clara-new-token").count()) === 0) {
      await page.locator('form[action*="rotate_token"] button[type="submit"]').first().click()
      await page.locator("[data-clara-confirm-ok]").click()
      await settle()
    }
    // The reveal arrives masked; Show (audited) puts the value on the page.
    if ((await page.locator("[data-clara-token-show]").count()) > 0) {
      await page.locator("[data-clara-token-show]").click()
      await settle()
    }
    const candidate = await page.locator("#clara-new-token").inputValue({ timeout: 10_000 })
    const check = await fetch(`${API}/api/`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ token: candidate, content: "project" }),
    })
    if (check.ok) token = candidate
    else console.log(`      token ${candidate.slice(0, 6)}… rejected, retrying`)
  }
  if (!token) throw new Error("no valid project token")
})

await step("fill records through the data API", async () => {
  if (!token) throw new Error("no token")
  const eventResponse = await fetch(`${API}/api/`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ token, content: "event", format: "json" }),
  })
  const events = await eventResponse.json()
  const event = events[0]?.unique_event_name ?? "baseline_arm_1"
  const rows = studies.map((s, index) => ({
    record_id: s.uid,
    form_name: "incoming_studies",
    event_name: event,
    study_institution_name: s.institution,
    study_aetitle_sender: s.senderAet,
    study_aetitle_addressed: s.addressedAet,
    study_patient_name: s.patientName,
    study_patient_id: s.patientId,
    study_description: s.description,
    study_date: s.date,
    study_time: s.time,
    study_accession_number: s.accession,
    // datetime_ymd is stored as "Y-m-d H:i" — FIONA writes the arrival time too.
    study_arrival_date: `${s.arrival} 08:3${index}`,
  }))
  rows.push({
    record_id: studies[0].uid,
    form_name: "series",
    event_name: event,
    series_instance_uid: "1.3.12.2.1107.5.1.4.89822.300000261005081302001",
    series_description: "Arterial phase 1.0 mm",
    series_date: "2026-10-05",
    series_time: "08:13:02",
    series_num_files: "486",
    series_sequence_name: "C+ portal_1",
    series_classify_types: "CT_ABDOMEN_ARTERIAL",
  })
  // Study 1: a transfer FIONA completed — read-only bookkeeping included.
  rows.push({
    record_id: studies[0].uid,
    form_name: "transfers",
    event_name: event,
    transfer_requested_date: "2026-10-06 09:15",
    transfer_project_name: pr2maskName,
    transfer_name: "IMP001",
    transfer_event_name: event,
    transfer_date: "2026-10-06 09:24",
    transfer_mapped_uid: "1.2.826.0.1.3680043.9.7111.20261006.1",
    transfer_active_proc_time: "41",
  })
  // Study 2: requested but not yet run — pending, on purpose.
  rows.push({
    record_id: studies[1].uid,
    form_name: "transfers",
    event_name: event,
    transfer_requested_date: "2026-10-07 13:40",
    transfer_project_name: pr2maskName,
    transfer_name: "IMP002",
    transfer_event_name: event,
  })
  const response = await fetch(`${API}/api/`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      token,
      content: "record",
      format: "json",
      type: "csv",
      overwriteBehavior: "overwrite",
      data: JSON.stringify(rows),
    }),
  })
  const body = await response.text()
  console.log(`      import → ${response.status} ${body.slice(0, 200)}`)
})

// --- page screenshots ----------------------------------------------------------------

const openProjectLink = async (name) => {
  await page.goto(`${WEB}/`)
  await settle()
  await page.getByRole("link", { name: new RegExp(name.replace(/[—-]/g, "[-—]")) }).first().click()
  await page.waitForURL(/\/projects\/\d+/)
}

await step("dashboard", async () => {
  await page.goto(`${WEB}/`)
  await shot("02-dashboard")
})

await step("project overview", async () => {
  // The dashboard link opens the project's first permitted section (Setup for an admin); the
  // overview has a route of its own.
  await openProjectLink(registryName)
  await page.goto(`${WEB}/projects/${registryId}/overview`)
  await shot("03-project-overview")
})

await step("setup", async () => {
  await page.locator("#clara-nav").getByRole("link", { name: "Setup" }).click()
  await shot("04-setup")
})

await step("design", async () => {
  await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
  await shot("05-design")
})

await step("record status dashboard", async () => {
  await page.goto(`${WEB}/projects/${registryId}/record-status`)
  await shot("06-record-status")
})

const openRecord = async (projectId, id, instrument) => {
  await page.goto(`${WEB}/projects/${projectId}/records/${id}?event=baseline_arm_1&instrument=${instrument}`)
}

await step("incoming study form", async () => {
  await openRecord(registryId, studies[0].uid, "incoming_studies")
  await shot("07-record-incoming")
})

await step("series form", async () => {
  await page.locator(".clara-instrument-nav").getByRole("link", { name: /series/i }).click()
  await shot("08-record-series")
})

await step("transfers completed", async () => {
  await page.locator(".clara-instrument-nav").getByRole("link", { name: /transfers/i }).click()
  await shot("09-record-transfers-done")
})

await step("transfers pending", async () => {
  await openRecord(registryId, studies[1].uid, "transfers")
  await shot("10-record-transfers-pending")
})

await step("export", async () => {
  await page.goto(`${WEB}/projects/${registryId}/export`)
  await shot("11-export")
})

await step("members", async () => {
  await page.goto(`${WEB}/projects/${registryId}/members`)
  await shot("12-members")
})

await step("pr2mask record form", async () => {
  await openRecord(pr2maskId, "IMP001", "pr2mask")
  await shot("13-pr2mask-record")
})

await step("audit log", async () => {
  await page.goto(`${WEB}/admin?section=audits`)
  await shot("14-audit")
})

await step("api documentation", async () => {
  await page.goto(`${WEB}/admin?section=api`)
  await shot("15-api-docs")
})

// --- authentication and authorization slide -------------------------------------------

await step("roles (permission matrix)", async () => {
  await page.goto(`${WEB}/projects/${registryId}/roles`)
  await settle()
  await page.fill("#role-name", "data-entry")
  await page.locator("[data-clara-roles-form]").scrollIntoViewIfNeeded()
  await shot("16-roles")
})

await step("users (control panel)", async () => {
  await page.goto(`${WEB}/admin?section=users`)
  await shot("17-users")
})

await step("two-factor authentication", async () => {
  await page.goto(`${WEB}/account/two-factor`)
  await shot("18-two-factor")
})

await step("training", async () => {
  await page.goto(`${WEB}/training`)
  await shot("19-training")
})

await step("login credentials form", async () => {
  // A signed-out page: the source picker with "Local" chosen shows the credential form.
  const anonymous = await browser.newContext({ viewport: { width: 1024, height: 768 }, deviceScaleFactor: 2 })
  const signedOut = await anonymous.newPage()
  await signedOut.goto(`${WEB}/login`)
  await signedOut.getByRole("radio", { name: "Local", exact: true }).check()
  await signedOut.locator("#login-email").waitFor()
  await signedOut.waitForTimeout(400)
  await signedOut.screenshot({ path: path.join(OUT, "20-login-local.png") })
  console.log("shot  20-login-local.png")
  await anonymous.close()
})

await browser.close()

console.log(`\n${failures.length === 0 ? "all steps ok" : `${failures.length} failed step(s):`}`)
for (const f of failures) console.log(` - ${f}`)
