// The administration pages' behaviour — the Control Panel (§5) and the project page's
// Members / Roles / Groups (§5.3–§5.5). One module for that logical section (REQ-UI-045); no
// UI string lives here — every label comes from the injected data-i18n block (REQ-UI-008).
//
// 1. Confirmations (§3.5): a form carrying data-clara-confirm="<consequence>" opens a
//    Bootstrap modal stating it; only the modal's confirm button submits the form, so the
//    CSRF token rides with the confirm click like every other mutation (§3.3).
// 2. Copy (§3.5): a button carrying data-clara-copy="<selector>" copies that element's value
//    — the once-shown project token.
// 3. Reopening (§6.7/§6.8): a server-rendered modal carrying data-clara-autoshow opens on load —
//    the commit dialog after a refused commit, the breaking-change question after a save the
//    API sent back for acknowledgement.

import { el, ready, t } from "../app.js"

let modal = null
let pending = null

function confirmModal() {
  if (modal) return modal

  const message = el("p", { class: "mb-0", "data-clara-confirm-message": "" })
  const cancel = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary", "data-bs-dismiss": "modal" }, t("action.cancel"))
  const ok = el("button", { type: "button", class: "btn btn-sm btn-danger", "data-clara-confirm-ok": "" }, t("admin.confirm.ok"))
  const root = el("div", { class: "modal fade", tabindex: "-1", "aria-hidden": "true", id: "clara-confirm" }, [
    el("div", { class: "modal-dialog modal-dialog-centered" }, [
      el("div", { class: "modal-content" }, [
        el("div", { class: "modal-header" }, [el("h2", { class: "modal-title h6" }, t("admin.confirm.title"))]),
        el("div", { class: "modal-body" }, [message]),
        el("div", { class: "modal-footer" }, [cancel, ok]),
      ]),
    ]),
  ])
  document.body.appendChild(root)

  ok.addEventListener("click", () => {
    const form = pending
    pending = null
    if (!form) return
    form.dataset.claraConfirmed = "1"
    form.requestSubmit()
  })

  modal = { root, message, instance: window.bootstrap.Modal.getOrCreateInstance(root) }
  return modal
}

function askFirst(event) {
  const form = event.target
  if (!(form instanceof HTMLFormElement) || !form.dataset.claraConfirm) return
  if (form.dataset.claraConfirmed === "1") return

  event.preventDefault()
  const dialog = confirmModal()
  dialog.message.textContent = form.dataset.claraConfirm
  pending = form
  dialog.instance.show()
}

async function copyFrom(selector) {
  const source = document.querySelector(selector)
  const text = source ? (source.value ?? source.textContent ?? "") : ""
  if (text === "") return false
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return true
  }
  // Plain-HTTP development stacks have no async clipboard: select and copy instead.
  if (source.select) source.select()
  return document.execCommand("copy")
}

ready(() => {
  document.addEventListener("submit", askFirst, true)

  for (const dialog of document.querySelectorAll(".modal[data-clara-autoshow]")) {
    window.bootstrap.Modal.getOrCreateInstance(dialog).show()
  }

  for (const button of document.querySelectorAll("[data-clara-copy]")) {
    const label = button.textContent
    button.addEventListener("click", async () => {
      try {
        if (await copyFrom(button.dataset.claraCopy)) {
          button.textContent = t("tfa.copied")
          setTimeout(() => { button.textContent = label }, 1500)
        }
      } catch (error) {
        // A refused clipboard needs no message: the value is on the page, selected.
      }
    })
  }
})
