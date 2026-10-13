// Builds docs/intro-to-Clara/CLARA_Introduction.pptx — the "Intro to CLARA" deck for a
// non-technical audience (VISION_AND_REQUIREMENTS.md "Presentation"). The .pptx opens directly
// in Apple Keynote (and PowerPoint); screenshots come from ../screenshots/ (capture_screenshots.mjs)
// and are shown in an iPad frame, icons from ./icons/ (Tabler Icons, MIT).
//
//   cd docs/intro-to-Clara/tools && npm install && node build_presentation.cjs
//
// Fonts are macOS system fonts (Avenir Next, Helvetica Neue) because Keynote is the target;
// PowerPoint on Windows substitutes its own.

const path = require("node:path")
const pptxgen = require("pptxgenjs")

const HERE = __dirname
const SHOTS = path.resolve(HERE, "..", "screenshots")
const ICONS = path.join(HERE, "icons")
const OUT = path.resolve(HERE, "..", "CLARA_Introduction.pptx")

// Lighthouse palette: night-sea navy dominates, sea blue supports, lantern amber accents.
const NAVY = "0B2239"
const SEA = "1F5F7A"
const AMBER = "F2A93B"
const AMBER_DK = "A85A12"
const INK = "1B2733"
const MUTED = "5B6B7A"
const TINT = "EEF3F7"
const LINE = "C9D6E0"
const SOFT = "D6E2EC"
const HEAD = "Avenir Next"
const BODY = "Helvetica Neue"

const pres = new pptxgen()
pres.layout = "LAYOUT_WIDE" // 13.333 × 7.5 in
pres.title = "CLARA — an introduction"
pres.subject = "Clinical Logbook for Automated Research Assistance"
pres.author = "CLARA project"
pres.theme = { headFontFace: HEAD, bodyFontFace: BODY }

pres.defineSlideMaster({
  title: "CLARA_CONTENT",
  background: { color: "FFFFFF" },
  objects: [
    { placeholder: { options: { name: "title", type: "title", x: 0.6, y: 0.4, w: 12.1, h: 0.85, fontFace: HEAD, fontSize: 36, bold: true, color: NAVY, align: "left", valign: "middle", margin: 0 }, text: "" } },
    { text: { text: "CLARA · Introduction", options: { x: 0.6, y: 7.0, w: 6, h: 0.3, fontFace: BODY, fontSize: 10, color: MUTED, margin: 0 } } },
  ],
  slideNumber: { x: 12.2, y: 7.0, w: 0.5, h: 0.3, fontFace: BODY, fontSize: 10, color: MUTED, align: "right" },
})
pres.defineSlideMaster({ title: "CLARA_DARK", background: { color: NAVY }, objects: [] })
pres.defineSlideMaster({
  title: "CLARA_SECTION",
  background: { color: NAVY },
  objects: [],
  slideNumber: { x: 12.2, y: 7.0, w: 0.5, h: 0.3, fontFace: BODY, fontSize: 10, color: "8FA6BA", align: "right" },
})

// --- helpers ---------------------------------------------------------------------------

let section = ""
const slideIn = (layout) => pres.addSlide({ masterName: layout, sectionTitle: section })
const startSection = (title) => {
  section = title
  pres.addSection({ title })
}

const text = (slide, value, opts) =>
  slide.addText(value, { fontFace: BODY, fontSize: 14, color: INK, margin: 0, valign: "top", isTextBox: true, ...opts })

const card = (slide, x, y, w, h, color = TINT, name) =>
  slide.addShape(pres.shapes.ROUNDED_RECTANGLE, { x, y, w, h, rectRadius: 0.12, fill: { color }, line: { color, width: 0 }, objectName: name })

const icon = (slide, name, x, y, d, color = SEA) => {
  slide.addShape(pres.shapes.OVAL, { x, y, w: d, h: d, fill: { color }, line: { color, width: 0 }, objectName: `icon ${name}` })
  const inset = d * 0.22
  slide.addImage({ path: path.join(ICONS, `${name}.png`), x: x + inset, y: y + inset, w: d - 2 * inset, h: d - 2 * inset, altText: name.replace(/-/g, " ") })
}

