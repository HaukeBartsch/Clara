// The dashboard's own behaviour (User_Interface_Design.md §4): the projects the API lists become a
// bento grid — three tiers of box, most recently used first and largest (REQ-UI-070/071). Every
// box carries the same facts the table used to split into columns (name, organization, and the
// record, instrument and field counts) plus when the acting user last worked there; which way round
// they sit follows the shape of the box, in CSS, so a wide box reads across and a narrow one stacks.
// The server renders the grid with its loading line and this module fills it from the same route's
// JSON (REQ-UI-032, REQ-UI-044), then re-reads it while the page is visible so the grid stays
// current (REQ-UI-073). One module for one surface (REQ-UI-045); no UI string appears here — every
// label comes from the injected data-i18n block (REQ-UI-008).

import { el, fetchJson, fill, ready, t } from "../app.js"

const DAY = 86400000

// How often the overview re-reads its region while visible (REQ-UI-073): often enough that a project
// someone else created, or records added to one, appear without a reload; seldom enough that an open
// tab costs the API one list read per two seconds.
const REFRESH_MS = 2000

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
    retry.addEventListener("click", () => loadProjects(false))
    line.appendChild(retry)
  }

  return el("li", { class: "clara-project-cell clara-project-cell--message" }, line)
}

// What the grid draws now: the list as read plus the day it was ranked on — "today", "yesterday" and
// the box tiers move at midnight even when the list does not — or FAILED for the failure line. A read
// that brings the same thing leaves the DOM alone, so hover and focus survive a refresh.
const FAILED = "failed"
let drawn = null
// Whether this page has shown the list once: only then does a refused refresh mean the session ended.
let listed = false

/** Draws the boxes and the heading's count, keeping keyboard focus on the project that had it. */
function render(grid, projects) {
  const ranked = rank(projects)
  const focused = grid.contains(document.activeElement) ? document.activeElement.getAttribute("href") : null
  // Only an administrator reaches this page with an empty list (a member with no projects gets the
  // no-access page, §2.3): an installation with no projects yet.
  fill(grid, ranked.length ? ranked.map(projectBox) : [message(t("dashboard.empty"), false)])
  if (focused) {
    const link = Array.from(grid.querySelectorAll(".clara-project-open")).find((open) => open.getAttribute("href") === focused)
    if (link) {
      link.focus()
    }
  }
  const count = document.querySelector("[data-clara-project-count]")
  if (count) {
    count.textContent = String(ranked.length)
  }
}

/**
 * Reads the region and redraws the grid when it changed; `refresh` marks the timer's reads
 * (REQ-UI-073). Resolves false once the page should stop refreshing.
 */
async function loadProjects(refresh) {
  const grid = document.querySelector("[data-region='projects']")
  if (!grid) {
    return false
  }

  let projects
  try {
    projects = await fetchJson("/", { refresh })
  } catch (error) {
    if (refresh && error.message === "forbidden") {
      // The session ended — idle, over age, revoked — or access was withdrawn: a reload lets the guard
      // decide, which shows the login page with its reason (REQ-AUTH-015) or the no-access page. A page
      // that never listed anything just stops, so a refusal cannot become a reload loop.
      if (listed) {
        window.location.reload()
      }
      return false
    }
    // A failed refresh keeps the boxes last read — still the best answer there is — and the next tick
    // tries again; only a grid with nothing to show takes the failure line.
    if (!listed && drawn !== FAILED) {
      drawn = FAILED
      fill(grid, [message(t("js.load_failed"), true)])
    }
    return true
  }

  listed = true
  // A tapped box is mid-animation on its way to the project page; a redraw would cut it off.
  if (grid.querySelector(".clara-project-cell--opening")) {
    return true
  }
  const shape = JSON.stringify(projects) + "|" + new Date().toDateString()
  if (shape !== drawn) {
    drawn = shape
    render(grid, projects)
  }

  return true
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

/** Resolves as soon as the document is visible — refreshing pauses while the tab is hidden. */
function whenVisible() {
  if (!document.hidden) {
    return Promise.resolve()
  }

  return new Promise((resolve) => document.addEventListener("visibilitychange", () => resolve(), { once: true }))
}

/**
 * Keeps the overview current (REQ-UI-073): a read every REFRESH_MS while the page is visible, and one
 * at a time — the next waits for the one before it, so a slow API never has reads stacking up.
 */
async function keepCurrent() {
  for (;;) {
    await sleep(REFRESH_MS)
    await whenVisible()
    if (!(await loadProjects(true))) {
      return
    }
  }
}

/**
 * A tapped project box plays a short bounce, then zooms out to double its size while fading away,
 * and only then does the browser follow the link (User_Interface_Design.md §4). The animation is a
 * presentation concern, so it lives here with the grid; the link itself stays a plain GET. Delegation
 * on the region — not a listener per box — means a retry or refresh re-render (which replaces the
 * boxes) keeps working without re-binding.
 */
function bindProjectOpen() {
  // If the dashboard is restored from the back/forward cache, any box still carrying the opening
  // state would be frozen at the animation's fully-transparent end — clear it so it is visible again.
  window.addEventListener("pageshow", (event) => {
    if (!event.persisted) {
      return
    }
    document.querySelectorAll(".clara-project-cell--opening").forEach((box) => box.classList.remove("clara-project-cell--opening"))
  })

  const grid = document.querySelector("[data-region='projects']")
  if (!grid) {
    return
  }

  grid.addEventListener("click", (event) => {
    const link = event.target.closest(".clara-project-open")
    if (!link) {
      return
    }
    const cell = link.closest(".clara-project-cell")
    const href = link.getAttribute("href")
    if (!cell || !href || cell.classList.contains("clara-project-cell--opening")) {
      return
    }
    event.preventDefault()

    // A user who asked for less motion is taken straight to the project — no spring, no zoom.
    const reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches
    if (reduce) {
      window.location.assign(href)
      return
    }

    cell.classList.add("clara-project-cell--opening")

    // Open when the zoom finishes; a browser that drops the animation (hidden tab, unsupported
    // keyframes) still opens via the fallback, and the flag keeps the two paths from racing.
    let opened = false
    const open = () => {
      if (opened) {
        return
      }
      opened = true
      // Drop the transient opening styles before leaving, so a later back/forward restore (bfcache)
      // shows the box in its normal place, not frozen at the animation's fully-transparent end.
      cell.classList.remove("clara-project-cell--opening")
      window.location.assign(href)
    }
    cell.addEventListener("animationend", (ended) => {
      if (ended.animationName === "clara-project-open") {
        open()
      }
    })
    window.setTimeout(open, 650)
  })
}

ready(() => {
  // The server-rendered placeholder says "loading"; the region is filled from the JSON the same
  // route serves, and navigation stays a plain GET (§3.7).
  loadProjects(false).then((going) => {
    if (going) {
      keepCurrent()
    }
  })
  bindProjectOpen()
})
