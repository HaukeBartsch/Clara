// The dashboard's own behaviour (User_Interface_Design.md §4): the projects the API lists become a
// bento grid — three tiers of box, most recently used first and largest (REQ-UI-070/071). Every
// box carries the same facts the table used to split into columns (name, organization, and the
// record, instrument and field counts) plus when the acting user last worked there; which way round
// they sit follows the shape of the box, in CSS, so a wide box reads across and a narrow one stacks.
// The server renders the grid with its loading line and this module fills it from the same route's
// JSON (REQ-UI-032, REQ-UI-044), then re-reads it while the page is visible so the grid stays
// current (REQ-UI-073). The boxes are grouped by region — the user's own first — and ranked by
// recency inside each group (REQ-UI-076). One module for one surface (REQ-UI-045); no UI string
// appears here — every label comes from the injected data-i18n block (REQ-UI-008).

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

/** The REQ-DB-006 code order PHP renders onto the grid (REQ-UI-076); empty when it is missing. */
function regionOrder(grid) {
  try {
    const order = JSON.parse(grid.dataset.claraRegionOrder || "[]")
    return Array.isArray(order) ? order.map(String) : []
  } catch (error) {
    return []
  }
}

/**
 * Splits the ranked list into region groups (REQ-UI-076): the group the API flags as the acting user's
 * region (`in_preferred_region`) first, the others in REQ-DB-006 order, and a code outside that order
 * after them, in code order. Each group keeps the ranked order, so recency still decides inside it —
 * and the tiers stay recency-only, so a project of another region used today keeps its wide box.
 */
function groupByRegion(ranked, order) {
  const groups = new Map()
  ranked.forEach((entry) => {
    const code = String(entry.project.organization || "")
    if (!groups.has(code)) {
      groups.set(code, { code, preferred: false, entries: [] })
    }
    const group = groups.get(code)
    group.entries.push(entry)
    group.preferred = group.preferred || entry.project.in_preferred_region === true
  })
  const position = (code) => (order.includes(code) ? order.indexOf(code) : order.length)

  return Array.from(groups.values()).sort((a, b) => Number(b.preferred) - Number(a.preferred)
    || position(a.code) - position(b.code)
    || (a.code < b.code ? -1 : a.code > b.code ? 1 : 0))
}

/** A group's heading row, spanning the grid: "Your region: OUS · 214", "Ahus · 120". */
function groupHeading() {
  return el("li", { class: "clara-project-group" }, el("h2", { class: "h6 mb-0" }))
}