/** A 4:3 screenshot inside an iPad frame (landscape); returns the frame height. */
const ipad = (slide, shot, x, y, w, alt) => {
  const bezel = w * 0.035
  const sw = w - 2 * bezel
  const sh = sw * 0.75
  const h = sh + 2 * bezel
  slide.addShape(pres.shapes.ROUNDED_RECTANGLE, {
    x, y, w, h, rectRadius: bezel * 1.8, fill: { color: "1C1C1E" }, line: { color: "3A3A3C", width: 0.75 },
    shadow: { type: "outer", color: "000000", opacity: 0.22, blur: 10, offset: 3, angle: 90 }, objectName: `iPad ${shot}`,
  })
  slide.addImage({ path: path.join(SHOTS, `${shot}.png`), x: x + bezel, y: y + bezel, w: sw, h: sh, altText: alt })
  slide.addShape(pres.shapes.OVAL, { x: x + w / 2 - 0.03, y: y + bezel / 2 - 0.03, w: 0.06, h: 0.06, fill: { color: "48484A" }, line: { color: "48484A", width: 0 } })
  return h
}

const ipadHeight = (w) => (w - 2 * w * 0.035) * 0.75 + 2 * w * 0.035

/** A straight connector from (x1, y1) to (x2, y2). */
const connector = (slide, x1, y1, x2, y2, color = LINE) =>
  slide.addShape(pres.shapes.LINE, {
    x: Math.min(x1, x2), y: Math.min(y1, y2), w: Math.max(Math.abs(x2 - x1), 0.001), h: Math.max(Math.abs(y2 - y1), 0.001),
    flipV: (y2 < y1) !== (x2 < x1), line: { color, width: 1.75 },
  })

const chip = (slide, label, x, y, w, fill, color, h = 0.5) => {
  slide.addShape(pres.shapes.ROUNDED_RECTANGLE, { x, y, w, h, rectRadius: h / 2, fill: { color: fill }, line: { color: fill, width: 0 } })
  text(slide, label, { x, y, w, h, align: "center", valign: "middle", color, fontSize: 14, bold: true })
}

const title = (slide, value) => slide.addText(value, { placeholder: "title" })

/** Two iPads side by side with a caption under each (slides 12, 14, 16). */
const twoIpads = (slide, left, right) => {
  const w = 5.85
  const y = 1.45
  const h = ipadHeight(w)
  for (const [x, item] of [[0.6, left], [6.88, right]]) {
    ipad(slide, item.shot, x, y, w, item.alt)
    text(slide, [
      { text: item.lead, options: { bold: true, color: NAVY } },
      { text: item.text },
    ], { x, y: y + h + 0.18, w, h: 0.75 })
  }
}

const divider = (number, heading, subtitle, iconName, notes) => {
  const slide = slideIn("CLARA_SECTION")
  text(slide, String(number), { x: 0.8, y: 1.6, w: 2, h: 1.3, fontFace: HEAD, fontSize: 96, bold: true, color: AMBER })
  text(slide, heading, { x: 0.8, y: 3.05, w: 8.6, h: 1.0, fontFace: HEAD, fontSize: 44, bold: true, color: "FFFFFF", valign: "middle" })
  text(slide, subtitle, { x: 0.8, y: 4.15, w: 8.6, h: 0.9, fontSize: 20, color: SOFT })
  icon(slide, iconName, 10.0, 2.35, 2.3, SEA)
  slide.addNotes(notes)
  return slide
}

// --- 1 · title ---------------------------------------------------------------------------

startSection("Introduction")
{
  const slide = slideIn("CLARA_DARK")
  icon(slide, "building-lighthouse", 0.8, 1.2, 0.95, AMBER)
  text(slide, "CLARA", { x: 0.8, y: 2.3, w: 6.2, h: 1.15, fontFace: HEAD, fontSize: 66, bold: true, color: "FFFFFF", valign: "middle" })
  text(slide, "Clinical Logbook for Automated Research Assistance", { x: 0.8, y: 3.5, w: 6.2, h: 0.45, fontSize: 18, color: AMBER })
  text(slide, "A new foundation for FIONA's research imaging — ready for many hospitals", { x: 0.8, y: 4.15, w: 6.0, h: 1.0, fontSize: 22, color: SOFT })
  text(slide, "Introduction for a non-technical audience · October 2026", { x: 0.8, y: 6.55, w: 6.2, h: 0.35, fontSize: 12, color: "8FA6BA" })
  const w = 5.4
  ipad(slide, "02-dashboard", 7.3, (7.5 - ipadHeight(w)) / 2, w, "CLARA start page listing two demo projects")
  slide.addNotes("CLARA is a logbook for clinical research data — the name comes from a lighthouse logbook. This talk explains why we built it, what it does silently for FIONA, and what study teams see in the browser. The screenshots are from a demo installation as it looks on an iPad.")
}

// --- 2 · why ----------------------------------------------------------------------------

