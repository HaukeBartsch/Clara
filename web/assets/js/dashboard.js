// The dashboard's own behaviour (User_Interface_Design.md §4): bind the project
// list from this route's data region into the table body the server rendered as an
// empty container (REQ-UI-032, REQ-UI-044). One module for one surface
// (REQ-UI-045); no UI string appears here — every label comes from the injected
// data-i18n block (REQ-UI-008).

import { el, fetchJson, fill, notice, ready, t, td } from "../app.js"

const COLUMNS = 4

function projectRow(project) {
  const link = el("a", { href: "/projects/" + project.id })
  link.textContent = project.project_name

  const name = document.createElement("td")
  name.appendChild(link)
  if (project.organization) {
    name.appendChild(el("div", { class: "text-body-secondary small" }, project.organization))
  }

  // el() takes its children as one array; extra arguments would be dropped.
  return el("tr", { class: "clara-project-row" }, [
    name,
    td(String(project.record_count), "clara-stat"),
    td(String(project.instrument_count), "clara-stat"),
    td(String(project.field_count), "clara-stat")
  ])
}

/** A failure row carrying the one action that can recover: fetch it again. */
function failedRow() {
  const row = notice(COLUMNS, t("js.load_failed"))
  const retry = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary ms-2" })
  retry.textContent = t("js.retry")
  retry.addEventListener("click", loadProjects)
  row.querySelector("td").appendChild(retry)

  return row
}

async function loadProjects() {
  const body = document.querySelector("[data-region='projects']")
  if (!body) {
    return
  }

  try {
    const projects = await fetchJson("/")
    // An empty list is the no-access page's job, and the server already rendered
    // it when there was nothing to show; a client-side empty read says so too.
    fill(body, projects.length ? projects.map(projectRow) : [notice(COLUMNS, t("no_access.title"))])
  } catch (error) {
    fill(body, [failedRow()])
  }
}

ready(() => {
  // The server-rendered placeholder says "loading"; the region is filled from the
  // JSON the same route serves, and navigation stays a plain GET (§3.7).
  loadProjects()
})
