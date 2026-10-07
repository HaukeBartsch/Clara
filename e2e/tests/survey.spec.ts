import { expect, test, type APIRequestContext } from "@playwright/test"

// M6 — the public survey page (User_Interface_Design.md §8.8, REQ-UI-028): a link issued from
// the record view opens in a browser that holds no session, shows the link's instrument with
// the same show/hide logic as the record form (§8.4), never starts a session (no cookie, GD-1),
// and never talks to the API from the browser (REQ-API-084). A revoked link is one
// "no longer valid" state (REQ-AUTH-040).
//
// Submitting is the other half of the exit criterion (Plan/Web_Implementation.md §9): the page
// posts its answers and nothing else, because the API resolves the record, instrument and event
// from the link token (REQ-API-083) — so the assertions here also check that the study's record
// identifier never appears in the page or its requests.

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

test.describe("public survey page (M6)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("a link issued from the record view opens without a session and follows the form logic", async ({ browser, page, request, baseURL }) => {
    test.setTimeout(120_000)
    const tag = Date.now()

    // --- a project with a survey instrument mapped to baseline, and one record ---
    const project = await admin(request, "POST", "/api/v1/projects",
      { project_name: `M6 survey ${tag}`, organization: "Org", participant_names: "S[0-9][0-9][0-9]" })
    const base = `/api/v1/projects/${project.id}`
    const [first] = await admin(request, "GET", `${base}/instruments`)
    await admin(request, "POST", `${base}/instruments/${first.id}/fields`, { field_name: "record_id", field_type: "text" })
    const survey = await admin(request, "POST", `${base}/instruments`, { name: "feedback" })
    await admin(request, "PUT", `${base}/instruments/${survey.id}`, { is_survey: true })
    await admin(request, "PUT", `${base}/instrument-event-mapping`,
      { arm_num: 1, mapping: { instrument: ["baseline_arm_1"], feedback: ["baseline_arm_1"] } })
    await admin(request, "POST", `${base}/instruments/${survey.id}/fields/bulk`, { fields: [
      { field_name: "happy", field_type: "radio", field_label: "Are you happy?", choices: "1$Yes##2$No, not really", required: true },
      { field_name: "age", field_type: "text", validation_type: "integer", field_label: "Age" },
    ] })
    await admin(request, "POST", `${base}/instruments/${survey.id}/fields`,
      { field_name: "why", field_type: "text", field_label: "Why not?", branching_logic: '[baseline_arm_1][happy] = "2"' })
    const { token } = await admin(request, "PUT", `${base}/users/1`, { role: null })
    const imported = await request.post(`${API_URL}/api/`, { form: {
      token, content: "record", format: "json",
      "data[0][record_id]": "S001", "data[0][form_name]": "instrument", "data[0][event_name]": "baseline_arm_1",
    } })
    expect(JSON.stringify(await imported.json())).not.toContain('"import_record_id":0')

    // --- a member issues the link from the record view (§8.7) ---
    await page.goto("/login")
    await page.getByRole("radio", { name: "Local", exact: true }).check()
    await page.fill("#login-email", ADMIN_EMAIL)
    await page.fill("#login-password", ADMIN_PASSWORD)
    await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
    await page.goto(`/projects/${project.id}/records/S001?event=baseline_arm_1&instrument=feedback`)
    await page.locator(".clara-survey-link").getByRole("button", { name: "Show survey link" }).click()
    const link = await page.locator("#clara-survey-url").inputValue()
    // The URL the API builds is a route of this application (Plan/Web_Implementation.md §9).
    expect(link.startsWith(`${baseURL}/s/`)).toBeTruthy()

    // --- the respondent: a fresh browser, no session ---
    const respondent = await (await browser.newContext()).newPage()
    const requests: string[] = []
    respondent.on("request", (r) => requests.push(r.url()))
    const response = await respondent.goto(link)
    expect(response?.status()).toBe(200)
    await expect(respondent.locator(".clara-survey-form")).toBeVisible()
    await expect(respondent.getByText("Are you happy?")).toBeVisible()
    await expect(respondent.getByText("No, not really")).toBeVisible() // a label with a comma, intact
    await expect(respondent.locator('[data-clara-field="record_id"]')).toHaveCount(0) // the study's, not shown

    // The same show/hide logic as the record form (§8.4).
    const why = respondent.locator('[data-clara-field="why"]')
    await expect(why).toBeHidden()
    await respondent.getByLabel("No, not really").check()
    await expect(why).toBeVisible()
    await respondent.getByLabel("Yes", { exact: true }).check()
    await expect(why).toBeHidden()

    // Advisory validation runs here too (REQ-VAL-002).
    await respondent.fill("#f-age", "abc")
    await expect(respondent.locator('[data-clara-field="age"] [data-clara-hint]')).toHaveText("Expected a whole number.")

    // The respondent is never told which record they are filling (REQ-AUTH-039): the token is
    // the only thing that addresses it, and the API resolves the triple from it (§3.10).
    expect(await respondent.locator("body").innerText()).not.toContain("S001")

    // Submit (REQ-UI-028): answers only — no record, instrument or event in the post (§8.8).
    await respondent.getByLabel("No, not really").check()
    await respondent.fill("#f-why", "the coffee")
    await respondent.fill("#f-age", "41")
    await respondent.locator(".clara-survey-form button[type=submit]").click()
    await expect(respondent.locator(".clara-survey-done")).toBeVisible()

    // The answers landed on the link's own (record, instrument, event) …
    const exported = await request.post(`${API_URL}/api/`, { form: {
      token, content: "record", format: "json", records: "S001", events: "baseline_arm_1",
    } })
    expect(exported.ok()).toBeTruthy()
    expect(JSON.stringify(await exported.json())).toContain("the coffee")

    // …and the first save through the link stamped its collection date (REQ-DB-041).
    const issued = await admin(request, "GET",
      `${base}/records/S001/instruments/${survey.id}/survey-link?event=baseline_arm_1`)
    expect(issued.collected_at).not.toBe("")

    // Re-opening the link serves the form again — the respondent may change their answers
    // (REQ-AUTH-042), though what they already said is not read back to them.
    await respondent.goto(link)
    await expect(respondent.locator(".clara-survey-form")).toBeVisible()

    // Outside the session (GD-1), and the browser only ever asked this application (REQ-API-084).
    expect(await respondent.context().cookies()).toEqual([])
    expect(requests.every((url) => url.startsWith(`${baseURL}/`) && !url.includes("/api/"))).toBeTruthy()

    // --- revoked: one state, no retry (REQ-AUTH-040) ---
    await page.locator(".clara-survey-link").getByRole("button", { name: "Revoke link" }).click()
    await page.locator("#clara-confirm [data-clara-confirm-ok]").click()
    await expect(page.locator(".alert-success")).toContainText("revoked")
    const revoked = await respondent.goto(link)
    expect(revoked?.status()).toBe(404)
    await expect(respondent.locator(".clara-survey-notice")).toContainText("no longer valid")
    await expect(respondent.locator(".clara-survey-form")).toHaveCount(0)
  })
})