startSection("Why a new foundation")
divider(1, "Why a new foundation", "FIONA, its REDCap connection — and the step to several hospitals", "building-community",
  "First part: the problem. FIONA works today; what changes is that it should serve several hospitals.")

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "FIONA brings hospital images to research projects")
  const steps = [
    ["building-hospital", "Images are taken", "The hospital's image archive sends each new study to FIONA."],
    ["scan", "FIONA registers", "FIONA notes every incoming study and each of its image series."],
    ["user-check", "A researcher asks", "A study is requested for a specific research project."],
    ["send", "FIONA delivers", "FIONA removes the patient's identity and delivers the images."],
  ]
  steps.forEach(([iconName, head, body], i) => {
    const x = 0.6 + i * (2.75 + 0.37)
    const y = 1.6
    card(slide, x, y, 2.75, 3.3, TINT, `step ${i + 1}`)
    icon(slide, iconName, x + 0.3, y + 0.3, 0.85)
    text(slide, `STEP ${i + 1}`, { x: x + 0.3, y: y + 1.35, w: 2.2, h: 0.3, fontSize: 11, bold: true, color: AMBER_DK, charSpacing: 1 })
    text(slide, head, { x: x + 0.3, y: y + 1.65, w: 2.2, h: 0.45, fontFace: HEAD, fontSize: 18, bold: true, color: NAVY })
    text(slide, body, { x: x + 0.3, y: y + 2.15, w: 2.2, h: 1.0 })
    if (i < steps.length - 1) {
      slide.addShape(pres.shapes.CHEVRON, { x: x + 2.82, y: y + 0.55, w: 0.22, h: 0.35, fill: { color: AMBER }, line: { color: AMBER, width: 0 } })
    }
  })
  card(slide, 0.6, 5.2, 12.11, 1.4, NAVY, "behind the scenes")
  icon(slide, "database", 0.95, 5.5, 0.8, AMBER)
  text(slide, [
    { text: "Behind the scenes  ", options: { bold: true, color: AMBER } },
    { text: "FIONA keeps its bookkeeping — projects, incoming studies, transfer requests — in a data store it talks to through the REDCap programming interface (API). CLARA replaces that store." },
  ], { x: 2.05, y: 5.35, w: 10.4, h: 1.1, fontSize: 16, color: "FFFFFF", valign: "middle" })
  slide.addNotes("FIONA is the research information system for radiology. It receives DICOM studies from the hospital archive (PACS), registers them, and on request anonymizes them and sends them to a research project. All of FIONA's bookkeeping sits behind the REDCap API — that is the part CLARA takes over.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Several hospitals, one foundation")
  text(slide, "What a multi-hospital FIONA needs", { x: 0.6, y: 1.45, w: 5.6, h: 0.35, fontSize: 14, bold: true, color: MUTED })
  text(slide, "How CLARA provides it", { x: 6.9, y: 1.45, w: 5.8, h: 0.35, fontSize: 14, bold: true, color: MUTED })
  const rows = [
    ["plug-connected", "FIONA must keep working exactly as it does today", "CLARA answers the same REDCap-style requests — no change in FIONA"],
    ["building-community", "Staff of every hospital sign in with their own hospital account", "Sign-in by hospital name, each linked to that hospital's login service"],
    ["lock", "Projects and sites must not see each other's data", "Personal access keys per project, roles, and data access groups"],
    ["wifi-off", "It must run inside the hospital network", "Self-contained: everything is installed locally — no internet needed"],
    ["history", "Every action must be traceable", "A complete audit trail of every change and every view"],
  ]
  rows.forEach(([iconName, need, answer], i) => {
    const y = 1.9 + i * 0.98
    card(slide, 0.6, y, 5.6, 0.82, TINT, `need ${i + 1}`)
    icon(slide, iconName, 0.75, y + 0.135, 0.55)
    text(slide, need, { x: 1.45, y, w: 4.6, h: 0.82, fontSize: 15, valign: "middle" })
    slide.addShape(pres.shapes.CHEVRON, { x: 6.4, y: y + 0.24, w: 0.25, h: 0.34, fill: { color: AMBER }, line: { color: AMBER, width: 0 } })
    card(slide, 6.9, y, 5.81, 0.82, NAVY, `answer ${i + 1}`)
    text(slide, answer, { x: 7.15, y, w: 5.4, h: 0.82, fontSize: 15, color: "FFFFFF", valign: "middle" })
  })
  slide.addNotes("Moving from one hospital to several changes the requirements: people from different hospitals log in differently, data must stay separated, the system must live inside the hospital network, and everything must be traceable. FIONA itself should not have to change.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Same requests, new engine")
  const stats = [
    ["7", "kinds of request FIONA sends — each answered the way REDCap answers it"],
    ["0", "changes needed in FIONA itself"],
    ["2", "faces: a silent API for FIONA, and a web interface for people"],
  ]
  stats.forEach(([number, label], i) => {
    const x = 0.6 + i * (3.8 + 0.355)
    card(slide, x, 1.55, 3.8, 2.95, TINT, `stat ${number}`)
    text(slide, number, { x: x + 0.35, y: 1.7, w: 3.1, h: 1.2, fontFace: HEAD, fontSize: 72, bold: true, color: AMBER_DK, valign: "middle" })
    text(slide, label, { x: x + 0.35, y: 3.0, w: 3.1, h: 1.3, fontSize: 16 })
  })
  text(slide, "FIONA's seven requests, in plain words", { x: 0.6, y: 4.8, w: 12, h: 0.35, fontSize: 14, bold: true, color: MUTED })
  const requests = ["Project information", "Form definitions", "Visits (events)", "Which form at which visit", "Field names", "Next free record number", "Records: read, write, delete"]
  requests.forEach((label, i) => {
    const row = i < 4 ? 0 : 1
    const col = row === 0 ? i : i - 4
    chip(slide, label, 0.6 + col * (2.9 + 0.17), 5.3 + row * 0.65, 2.9, row === 1 && col === 2 ? NAVY : SOFT, row === 1 && col === 2 ? "FFFFFF" : NAVY)
  })
  slide.addNotes("FIONA uses seven kinds of REDCap API request: project, metadata, event, formEventMapping, exportFieldNames, generateNextRecordName and record (export, import, delete). CLARA answers all of them the REDCap way, so FIONA only needs a new address and token. Reading and writing records is the busiest of the seven.")
}