function headingText(group) {
  const params = { region: group.code || "—", count: String(group.entries.length) }

  return t(group.preferred ? "dashboard.group.preferred" : "dashboard.group.region", params)
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

const TIER_CLASSES = ["recent", "active", "idle"].map((tier) => "clara-project-cell--" + tier)

/**
 * One project's box, empty of the facts that can change — paint() writes those. The DOM order is the
 * same in every box — name, organization, last used, then the three counts — and `app.css` lays those
 * out down or across by the box's own width, which is why a wide box and a narrow one can hold
 * identical markup: the head group and the count group simply change from stacked to side by side.
 * Nothing here knows which tier it is rendering.
 */
function projectBox(project) {
  // el() takes its children as one array; extra arguments would be dropped.
  return el("li", { class: "clara-project-cell" },
    el("article", { class: "clara-project" }, [
      el("div", { class: "clara-project-head" }, [
        el("p", { class: "clara-project-name" },
          el("a", { class: "clara-project-open stretched-link", href: "/projects/" + project.id })),
        el("p", { class: "clara-project-org" }),
        el("p", { class: "clara-project-used" }),
      ]),
      el("div", { class: "clara-project-tail" }, [
        el("div", { class: "clara-project-stats" }, [
          stat(t("dashboard.project.records"), ""),
          stat(t("dashboard.project.instruments"), ""),
          stat(t("dashboard.project.fields"), ""),
        ]),
        // The wide box has room to name what the stretched link does; a narrow one has the link alone.
        el("p", { class: "clara-project-go" }, t("dashboard.project.open")),
      ]),
    ]))
}

/** Writes a text only where it differs, so a refresh leaves an unchanged fact untouched. */
function setText(node, text) {
  if (node.textContent !== text) {
    node.textContent = text
  }
}

/**
 * Brings a box up to date with its project: tier, name, organization (hidden where there is none),
 * last used and the three counts.
 */
function paint(box, entry) {
  const tier = "clara-project-cell--" + entry.tier
  if (!box.classList.contains(tier)) {
    box.classList.remove(...TIER_CLASSES)
    box.classList.add(tier)
  }
  setText(box.querySelector(".clara-project-open"), String(entry.project.project_name))
  const org = box.querySelector(".clara-project-org")
  setText(org, entry.project.organization ? String(entry.project.organization) : "")
  org.hidden = !entry.project.organization
  setText(box.querySelector(".clara-project-used"), usedText(entry))
  const values = box.querySelectorAll(".clara-project-stat-value")
  const counts = [entry.project.record_count, entry.project.instrument_count, entry.project.field_count]
  counts.forEach((value, index) => setText(values[index], String(value)))
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
// The box each project id is drawn in. A project keeps its box across refreshes, so a redraw rewrites
// what changed instead of rebuilding the grid — which made Safari jump the page back to the top.
let boxes = new Map()
// The heading each region group is drawn under, by organization code — kept the same way as the boxes.
let headings = new Map()

/**
 * Brings the grid and the heading's count to the list in place (REQ-UI-073): each project's box is
 * reused and repainted, a box moves only where the order changed, a new project gets a new box, and
 * whatever is left over — a project no longer visible, the loading or failure line — goes. The
 * scroll position stays where the reader left it, and keyboard focus on the project that had it.
 * With projects in more than one region each group starts with its heading (REQ-UI-076); with one
 * region there is none, and the grid is the single ranked list.
 */
function render(grid, projects) {
  const ranked = rank(projects)
  const focused = grid.contains(document.activeElement) ? document.activeElement : null
  const count = document.querySelector("[data-clara-project-count]")
  if (count) {
    setText(count, String(ranked.length))
  }

  // Only an administrator reaches this page with an empty list (a member with no projects gets the
  // no-access page, §2.3): an installation with no projects yet.
  if (!ranked.length) {
    boxes = new Map()
    headings = new Map()
    fill(grid, [message(t("dashboard.empty"), false)])
    return
  }

  const groups = groupByRegion(ranked, regionOrder(grid))
  const titled = groups.length > 1
  const kept = new Map()
  const keptHeadings = new Map()
  const wanted = []
  groups.forEach((group) => {
    if (titled) {
      const heading = headings.get(group.code) || groupHeading()
      setText(heading.firstElementChild, headingText(group))
      keptHeadings.set(group.code, heading)
      wanted.push(heading)
    }
    group.entries.forEach((entry) => {
      const box = boxes.get(entry.project.id) || projectBox(entry.project)
      paint(box, entry)
      kept.set(entry.project.id, box)
      wanted.push(box)
    })
  })
  boxes = kept
  headings = keptHeadings

  // Whatever is not a box or heading to show — a project no longer visible, a region with nothing left,
  // the loading or failure line — goes first, so it cannot stand in the way of what comes after it.
  const shown = new Set(wanted)
  Array.from(grid.children).filter((child) => !shown.has(child)).forEach((child) => child.remove())

  // Then the boxes into order, moving only those an ordering change moved: a box that went forward is
  // inserted where it now belongs, and one that went back steps out until its turn comes. Safari drops
  // the scroll position when a redraw moves most of the grid, which moving every box after it would.
  let cursor = grid.firstElementChild
  for (const box of wanted) {
    if (cursor && box !== cursor && cursor.nextElementSibling === box) {
      const later = cursor
      cursor = box
      later.remove()
    }
    if (box === cursor) {
      cursor = cursor.nextElementSibling
    } else {
      grid.insertBefore(box, cursor)
    }
  }

  // A box that moved lost focus on the way; give it back without scrolling the page to it.
  if (focused && focused.isConnected && document.activeElement !== focused) {
    focused.focus({ preventScroll: true })
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
 * on the region — not a listener per box — means boxes a retry or refresh adds keep working without
 * re-binding.
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
