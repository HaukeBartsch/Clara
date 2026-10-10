import { expect, test, type APIRequestContext, type Page } from "@playwright/test"

// M6 — the responsive pass (Plan/Web_Implementation.md §6; REQ-UI-032, AGENTS.md: "keep tables
// responsive on tablets and phones"). Every page type renders at phone and tablet width
// without the page itself scrolling sideways: wide content — a table with many columns —
// scrolls inside its own .table-responsive wrapper instead, and the left panel collapses to
// the off-canvas pattern below `lg` (User_Interface_Design.md §2.4).

const API_URL = process.env.CLARA_API_URL ?? "http://127.0.0.1:8085"
const SERVICE_TOKEN = process.env.CLARA_SERVICE_TOKEN ?? "clara-dev-481516-svc" // dev-stack.sh, development only
const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

const VIEWPORTS = [
  { name: "phone", width: 390, height: 844 },
  { name: "tablet", width: 768, height: 1024 },
]

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

/** The page overflows sideways when its scrollable width exceeds the viewport. */
async function overflow(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
}

test.describe("responsive pass (M6)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("no page scrolls sideways at phone or tablet width", async ({ browser, request }) => {
    test.setTimeout(180_000)
    const tag = Date.now()

    // A project with enough structure that every page has content: two events, a survey
    // instrument, fields of every input kind, a record with values.
    const project = await admin(request, "POST", "/api/v1/projects",
      { project_name: `M6 responsive ${tag} with a rather long project name`, organization: "Org", participant_names: "R[0-9][0-9][0-9]" })
    const base = `/api/v1/projects/${project.id}`
    await admin(request, "POST", `${base}/events`, { arm_num: 1, event_name: "follow_up_visit_after_six_months", period: 180 })
    const [first] = await admin(request, "GET", `${base}/instruments`)
    const survey = await admin(request, "POST", `${base}/instruments`, { name: "patient_reported_outcome" })
    await admin(request, "PUT", `${base}/instruments/${survey.id}`, { is_survey: true })
    await admin(request, "PUT", `${base}/instrument-event-mapping`, { arm_num: 1, mapping: {
      instrument: ["baseline_arm_1", "follow_up_visit_after_six_months_arm_1"],
      patient_reported_outcome: ["baseline_arm_1", "follow_up_visit_after_six_months_arm_1"] } })
    await admin(request, "POST", `${base}/instruments/${first.id}/fields/bulk`, { fields: [
      { field_name: "record_id", field_type: "text" },
      { field_name: "a_rather_long_field_name_for_layout", field_type: "text", field_label: "A long label that wraps across the panel on small screens", required: true },
      { field_name: "smoker", field_type: "radio", choices: "1$Never##2$Former smoker##3$Current smoker##4$Unknown", field_label: "Smoking" },
      { field_name: "q1", field_type: "matrix", matrix_group: "qol", choices: "1$Not at all##2$A little##3$Quite a bit##4$Very much", field_label: "Pain" },
      { field_name: "q2", field_type: "matrix", matrix_group: "qol", choices: "1$Not at all##2$A little##3$Quite a bit##4$Very much", field_label: "Fatigue" },
    ] })
    await admin(request, "POST", `${base}/instruments/${survey.id}/fields`, { field_name: "score", field_type: "text", validation_type: "integer" })
    const { token } = await admin(request, "PUT", `${base}/users/1`, { role: null })
    await request.post(`${API_URL}/api/`, { form: { token, content: "record", format: "json",
      "data[0][record_id]": "R001", "data[0][form_name]": "instrument", "data[0][event_name]": "baseline_arm_1",
      "data[0][a_rather_long_field_name_for_layout]": "x", "data[0][smoker]": "2" } })
    // Issuing is a POST; GET only reports the state (§4.17), so the layout fixture asks for one.
    const link = (await admin(request, "POST",
      `${base}/records/R001/instruments/${survey.id}/survey-link?event=baseline_arm_1`)).url as string

    const pages = [
      "/", "/account/two-factor", "/account/password",
      "/admin?section=users", "/admin?section=projects", "/admin?section=audits",
      "/admin?section=translations", "/admin?section=settings",
      `/projects/${project.id}/overview`, `/projects/${project.id}/setup`, `/projects/${project.id}/design`,
      `/projects/${project.id}/design/instruments/${first.id}`, `/projects/${project.id}/record-status`,
      `/projects/${project.id}/records/R001?event=baseline_arm_1&instrument=instrument`,
      `/projects/${project.id}/export`, `/projects/${project.id}/members`, `/projects/${project.id}/roles`,
      `/projects/${project.id}/groups`,
    ]

    const offenders: string[] = []
    for (const viewport of VIEWPORTS) {
      const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } })
      const page = await context.newPage()

      // Public pages first, without a session.
      for (const path of ["/login", "/password-reset", new URL(link).pathname]) {
        await page.goto(path)
        const extra = await overflow(page)
        if (extra > 0) offenders.push(`${viewport.name} ${path}: +${extra}px`)
      }

      await page.goto("/login")
      await page.getByRole("radio", { name: "Local", exact: true }).check()
      await page.fill("#login-email", ADMIN_EMAIL)
      await page.fill("#login-password", ADMIN_PASSWORD)
      await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
      await expect(page.locator("footer")).toContainText("Sign out")

      for (const path of pages) {
        await page.goto(path)
        await page.waitForLoadState("networkidle")
        const extra = await overflow(page)
        if (extra > 0) offenders.push(`${viewport.name} ${path}: +${extra}px`)
      }
      await context.close()
    }

    expect(offenders, offenders.join("\n")).toEqual([])
  })
})