// --- 3 · the silent API ------------------------------------------------------------------

startSection("The silent partner")
divider(2, "The silent partner", "CLARA's API — where FIONA keeps its books, day and night", "database",
  "Second part: the API. Nobody sees it, but FIONA talks to it all the time.")

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Three kinds of information FIONA keeps in CLARA")
  const items = [
    ["07-record-incoming", "scan", "Incoming studies", "Each study that arrives: hospital, sending device, patient, description and date.", "Incoming study form with institution, sending device and patient fields"],
    ["08-record-series", "folders", "Image series", "Each series inside a study: description, number of images, detected image type.", "Series form with series description, file count and image type"],
    ["09-record-transfers-done", "send", "Transfer requests", "Where a study should go, under which anonymous name — and whether it has been sent.", "Transfers form with target project and anonymized name"],
  ]
  items.forEach(([shot, iconName, head, body, alt], i) => {
    const x = 0.6 + i * (3.8 + 0.355)
    const h = ipad(slide, shot, x, 1.5, 3.8, alt)
    icon(slide, iconName, x, 1.5 + h + 0.3, 0.45)
    text(slide, head, { x: x + 0.6, y: 1.5 + h + 0.3, w: 3.2, h: 0.45, fontFace: HEAD, fontSize: 18, bold: true, color: NAVY, valign: "middle" })
    text(slide, body, { x, y: 1.5 + h + 0.9, w: 3.8, h: 1.1 })
  })
  slide.addNotes("In the demo, the 'FIONA Data Transfer Registry' project is built from FIONA's real data dictionaries: incoming studies, their series, and transfer requests. FIONA fills these forms through the API; people can look at them in the browser.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "A transfer request, start to finish")
  const steps = [
    ["Images arrive", "FIONA records the study and its series in CLARA — automatically, through the API."],
    ["A researcher asks", "In the transfers form they name the target project and an anonymous participant name."],
    ["FIONA delivers", "Every 5 to 10 minutes FIONA anonymizes the marked studies and sends them on."],
    ["CLARA shows it is done", "FIONA writes the transfer date back; staff see the result — or an error to follow up."],
  ]
  steps.forEach(([head, body], i) => {
    const y = 1.6 + i * 1.28
    if (i < steps.length - 1) connector(slide, 0.9, y + 0.6, 0.9, y + 1.28)
    slide.addText(String(i + 1), { shape: pres.shapes.OVAL, x: 0.6, y, w: 0.6, h: 0.6, fill: { color: NAVY }, line: { color: NAVY, width: 0 }, fontFace: HEAD, fontSize: 18, bold: true, color: "FFFFFF", align: "center", valign: "middle", margin: 0 })
    text(slide, head, { x: 1.45, y: y + 0.08, w: 5.0, h: 0.4, fontFace: HEAD, fontSize: 18, bold: true, color: NAVY })
    text(slide, body, { x: 1.45, y: y + 0.5, w: 5.0, h: 0.72 })
  })
  ipad(slide, "09-record-transfers-done", 6.83, 1.65, 5.9, "A completed transfer: requested date, target project, anonymized name and the transfer date filled in by FIONA")
  slide.addNotes("The transfers form explains itself: a transfer starts once a date and a target project are filled in. FIONA's background process picks it up within 5 to 10 minutes, anonymizes and sends the study, and writes the transfer date back. The fields FIONA fills are read-only for people.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Built to be trusted")
  const cells = [
    ["key", "Personal access keys", "Each member gets their own key per project; it can be replaced at any time."],
    ["file-check", "Checked before stored", "Every value is validated against the form definition before it is saved."],
    ["history", "Every change recorded", "Who changed what, and when — kept in an audit trail that cannot be edited."],
    ["book", "Fully documented", "A built-in reference describes every request, for FIONA's developers and others."],
  ]
  cells.forEach(([iconName, head, body], i) => {
    const x = 0.6 + (i % 2) * (3.05 + 0.3)
    const y = 1.6 + Math.floor(i / 2) * (2.25 + 0.3)
    card(slide, x, y, 3.05, 2.25, TINT, head)
    icon(slide, iconName, x + 0.25, y + 0.25, 0.6)
    text(slide, head, { x: x + 0.25, y: y + 1.0, w: 2.6, h: 0.4, fontFace: HEAD, fontSize: 16, bold: true, color: NAVY })
    text(slide, body, { x: x + 0.25, y: y + 1.42, w: 2.6, h: 0.75 })
  })
  ipad(slide, "15-api-docs", 7.33, 1.75, 5.4, "The API documentation page in the Control Panel")
  slide.addNotes("Every API call carries a personal project key (token) that can be rotated. Values are validated before they are stored, every change is written to an audit trail, and the whole API is documented in the Control Panel.")
}

