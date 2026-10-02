// Copy-to-clipboard for the one-time values on the two-factor page (§2.5): the manual
// enrollment key and the recovery codes. A copy button is an affordance, nothing more —
// every value it copies is already on the page as selectable text, so a browser without
// this module loses nothing but the button's effect (REQ-UI-045).
//
// No UI string lives here: the label swap reads from the injected data-i18n block
// (REQ-UI-008), and the clipboard API is the only thing this touches.

import { ready, t } from "../app.js"

function labelFor(button) {
  // The original label stays on the element so a second copy, or a failed one, can put it
  // back — the DOM is the state, as everywhere else in the client code.
  if (!button.dataset.claraCopyLabel) {
    button.dataset.claraCopyLabel = button.textContent.trim()
  }
  return button.dataset.claraCopyLabel
}

function flash(button, text) {
  const original = labelFor(button)
  button.textContent = text
  window.setTimeout(() => {
    button.textContent = original
  }, 2000)
}

/** Reads the element a selector names, as plain text — never as HTML. */
function textOf(selector) {
  const source = document.querySelector(selector)
  return source ? source.textContent.trim() : ""
}

async function copy(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return true
  }
  // Insecure origin: select it instead, so the keyboard shortcut is all that is left to do.
  const source = document.createElement("textarea")
  source.value = text
  source.setAttribute("readonly", "")
  source.style.position = "fixed"
  source.style.opacity = "0"
  document.body.appendChild(source)
  source.select()
  const ok = document.execCommand("copy")
  source.remove()

  return ok
}

ready(() => {
  for (const button of document.querySelectorAll("[data-clara-copy]")) {
    button.hidden = false
    button.addEventListener("click", async () => {
      const text = textOf(button.dataset.claraCopy)
      if (text === "") {
        return
      }
      try {
        flash(button, (await copy(text)) ? t("tfa.copied") : labelFor(button))
      } catch (error) {
        // A refused clipboard is not worth an error message the user cannot act on: the
        // text is on the page either way.
        button.textContent = labelFor(button)
      }
    })
  }
})
