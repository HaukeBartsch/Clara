// The Configuration Check section's browser probes (§5.10, REQ-UI-061). The server rates what
// PHP and the API can see from inside; whether nginx keeps the internal routes off the network
// and serves the assets with the right headers can only be seen from outside, so this module
// asks the way INSTALL.md §13.1's curl checks do and fills the browser rows.
//
// The probes are the one place the browser addresses /api/v1/* and the API's documentation
// paths, and they do so expecting nginx's 404 (REQ-UI-002 names the exception): sent without
// credentials and without following redirects, so a misrouted probe reaches nothing with this
// session. No UI string lives here — every line comes from the injected data-i18n block.

import { ready, t } from "../app.js"

const BADGES = {
  ok: "text-bg-success",
  warn: "text-bg-warning",
  fail: "text-bg-danger",
  info: "text-bg-info",
  skip: "text-bg-secondary",
  pending: "text-bg-secondary",
}

/** One request as an outside caller makes it: no cookie, no cache, no redirect followed. */
async function probe(path) {
  const response = await fetch(path, { method: "GET", credentials: "omit", cache: "no-store", redirect: "manual" })
  return {
    path,
    status: response.type === "opaqueredirect" ? 302 : response.status,
    header: (name) => response.headers.get(name) || "",
  }
}

/** Paths that must answer 404 from outside: every one that does not is listed. */
async function hidden(paths) {
  const answers = await Promise.all(paths.map(probe))
  return answers.filter((a) => a.status !== 404).map((a) => a.path + " → " + a.status)
}

const PROBES = {
  "browser.admin_api": async () => {
    const exposed = await hidden(["/api/v1/health"])
    return exposed.length === 0
      ? ["ok", t("admin.check.browser.admin_api.ok")]
      : ["fail", t("admin.check.browser.admin_api.exposed", { list: exposed.join(", ") })]
  },

  "browser.docs": async () => {
    const exposed = await hidden(["/docs", "/openapi.json", "/healthz"])
    return exposed.length === 0
      ? ["ok", t("admin.check.browser.docs.ok")]
      : ["fail", t("admin.check.browser.docs.exposed", { list: exposed.join(", ") })]
  },

  "browser.assets": async () => {
    const [own, vendor] = await Promise.all([probe("/assets/app.js"), probe("/assets/vendor/bootstrap/bootstrap.min.css")])
    if (vendor.status !== 200 || !vendor.header("Content-Type").startsWith("text/css")) {
      return ["fail", t("admin.check.browser.assets.vendor", { status: String(vendor.status), type: vendor.header("Content-Type") })]
    }
    return own.header("Cache-Control").includes("no-cache")
      ? ["ok", t("admin.check.browser.assets.ok")]
      : ["warn", t("admin.check.browser.assets.cache", { value: own.header("Cache-Control") || "—" })]
  },

  "browser.hsts": async () => {
    if (window.location.protocol !== "https:") {
      return ["info", t("admin.check.browser.hsts.plain")]
    }
    const answer = await probe("/assets/app.js")
    return answer.header("Strict-Transport-Security") !== ""
      ? ["ok", t("admin.check.browser.hsts.ok")]
      : ["warn", t("admin.check.browser.hsts.missing")]
  },
}

function show(row, status, text) {
  const badge = row.querySelector("[data-clara-probe-status]")
  badge.className = "badge " + BADGES[status]
  badge.textContent = t("admin.check.status." + status)
  row.querySelector("[data-clara-probe-finding]").textContent = text
  row.dataset.status = status
}

ready(() => {
  document.querySelectorAll("tr[data-clara-probe]").forEach((row) => {
    const run = PROBES[row.dataset.claraProbe]
    if (run === undefined) {
      return
    }
    show(row, "pending", "")
    run()
      .then(([status, text]) => show(row, status, text))
      .catch(() => show(row, "skip", t("admin.check.browser.failed")))
  })
})