// --- 4 · the web interface ---------------------------------------------------------------

startSection("The web interface")
divider(3, "The web interface", "Everything a study team does — in the browser, on a laptop or an iPad", "forms",
  "Third part: what people see. Organized by theme, not by menu.")

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Six things the web interface does")
  const themes = [
    ["folders", "Projects", "Organize studies and move them from development to production to analysis."],
    ["forms", "Forms", "Design data collection forms — or import existing REDCap dictionaries."],
    ["clipboard-list", "Data collection", "Participants, visits and forms, with their status at a glance."],
    ["download", "Export", "Download data at exactly the level your role allows."],
    ["eye", "Oversight", "Audit trail, user accounts and settings for administrators."],
    ["school", "Help", "Training guides, and the interface in English, Bokmål and Nynorsk."],
  ]
  themes.forEach(([iconName, head, body], i) => {
    const x = 0.6 + (i % 3) * (3.8 + 0.355)
    const y = 1.55 + Math.floor(i / 3) * (2.4 + 0.3)
    card(slide, x, y, 3.8, 2.4, TINT, head)
    icon(slide, iconName, x + 0.3, y + 0.3, 0.65, i % 2 === 0 ? SEA : NAVY)
    text(slide, head, { x: x + 0.3, y: y + 1.1, w: 3.2, h: 0.4, fontFace: HEAD, fontSize: 18, bold: true, color: NAVY })
    text(slide, body, { x: x + 0.3, y: y + 1.55, w: 3.2, h: 0.75 })
  })
  slide.addNotes("The following slides take these six themes one at a time.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Projects: every team sees its own studies")
  twoIpads(slide,
    { shot: "02-dashboard", lead: "Start page — ", text: "only the projects you belong to, with their size at a glance.", alt: "Start page with the list of projects" },
    { shot: "03-project-overview", lead: "Project overview — ", text: "key facts and the project's mode: Development, Production or Analysis.", alt: "Project overview with counts, details and project mode" })
  slide.addNotes("After signing in, people see only their own projects. Each project has a mode: in Development everything can change; in Production design changes are staged and checked; in Analysis data entry stops and only viewing and export remain. Only system administrators change the mode.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Forms: design what you collect")
  ipad(slide, "05-design", 0.6, 1.5, 6.2, "Design page listing the instruments with an import section")
  const rows = [
    ["route", "Visits and forms: arms, events, and which form belongs to which visit"],
    ["file-spreadsheet", "Import existing REDCap data dictionaries — no retyping"],
    ["list-check", "Field types: text with checks, choices, checkboxes, yes/no, notes, calculations"],
    ["arrows-split-2", "Show a question only when it is relevant"],
    ["checks", "Repeat a form as often as needed"],
    ["shield-check", "In production, changes are staged and checked before they go live"],
  ]
  rows.forEach(([iconName, body], i) => {
    const y = 1.55 + i * 0.82
    icon(slide, iconName, 7.2, y + 0.08, 0.46)
    text(slide, body, { x: 7.85, y, w: 4.85, h: 0.65, fontSize: 15, valign: "middle" })
  })
  slide.addNotes("Setup defines the study's visits (events) and forms (instruments); Design edits the fields. Existing REDCap dictionaries import directly — the demo projects were created this way. Branching logic hides questions that do not apply; repeating forms can be filled several times per visit.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Collecting data: status at a glance")
  twoIpads(slide,
    { shot: "06-record-status", lead: "Record Status Dashboard — ", text: "one row per participant, a dot per form: grey, yellow or green.", alt: "Record Status Dashboard with three records and coloured status dots" },
    { shot: "07-record-incoming", lead: "A form — ", text: "every value keeps its history. Forms can also be sent out as a survey link.", alt: "A record's incoming study form with history buttons next to each field" })
  slide.addNotes("The dashboard shows each participant with a status dot per form: no data, some data, finished. Opening a form shows its fields; the small button next to each field shows its history — who entered which value and when. A form marked as survey can be filled by a participant through a personal link, without logging in.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Export: share data, safely")
  text(slide, "Four export levels — set per role, form and visit", { x: 0.6, y: 1.5, w: 6.2, h: 0.45, fontFace: HEAD, fontSize: 18, bold: true, color: NAVY })
  const levels = [
    ["Full dataset", "all fields and values, unchanged", NAVY, "FFFFFF"],
    ["No identifiers", "every identifying field removed", SEA, "FFFFFF"],
    ["De-identified", "identifiers removed, personal fields hashed, dates shifted", "A9C4D3", NAVY],
    ["None", "no download", SOFT, NAVY],
  ]
  levels.forEach(([name, body, fill, color], i) => {
    const y = 2.1 + i * 0.95
    card(slide, 0.6, y, 6.2 - i * 0.45, 0.8, fill, name)
    text(slide, [
      { text: name, options: { bold: true, breakLine: true } },
      { text: body, options: { fontSize: 14 } },
    ], { x: 0.85, y, w: 5.8 - i * 0.45, h: 0.8, fontSize: 15, color, valign: "middle" })
  })
  text(slide, "CSV or JSON  ·  Excel-friendly (æ, ø, å)  ·  every download logged", { x: 0.6, y: 6.05, w: 6.4, h: 0.5, fontSize: 14, color: MUTED })
  ipad(slide, "11-export", 7.13, 1.6, 5.6, "Export page with format options and the applied sensitivity level")
  slide.addNotes("What a person may export is part of their role. The export page shows which level applies before downloading. De-identified exports remove direct identifiers, hash personal fields and shift dates. Every download is written to the audit log.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Oversight: who did what, and who may sign in")
  twoIpads(slide,
    { shot: "14-audit", lead: "Audit trail — ", text: "every change and every view; filter it and export it.", alt: "Audits page with filters and a list of logged events" },
    { shot: "17-users", lead: "Users — ", text: "accounts with an end date, locked after 180 days without sign-in (adjustable).", alt: "Users page in the Control Panel" })
  slide.addNotes("Administrators work in the Control Panel. The audit trail records every change and every view of a record, can be filtered by project, user, event and date, and exported. User accounts can carry an end date and are disabled automatically after a period without sign-in until an administrator enables them again.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Help built in: training and languages")
  ipad(slide, "19-training", 0.6, 1.5, 6.2, "Training page with step-by-step guides")
  const rows = [
    ["school", "Training guides", "Step-by-step pictures for common tasks — from a new project to exporting audit trails."],
    ["language", "Your language", "Each user picks English, Norsk bokmål or Nynorsk."],
    ["file-spreadsheet", "Easy to translate", "Administrators export the texts as a spreadsheet, translate them, and import them again."],
  ]
  rows.forEach(([iconName, head, body], i) => {
    const y = 1.6 + i * 1.6
    icon(slide, iconName, 7.2, y, 0.6)
    text(slide, head, { x: 8.0, y: y + 0.02, w: 4.7, h: 0.4, fontFace: HEAD, fontSize: 18, bold: true, color: NAVY })
    text(slide, body, { x: 8.0, y: y + 0.47, w: 4.7, h: 0.9 })
  })
  slide.addNotes("The Training page holds short illustrated guides; new guides are just folders of pictures and text. The interface is translated into Bokmål and Nynorsk; administrators can update translations with a spreadsheet.")
}

