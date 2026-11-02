// The two-factor surfaces (§2.5): the settings page and the enrollment wizard a mandate opens
// before login completes (§2.2). Two enhancements, neither of them required:
//
// - Copy-to-clipboard for the one-time values: the manual enrollment key and the recovery codes.
//   Every value a button copies is already on the page as selectable text, so a browser without
//   this module loses nothing but the button's effect (REQ-UI-045).
// - The enrollment QR code (REQ-AUTH-056, REQ-UI-039), drawn in the browser from the
//   `otpauth://` URI the page already carries. The panel stays hidden until it is drawn, so
//   without this module — or if the library fails — the manual key below it is the way in.
//
// No UI string lives here: the label swap reads from the injected data-i18n block, and the QR
// panel's accessible name is rendered server-side (REQ-UI-008).

import { ready, t } from "../app.js"

// qrcodejs 1.0.0 (Technology_Stack_Design.md §3), vendored. It is a classic script that assigns
// the global `QRCode` — and is sloppy-mode code — so it is loaded with a script element, the
// d3/Swagger precedent, never imported as a module.
const QR_LIBRARY = "/assets/vendor/qrcodejs/qrcode.min.js"

// Canvas resolution in device pixels; app.css scales the result into its panel, which keeps it
// sharp on high-density screens.
const QR_PIXELS = 512

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

function wireCopyButtons() {
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
}

const loadScript = (src) => new Promise((resolve, reject) => {
  const script = document.createElement("script")
  script.src = src
  script.addEventListener("load", resolve)
  script.addEventListener("error", () => reject(new Error(src)))
  document.head.appendChild(script)
})

/** Draws the URI a `data-clara-qr` panel carries, then shows the panel. */
function drawQr(panel, QRCode) {
  const text = panel.dataset.claraQr
  if (!text) {
    return
  }
  // Black on white whatever the theme: authenticator apps read dark modules on a light ground,
  // and the panel's white padding is the quiet zone a scanner needs around them (app.css).
  new QRCode(panel, {
    text,
    width: QR_PIXELS,
    height: QR_PIXELS,
    colorDark: "#000000",
    colorLight: "#ffffff",
    correctLevel: QRCode.CorrectLevel.M,
  })
  // The library copies its text into a title attribute, which would put the secret into a
  // hover tooltip. The panel's role="img" names it; its drawing is presentational.
  panel.removeAttribute("title")
  for (const image of panel.querySelectorAll("img")) {
    image.alt = ""
  }
  panel.hidden = false
}

async function drawQrCodes() {
  const panels = document.querySelectorAll("[data-clara-qr]")
  if (panels.length === 0) {
    return
  }
  try {
    await loadScript(QR_LIBRARY)
    for (const panel of panels) {
      drawQr(panel, window.QRCode)
    }
  } catch (error) {
    // The manual key is on the page either way; a code that cannot be drawn stays hidden
    // rather than half-drawn.
    for (const panel of panels) {
      panel.hidden = true
    }
  }
}

ready(() => {
  wireCopyButtons()
  drawQrCodes()
})
