// Seeds a FRESH e2e/dev-stack.sh with three projects of medium activity, then re-captures
// web/assets/training/05-export-audit-trails/0{1,2}.png (a second run fails on the existing
// users and projects). Run: node e2e/capture-training-05.mjs
import { chromium } from "playwright"

const API = "http://127.0.0.1:8085"
const WEB = "http://127.0.0.1:8090"
const SERVICE_TOKEN = "clara-dev-481516-svc" // dev-stack.sh, development only
const ADMIN = { email: "admin@example.org", password: "clara-dev-481516-pw" }
const OUT = new URL("../web/assets/training/05-export-audit-trails/", import.meta.url).pathname

// Deterministic "random" values, so a re-run produces the same data.
let seed = 20261013
const rnd = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648)
const pick = (list) => list[Math.floor(rnd() * list.length)]
const between = (lo, hi) => lo + Math.floor(rnd() * (hi - lo + 1))
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

async function admin(method, path, body, userId = 1) {
  const response = await fetch(API + path, {
    method,
    headers: { "X-Internal-Service-Token": SERVICE_TOKEN, "X-Internal-User-Id": String(userId), "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  if (!response.ok) throw new Error(`${method} ${path}: ${response.status} ${text}`)
  return text === "" ? null : JSON.parse(text)
}

async function importRecords(token, rows) {
  const form = new URLSearchParams({ token, content: "record", format: "json", data: JSON.stringify(rows) })
  const response = await fetch(API + "/api/", { method: "POST", body: form })
  const text = await response.text()
  if (!response.ok || text.includes('"error"') || text.includes('"import_record_id":0')) throw new Error(`import: ${response.status} ${text}`)
}

// --- staff accounts -----------------------------------------------------------------------
const staff = {}
for (const [key, email, name] of [
  ["kari", "kari.nordmann@example.org", "Kari Nordmann"],
  ["ola", "ola.hansen@example.org", "Ola Hansen"],
  ["ingrid", "ingrid.berg@example.org", "Ingrid Berg"],
  ["lars", "lars.johansen@example.org", "Lars Johansen"],
]) {
  const user = await admin("POST", "/api/v1/users", { email, display_name: name, password: `${key}-training-pw-2026` })
  staff[key] = { ...user, email, name, password: `${key}-training-pw-2026` }
}

// --- projects -----------------------------------------------------------------------------
const projects = [
  {
    name: "Heart Study", organization: "OUS", pi: staff.kari, prefix: "HEART",
    events: ["follow_up"],
    instruments: {
      demographics: [
        { field_name: "record_id", field_type: "text", field_label: "Participant ID" },
        { field_name: "age", field_type: "text", validation_type: "integer", field_label: "Age" },
        { field_name: "sex", field_type: "radio", choices: "1$Male##2$Female", field_label: "Sex" },
      ],
      vitals: [
        { field_name: "weight", field_type: "text", validation_type: "floating point", field_label: "Weight (kg)" },
        { field_name: "systolic", field_type: "text", validation_type: "integer", field_label: "Systolic blood pressure" },
        { field_name: "smoker", field_type: "radio", choices: "0$No##1$Yes", field_label: "Smoker" },
      ],
    },
    mapping: { demographics: ["baseline"], vitals: ["baseline", "follow_up"] },
    members: ["kari", "ingrid", "ola"], records: 12,
    row: () => ({ age: String(between(42, 81)), sex: pick(["1", "2"]) }),
    visit: () => ({ weight: (60 + rnd() * 40).toFixed(1), systolic: String(between(110, 165)), smoker: pick(["0", "0", "1"]) }),
  },
  {
    name: "Diabetes Registry", organization: "Ahus", pi: staff.ola, prefix: "DIAB",
    events: ["month_6", "month_12"],
    instruments: {
      enrolment: [
        { field_name: "record_id", field_type: "text", field_label: "Participant ID" },
        { field_name: "age", field_type: "text", validation_type: "integer", field_label: "Age" },
        { field_name: "diabetes_type", field_type: "radio", choices: "1$Type 1##2$Type 2", field_label: "Diabetes type" },
      ],
      lab: [
        { field_name: "hba1c", field_type: "text", validation_type: "floating point", field_label: "HbA1c (mmol/mol)" },
        { field_name: "bmi", field_type: "text", validation_type: "floating point", field_label: "BMI" },
      ],
    },
    mapping: { enrolment: ["baseline"], lab: ["baseline", "month_6", "month_12"] },
    members: ["ola", "lars"], records: 9,
    row: () => ({ age: String(between(19, 77)), diabetes_type: pick(["1", "2", "2"]) }),
    visit: () => ({ hba1c: (40 + rnd() * 35).toFixed(1), bmi: (21 + rnd() * 12).toFixed(1) }),
  },
  {
    name: "Stroke Rehabilitation", organization: "SUN", pi: staff.ingrid, prefix: "REHAB",
    events: ["discharge"],
    instruments: {
      admission: [
        { field_name: "record_id", field_type: "text", field_label: "Participant ID" },
        { field_name: "age", field_type: "text", validation_type: "integer", field_label: "Age" },
        { field_name: "nihss", field_type: "text", validation_type: "integer", field_label: "NIHSS score" },
      ],
      function_score: [
        { field_name: "barthel", field_type: "text", validation_type: "integer", field_label: "Barthel index" },
        { field_name: "notes", field_type: "notes", field_label: "Therapist notes" },
      ],
    },
    mapping: { admission: ["baseline"], function_score: ["baseline", "discharge"] },
    members: ["ingrid", "kari"], records: 6,
    row: () => ({ age: String(between(48, 88)), nihss: String(between(2, 18)) }),
    visit: () => ({ barthel: String(between(20, 95)), notes: pick(["Walks with support.", "Independent transfers.", "Needs help with stairs.", ""]) }),
  },
]

for (const spec of projects) {
  const project = await admin("POST", "/api/v1/projects", {
    project_name: spec.name, organization: spec.organization, pi_name: spec.pi.name,
    pi_email: spec.pi.email, participant_names: `${spec.prefix}[0-9][0-9][0-9]`, rek_number: `REK-2026/${between(100, 999)}`,
  })
  spec.id = project.id
  const base = `/api/v1/projects/${project.id}`
  for (const event of spec.events) await admin("POST", `${base}/events`, { arm_num: 1, event_name: event })

  // The project's first instrument becomes the first one of the spec; the rest are created.
  const names = Object.keys(spec.instruments)
  const [first] = await admin("GET", `${base}/instruments`)
  await admin("PUT", `${base}/instruments/${first.id}`, { name: names[0] })
  const ids = { [names[0]]: first.id }
  spec.ids = ids
  for (const name of names.slice(1)) ids[name] = (await admin("POST", `${base}/instruments`, { name })).id
  for (const name of names) await admin("POST", `${base}/instruments/${ids[name]}/fields/bulk`, { fields: spec.instruments[name] })

  const mapping = {}
  for (const [name, events] of Object.entries(spec.mapping)) mapping[name] = events.map((event) => `${event}_arm_1`)
  await admin("PUT", `${base}/instrument-event-mapping`, { arm_num: 1, mapping })

  await admin("POST", `${base}/roles`, { name: "Study nurse", project_admin: false, grants: [],
    arms: { "1": { data: "view_edit", export: "export_de_identified" } } })
  await admin("POST", `${base}/roles`, { name: "Monitor", project_admin: false, grants: [],
    arms: { "1": { data: "read_only", export: "export_none" } } })

  // Members: the PI administers the project, the others enter data as study nurses.
  spec.tokens = {}
  for (const key of spec.members) {
    const role = staff[key] === spec.pi ? null : "Study nurse"
    spec.tokens[key] = (await admin("PUT", `${base}/users/${staff[key].id}`, { role })).token
  }

  // Data entry: each record by one of the members, the follow-up visits later, some corrected.
  const [firstForm, visitForm] = names
  const visitEvents = spec.mapping[visitForm]
  for (let n = 1; n <= spec.records; n++) {
    const id = `${spec.prefix}${String(n).padStart(3, "0")}`
    const who = spec.tokens[pick(spec.members)]
    await importRecords(who, [
      { record_id: id, form_name: firstForm, event_name: "baseline_arm_1", ...spec.row() },
      { record_id: id, form_name: visitForm, event_name: "baseline_arm_1", ...spec.visit() },
    ])
    await sleep(120)
  }
  for (let n = 1; n <= spec.records; n++) {
    if (rnd() < 0.4) continue
    const id = `${spec.prefix}${String(n).padStart(3, "0")}`
    for (const event of visitEvents.slice(1)) {
      if (rnd() < 0.3) break
      await importRecords(spec.tokens[pick(spec.members)], [{ record_id: id, form_name: visitForm, event_name: `${event}_arm_1`, ...spec.visit() }])
    }
  }
  for (let k = 0; k < 3; k++) {
    const id = `${spec.prefix}${String(between(1, spec.records)).padStart(3, "0")}`
    await importRecords(spec.tokens[spec.members[0]], [{ record_id: id, form_name: visitForm, event_name: "baseline_arm_1", ...spec.visit() }])
  }
  await sleep(400)
}

// --- a varied, spaced-out tail for Heart Study, the project the pictures filter on ---------------
{
  const heart = projects[0]
  const base = `/api/v1/projects/${heart.id}`
  const kari = staff.kari.id
  const step = async (action) => { await action(); await sleep(1500) }
  await step(() => admin("POST", `${base}/instruments/${heart.ids.vitals}/fields`,
    { field_name: "diastolic", field_type: "text", validation_type: "integer", field_label: "Diastolic blood pressure" }, kari))
  await step(() => importRecords(heart.tokens.ingrid, [
    { record_id: "HEART013", form_name: "demographics", event_name: "baseline_arm_1", age: "58", sex: "2" },
  ]))
  await step(() => admin("PUT", `${base}/records/HEART013/events/baseline_arm_1/instruments/${heart.ids.demographics}/completion`,
    { state: "finished" }, kari))
  await step(() => admin("PUT", `${base}/users/${staff.ola.id}`, { role: "Monitor" }))
  await step(() => importRecords(heart.tokens.ingrid, [
    { record_id: "HEART004", form_name: "vitals", event_name: "follow_up_arm_1", weight: "81.2", systolic: "138", diastolic: "86" },
  ]))
  await step(() => admin("PUT", base, { rek_number: "REK-2026/418", start_date: "2026-09-01", end_date: "2028-08-31" }, kari))
  await step(() => admin("PUT", `${base}/records/HEART004/events/follow_up_arm_1/instruments/${heart.ids.vitals}/completion`,
    { state: "finished" }, kari))
}

// --- the web side: staff sign in and export -------------------------------------------------
const browser = await chromium.launch()

async function signIn(context, who) {
  const page = await context.newPage()
  await page.goto(WEB + "/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", who.email)
  await page.fill("#login-password", who.password)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await page.locator("#clara-account-menu").waitFor()
  return page
}

for (const [key, project] of [["kari", projects[0]], ["ola", projects[1]], ["ingrid", projects[2]]]) {
  const context = await browser.newContext()
  const page = await signIn(context, staff[key])
  await page.goto(`${WEB}/projects/${project.id}/export`)
  const [download] = await Promise.all([page.waitForEvent("download"), page.locator("[data-clara-export-download]").click()])
  await download.path()
  await context.close()
  await sleep(300)
}

// --- the pictures ---------------------------------------------------------------------------
const context = await browser.newContext({ viewport: { width: 1280, height: 860 } })
const page = await signIn(context, ADMIN)
const outline = (locator) => locator.evaluate((el) => {
  el.style.outline = "3px solid #fd7e14"
  el.style.outlineOffset = "2px"
  el.style.borderRadius = "6px"
})
const clear = (locator) => locator.evaluate((el) => { el.style.outline = ""; el.style.outlineOffset = "" })
const shot = async (name) => {
  await page.mouse.move(0, 0)
  await page.evaluate(() => document.activeElement?.blur())
  await page.waitForTimeout(300)
  await page.screenshot({ path: OUT + name })
}

await page.locator('header a[href="/admin"]').click()
await page.locator('#clara-nav a[href="/admin?section=audits"]').click()
const filters = page.locator("form.clara-audit-filters")
await filters.waitFor()
await filters.locator("select[name=project]").selectOption({ label: "Heart Study" })
await filters.getByRole("button", { name: "Filter" }).click()
await page.locator("form.clara-audit-filters").waitFor()

// 1. Audits in the left panel and the project filter.
const navAudits = page.locator('#clara-nav a[href="/admin?section=audits"]')
const projectFilter = page.locator("form.clara-audit-filters select[name=project]")
await outline(navAudits)
await outline(projectFilter)
await shot("01.png")
await clear(navAudits)
await clear(projectFilter)

// 2. Export CSV.
const exportButton = page.locator('form.clara-audit-filters button[name="download"]')
await outline(exportButton)
await shot("02.png")

await browser.close()
console.log("projects", projects.map((p) => `${p.name}#${p.id}`).join(", "))