// --- 5 · authentication and authorization ---------------------------------------------------

startSection("Signing in and permissions")
divider(4, "Signing in and permissions", "Hospital names, ways to sign in, and who may do what", "fingerprint",
  "Fourth part: how people get in, and what they may do once inside.")

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Sign in where you work: hospital names")
  const h = ipad(slide, "20-login-local", 0.6, 1.55, 5.6, "Sign-in page with the hospital choices Hospital 1, Hospital 2 and Local")
  text(slide, "The sign-in page: pick your hospital, then sign in the way that hospital uses.", { x: 0.6, y: 1.55 + h + 0.18, w: 5.6, h: 0.6 })
  text(slide, "Example", { x: 6.75, y: 1.45, w: 2, h: 0.3, fontSize: 12, bold: true, color: MUTED })
  chip(slide, "Hospital 1", 6.75, 2.15, 1.9, AMBER, NAVY, 0.55)
  const services = ["Hospital single sign-on (OAuth2)", "Hospital directory (LDAP)", "CLARA account (password)"]
  services.forEach((label, i) => {
    const y = 1.5 + i * 0.72
    connector(slide, 8.65, 2.425, 9.05, y + 0.31, MUTED)
    card(slide, 9.05, y, 3.68, 0.62, TINT, label)
    text(slide, label, { x: 9.2, y, w: 3.45, h: 0.62, fontSize: 14, valign: "middle" })
  })
  const rules = [
    "A name can stand for several sign-in services — they are tried at the same time, and the first success wins.",
    "One service can appear under several names.",
    "With only one name configured, the choice is skipped.",
    "Your e-mail address identifies you, whichever way you sign in.",
  ]
  rules.forEach((rule, i) => {
    const y = 3.85 + i * 0.75
    icon(slide, "checks", 6.75, y + 0.05, 0.38, SEA)
    text(slide, rule, { x: 7.3, y: y + 0.05, w: 5.4, h: 0.38, valign: "middle" })
  })
  slide.addNotes("Before signing in, people choose a name such as 'Hospital 1' or 'Hospital 2'. Behind each name the administrator connects one or more sign-in services: the hospital's single sign-on (OAuth2 / OpenID Connect), its directory (LDAP or LDAPS), or local CLARA accounts. Passwords are only sent to the services behind the chosen name, all at once; the first that says yes wins. In the demo, 'Local' stands for CLARA's own accounts.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Ways to sign in — and what protects them")
  text(slide, "Sign-in methods", { x: 0.6, y: 1.45, w: 5.9, h: 0.45, fontFace: HEAD, fontSize: 18, bold: true, color: NAVY })
  const methods = [
    ["login", "Hospital single sign-on", "OAuth2 / OpenID Connect, several providers possible. The hospital's own login handles its second factor."],
    ["server", "Hospital directory", "LDAP or LDAPS — several directory servers, asked at the same time."],
    ["key", "CLARA accounts", "E-mail invitation, forgotten-password reset and password change; passwords are kept only as a one-way hash."],
  ]
  methods.forEach(([iconName, head, body], i) => {
    const y = 2.05 + i * 1.5
    card(slide, 0.6, y, 5.9, 1.35, TINT, head)
    icon(slide, iconName, 0.8, y + 0.2, 0.6)
    text(slide, head, { x: 1.6, y: y + 0.15, w: 4.75, h: 0.38, fontFace: HEAD, fontSize: 16, bold: true, color: NAVY })
    text(slide, body, { x: 1.6, y: y + 0.53, w: 4.75, h: 0.75 })
  })
  text(slide, "Protection", { x: 6.83, y: 1.45, w: 5.9, h: 0.45, fontFace: HEAD, fontSize: 18, bold: true, color: NAVY })
  const protections = [
    ["device-mobile", "Two-factor sign-in: ", "authenticator app or e-mail code, recovery codes; can be required for everyone."],
    ["lock", "Lockout: ", "5 failed attempts within 10 minutes pause that account for 15 minutes."],
    ["user-off", "Account limits: ", "an end date per account; disabled after 180 days without sign-in."],
    ["clock-hour-4", "Sessions: ", "end after 30 minutes idle, or 8 hours in total."],
    ["history", "Logged: ", "every sign-in, failed attempt and sign-out."],
  ]
  protections.forEach(([iconName, lead, body], i) => {
    const y = 2.05 + i * 0.88
    icon(slide, iconName, 6.83, y + 0.1, 0.5, NAVY)
    text(slide, [
      { text: lead, options: { bold: true, color: NAVY } },
      { text: body },
    ], { x: 7.5, y, w: 5.2, h: 0.78, valign: "middle" })
  })
  slide.addNotes("Three ways in: the hospital's single sign-on, the hospital's directory, or a CLARA account. Two-factor sign-in (authenticator app or e-mail code) protects the directory and CLARA-account logins; single sign-on uses the hospital's own second factor. The numbers shown are the defaults; the session times and the inactivity limit can be configured.")
}

