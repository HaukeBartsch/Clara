// The record view's behaviour (User_Interface_Design.md §8). One module for that view
// (REQ-UI-045); every label comes from the injected data-i18n block (REQ-UI-008). What the
// form shares with the survey page — branching, advisory validation, the required-field
// check, clearing a radio, the timezone — lives in form.js; this module adds what only the
// record view has:
//
// 1. Per-field history (§8.3, REQ-UI-026): a read-only modal fed by the route's `history`
//    data region (REQ-UI-044), paged with the API cursor.
// 2. The event picker submitting on change.

import { fillTimezone, readContext, wireBranching, wireHints, wireRadioReset, wireRequired } from "./form.js"
import { el, fetchJson, ready, t } from "../app.js"

// --- 1. per-field history ---

let historyModal = null

function historyDialog() {
  if (historyModal) return historyModal
  const title = el("h2", { class: "modal-title h6" })
  const body = el("tbody")
  const more = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary d-none" }, t("record.history.more"))
  const root = el("div", { class: "modal fade", tabindex: "-1", "aria-hidden": "true", id: "clara-history" }, [
    el("div", { class: "modal-dialog modal-lg modal-dialog-scrollable" }, [
      el("div", { class: "modal-content" }, [
        el("div", { class: "modal-header" }, [
          title,
          el("button", { type: "button", class: "btn-close", "data-bs-dismiss": "modal", "aria-label": t("action.close") }),
        ]),
        el("div", { class: "modal-body" }, [
          el("div", { class: "table-responsive" }, [
            el("table", { class: "table table-sm align-middle mb-2 clara-history" }, [
              el("thead", null, el("tr", null, [
                el("th", { scope: "col" }, t("record.history.when")),
                el("th", { scope: "col" }, t("record.history.who")),
                el("th", { scope: "col" }, t("record.history.change")),
              ])),
              body,
            ]),
          ]),
          more,
        ]),
      ]),
    ]),
  ])
  document.body.appendChild(root)
  historyModal = { root, title, body, more, instance: window.bootstrap.Modal.getOrCreateInstance(root), cursor: null, url: "" }
  more.addEventListener("click", () => loadHistory(false))
  return historyModal
}

const historyRow = (entry) => {
  // Values are written as text, never as markup (REQ-UI-004); a deletion shows the deleted
  // value on the old side (REQ-AUD-009).
  const change = el("td", { class: "clara-history-change" }, [
    el("span", { class: "badge text-bg-light border me-1" }, t("record.history.action." + entry.action)),
    el("span", { class: "text-body-secondary" }, entry.old ?? "—"),
    " → ",
    el("span", null, entry.new ?? "—"),
  ])
  return el("tr", null, [
    el("td", { class: "text-nowrap small" }, entry.created_at),
    el("td", { class: "small" }, entry.user || t("record.history.nobody")),
    change,
  ])
}

const historyNotice = (text) => el("tr", null, el("td", { colspan: "3", class: "clara-region-empty" }, text))

async function loadHistory(first) {
  const dialog = historyDialog()
  if (first) dialog.body.replaceChildren(historyNotice(t("js.loading")))
  dialog.more.classList.add("d-none")
  try {
    const page = await fetchJson(dialog.url + (dialog.cursor ? "&cursor=" + encodeURIComponent(dialog.cursor) : ""))
    if (first) dialog.body.replaceChildren()
    for (const entry of page.entries || []) dialog.body.appendChild(historyRow(entry))
    dialog.cursor = page.next_cursor || null
    if (dialog.cursor) dialog.more.classList.remove("d-none")
    if (!dialog.cursor && dialog.body.children.length === 0) dialog.body.appendChild(historyNotice(t("record.history.empty")))
  } catch (error) {
    dialog.body.appendChild(historyNotice(t("js.load_failed")))
  }
}

function wireHistory(form) {
  for (const button of document.querySelectorAll("[data-clara-history]")) {
    button.addEventListener("click", () => {
      const dialog = historyDialog()
      const field = button.dataset.claraHistory
      dialog.title.textContent = t("record.history.title") + " — " + field
      dialog.url = form.dataset.history + "?field=" + encodeURIComponent(field) + "&event=" + encodeURIComponent(form.dataset.event)
      dialog.cursor = null
      dialog.instance.show()
      loadHistory(true)
    })
  }
}

// --- 2. the event picker ---

function wireEventPicker() {
  for (const picker of document.querySelectorAll("form[data-clara-autosubmit]")) {
    picker.querySelector("[data-clara-autosubmit-button]")?.classList.add("d-none")
    picker.querySelector("select")?.addEventListener("change", () => picker.requestSubmit())
  }
}


ready(() => {
  wireEventPicker()
  const form = document.querySelector("form[data-clara-record-form]")
  if (!form) return

  fillTimezone(form)
  wireBranching(form, readContext())
  wireHints(form)
  wireRequired(form)
  wireRadioReset(form)
  wireHistory(form)
})
