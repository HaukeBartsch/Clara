// The dashboard's own behaviour (User_Interface_Design.md §4): the projects the API lists become a
// bento grid — three tiers of box, most recently used first and largest (REQ-UI-070/071). Every
// box carries the same facts the table used to split into columns (name, organization, and the
// record, instrument and field counts) plus when the acting user last worked there; which way round
// they sit follows the shape of the box, in CSS, so a wide box reads across and a narrow one stacks.
// The server renders the grid with its loading line and this module fills it from the same route's
// JSON (REQ-UI-032, REQ-UI-044). One module for one surface (REQ-UI-045); no UI string appears here
// — every label comes from the injected data-i18n block (REQ-UI-008).

import { el, fetchJson, fill, ready, t } from "../app.js"

const DAY = 86400000

// Box size follows when a project was last used (REQ-UI-071): within a week it takes the wide box,
// within a month the middle one, and older or never-opened projects the compact one. The tiers order
// and size the grid; they do not rank one project's data over another's — three counts always appear.
const RECENT_DAYS = 7
const ACTIVE_DAYS = 30

/**
 * The API's stored `YYYY-MM-DD HH:MM:SS` UTC text (REQ-API-049) as a Date, or null for a project the
 * acting user has never generated an event in. The layout carries no offset, so "Z" is what says the
 * time is UTC; without it a browser would read the stamp as local and shift every box by its zone.
 */
function lastUsed(project) {
  if (!project.last_accessed_at) {
    return null
  }
  const when = new Date(String(project.last_accessed_at).replace(" ", "T") + "Z")

  return Number.isNaN(when.getTime()) ? null : when
}

/** Whole calendar days before today, counted in the browser's zone — the user's own "yesterday". */
function daysBeforeToday(when) {
  const dayStart = (date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()

  return Math.round((dayStart(new Date()) - dayStart(when)) / DAY)
}

/** The size class of a project's box, from how long ago it was used; null (never) is the smallest. */
function tierOf(days) {
  if (days === null) {
    return "idle"
  }
  if (days <= RECENT_DAYS) {
    return "recent"
  }

  return days <= ACTIVE_DAYS ? "active" : "idle"
}

/** Never-used sorts after anything used; otherwise the more recent day comes first. */
function compareDays(left, right) {
  if (left === null) {
    return right === null ? 0 : 1
  }
  if (right === null) {
    return -1
  }

  return left - right
}

/**
 * Attaches each project's recency and box tier, then orders the grid: most recently used first,
 * never-opened last, and the reader's own alphabet within a day (REQ-UI-071). The API's order is
 * alphabetical and stays untouched — ordering is this page's presentation, not the endpoint's.
 */
function rank(projects) {
  return projects
    .map((project) => {
      const when = lastUsed(project)
      const days = when === null ? null : daysBeforeToday(when)

      return { project, when, days, tier: tierOf(days) }
    })
    .sort((a, b) => compareDays(a.days, b.days)
      || String(a.project.project_name).localeCompare(String(b.project.project_name), undefined, { sensitivity: "base" }))
}

// A date says less than a day count and more than a word, so the box shows "today", "yesterday", or
// the date itself — with the year once it is no longer this year. Intl formats it in the browser's
// locale; the app's own language controls which words wrap it (REQ-UI-008).
const dateOptions = { day: "numeric", month: "short" }

function formatDate(when) {
  const sameYear = when.getFullYear() === new Date().getFullYear()
  const options = sameYear ? dateOptions : Object.assign({ year: "numeric" }, dateOptions)
  try {
    return new Intl.DateTimeFormat(navigator.language || undefined, options).format(when)
  } catch (error) {
    // A locale the browser cannot resolve still has to yield a date the user can read.
    return new Intl.DateTimeFormat(undefined, options).format(when)
  }
}

/** The "last used" line: relative for the two days it matters most, dated beyond them. */
function usedText(entry) {
  if (entry.when === null) {
    return t("dashboard.project.never_used")
  }
  if (entry.days === 0) {
    return t("dashboard.project.last_used", { when: t("dashboard.project.last_used.today") })
  }
  if (entry.days === 1) {
    return t("dashboard.project.last_used", { when: t("dashboard.project.last_used.yesterday") })
  }

  return t("dashboard.project.last_used", { when: formatDate(entry.when) })
}

/** One count with its label — value first, so the figures of a row of stats line up. */
function stat(label, value) {
  return el("div", { class: "clara-project-stat" }, [
    el("span", { class: "clara-project-stat-value" }, String(value)),
    el("span", { class: "clara-project-stat-label" }, label),
  ])
}

/**
 * One project's box. The DOM order is the same in every box — name, organization, last used, then the
 * three counts — and `app.css` lays those out down or across by the box's own width, which is why a
 * wide box and a narrow one can hold identical markup: the head group and the count group simply
 * change from stacked to side by side. Nothing here knows which tier it is rendering.
 */
function projectBox(entry) {
  const head = [
    el("p", { class: "clara-project-name" },
      el("a", { class: "clara-project-open stretched-link", href: "/projects/" + entry.project.id },
        entry.project.project_name)),
  ]
  if (entry.project.organization) {
    head.push(el("p", { class: "clara-project-org" }, entry.project.organization))
  }
  head.push(el("p", { class: "clara-project-used" }, usedText(entry)))

  // el() takes its children as one array; extra arguments would be dropped.
  return el("li", { class: "clara-project-cell clara-project-cell--" + entry.tier },
    el("article", { class: "clara-project" }, [
      el("div", { class: "clara-project-head" }, head),
      el("div", { class: "clara-project-tail" }, [
        el("div", { class: "clara-project-stats" }, [
          stat(t("dashboard.project.records"), entry.project.record_count),
          stat(t("dashboard.project.instruments"), entry.project.instrument_count),
          stat(t("dashboard.project.fields"), entry.project.field_count),
        ]),
        // The wide box has room to name what the stretched link does; a narrow one has the link alone.
        el("p", { class: "clara-project-go" }, t("dashboard.project.open")),
      ]),
    ]))
}

/** A grid-wide line — loading, empty, or failed — in place of boxes. */
function message(text, withRetry) {
  const line = el("p", { class: "clara-region-empty mb-0" }, text)
  if (withRetry) {
    const retry = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary ms-2" }, t("js.retry"))
    retry.addEventListener("click", loadProjects)
    line.appendChild(retry)
  }

  return el("li", { class: "clara-project-cell clara-project-cell--message" }, line)
}

async function loadProjects() {
  const grid = document.querySelector("[data-region='projects']")
  if (!grid) {
    return
  }

  try {
    const projects = await fetchJson("/")
    // Only an administrator reaches this page with an empty list (a member with no projects gets the
    // no-access page, §2.3): an installation with no projects yet.
    const ranked = rank(projects)
    fill(grid, ranked.length ? ranked.map(projectBox) : [message(t("dashboard.empty"), false)])
  } catch (error) {
    fill(grid, [message(t("js.load_failed"), true)])
  }
}

ready(() => {
  // The server-rendered placeholder says "loading"; the region is filled from the JSON the same
  // route serves, and navigation stays a plain GET (§3.7).
  loadProjects()
})