{
  const slide = slideIn("CLARA_CONTENT")
  title(slide, "Who may do what: roles and permissions")
  text(slide, "Data access", { x: 0.6, y: 1.5, w: 6, h: 0.35, fontSize: 14, bold: true, color: MUTED })
  ;[["No access", SOFT, NAVY], ["Read only", "A9C4D3", NAVY], ["View & edit", NAVY, "FFFFFF"]].forEach(([label, fill, color], i) =>
    chip(slide, label, 0.6 + i * (1.95 + 0.15), 1.9, 1.95, fill, color))
  text(slide, "Export", { x: 0.6, y: 2.65, w: 6, h: 0.35, fontSize: 14, bold: true, color: MUTED })
  ;[["None", SOFT, NAVY], ["De-identified", "A9C4D3", NAVY], ["No identifiers", SEA, "FFFFFF"], ["Full", NAVY, "FFFFFF"]].forEach(([label, fill, color], i) =>
    chip(slide, label, 0.6 + i * (1.45 + 0.13), 3.05, 1.45, fill, color))
  const rows = [
    ["hierarchy-2", "Set per form and visit for each role — roles are defined per project."],
    ["user-shield", "Project administrators change the setup; system administrators see every project."],
    ["building-community", "Data access groups keep each site's records apart in a shared project."],
  ]
  rows.forEach(([iconName, body], i) => {
    const y = 4.0 + i * 0.85
    icon(slide, iconName, 0.6, y + 0.07, 0.5)
    text(slide, body, { x: 1.3, y, w: 5.4, h: 0.65, valign: "middle" })
  })
  ipad(slide, "16-roles", 7.13, 1.55, 5.6, "Roles page with the permission table per instrument and event")
  slide.addNotes("A role is a named set of permissions inside one project, for example data-entry or controller. For every form and visit it sets a data access level and an export level, plus two extra rights: deleting a form's values and editing collected surveys. Members without a role have full rights in that project. Data access groups let several sites share one project without seeing each other's records.")
}

