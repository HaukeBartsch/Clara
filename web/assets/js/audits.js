// The Audits section's Export control (§5.6, REQ-UI-057): the button submits the filter form
// with download=1, so the export carries exactly the filters on screen. Before it submits,
// audits.js prices the same filters with estimate=1; above 1 GiB a modal names the estimated
// size and only starts the download on confirm (REQ-AUD-032). The estimate is advisory — any
// failure of it starts the export without a warning rather than blocking it. Without this
// module the button still downloads; only the warning is lost. No UI string lives here —
// every label comes from the injected data-i18n block (REQ-UI-008).

import { el, fetchJson, ready, t } from "../app.js"

const GIB = 1073741824

// Set once the user has confirmed (or no warning was owed): the next submit goes through.
let proceed = false

/** The same filter form's values priced by the estimate endpoint (`estimate=1`). */
function estimateQuery(form) {
  const params = new URLSearchParams(new FormData(form))
  params.delete("download")
  params.set("estimate", "1")
  return "/admin?" + params.toString()
}

/** A human-readable size for the modal: GiB, one decimal — the threshold is binary too. */
function formatSize(bytes) {
  return (bytes / GIB).toFixed(1) + " GiB"
}

function confirmExport(bytes, form, submitter) {
  const cancel = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary", "data-bs-dismiss": "modal" }, t("action.cancel"))
  const ok = el("button", { type: "button", class: "btn btn-sm btn-warning" }, t("admin.audits.export.confirm"))
  const root = el("div", { class: "modal fade", tabindex: "-1", "aria-hidden": "true", id: "clara-audit-export-warning" }, [
    el("div", { class: "modal-dialog modal-dialog-centered" }, [
      el("div", { class: "modal-content" }, [
        el("div", { class: "modal-header" }, [el("h2", { class: "modal-title h6" }, t("admin.audits.export.warning_title"))]),
        el("div", { class: "modal-body" }, [
          el("p", { class: "mb-0" }, t("admin.audits.export.warning_body", { size: formatSize(bytes) })),
        ]),
        el("div", { class: "modal-footer" }, [cancel, ok]),
      ]),
    ]),
  ])
  document.body.appendChild(root)
  root.addEventListener("hidden.bs.modal", () => root.remove())

  ok.addEventListener("click", () => {
    window.bootstrap.Modal.getInstance(root)?.hide()
    go(form, submitter)
  })
  window.bootstrap.Modal.getOrCreateInstance(root).show()
}

/** Lets the intercepted submit through: requestSubmit re-fires with the export button as
 *  its submitter, so download=1 rides along like on an un-javascripted click. */
function go(form, submitter) {
  proceed = true
  form.requestSubmit(submitter)
}

ready(() => {
  const form = document.querySelector("form.clara-audit-filters")
  if (form === null) {
    return
  }
  form.addEventListener("submit", (event) => {
    const submitter = event.submitter
    if (submitter === null || submitter.name !== "download" || proceed) {
      return
    }
    event.preventDefault()
    fetchJson(estimateQuery(form))
      .then((estimate) => {
        const bytes = Number(estimate?.estimated_bytes ?? 0)
        if (bytes > GIB) {
          confirmExport(bytes, form, submitter)
        } else {
          go(form, submitter)
        }
      })
      .catch(() => go(form, submitter))
  })
})