// --- 6 · close ------------------------------------------------------------------------------

startSection("Summary")
{
  const slide = slideIn("CLARA_DARK")
  text(slide, "In short", { x: 0.8, y: 0.7, w: 11, h: 0.9, fontFace: HEAD, fontSize: 40, bold: true, color: "FFFFFF", valign: "middle" })
  const points = [
    ["FIONA keeps working", "CLARA speaks REDCap's language, so FIONA needs no change."],
    ["Every hospital signs in its own way", "Hospital names connect each site to its own login service."],
    ["A complete tool for study teams", "Design, collect, export and audit — with permissions per form and visit."],
  ]
  points.forEach(([head, body], i) => {
    const x = 0.8 + i * (3.75 + 0.35)
    text(slide, String(i + 1), { x, y: 1.95, w: 1, h: 0.75, fontFace: HEAD, fontSize: 40, bold: true, color: AMBER })
    text(slide, head, { x, y: 2.8, w: 3.6, h: 0.8, fontFace: HEAD, fontSize: 20, bold: true, color: "FFFFFF" })
    text(slide, body, { x, y: 3.75, w: 3.6, h: 1.0, fontSize: 16, color: SOFT })
  })
  text(slide, "Next steps", { x: 0.8, y: 5.15, w: 11, h: 0.4, fontFace: HEAD, fontSize: 18, bold: true, color: AMBER })
  text(slide, "Configuration check  ·  Performance measurements  ·  Versioned upgrades  ·  Technical-debt list",
    { x: 0.8, y: 5.6, w: 11.7, h: 0.8, fontSize: 16, color: SOFT })
  icon(slide, "building-lighthouse", 11.75, 0.75, 0.8, AMBER)
  slide.addNotes("Summary: FIONA continues unchanged on a foundation built for several hospitals; each hospital signs in its own way; study teams get a complete, auditable data capture tool. The next steps are taken from the open items in VISION_AND_REQUIREMENTS.md.")
}

pres.writeFile({ fileName: OUT }).then((file) => console.log(`wrote ${file}`))
