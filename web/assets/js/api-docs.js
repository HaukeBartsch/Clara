// The Control Panel's API documentation section (§5.9, REQ-UI-057). One module for that
// section (REQ-UI-045); no UI string lives here — the one failure line comes from the
// injected data-i18n block (REQ-UI-008).
//
// The vendored Swagger UI bundle is a classic script that attaches SwaggerUIBundle to the
// global object, so it is loaded with a script element rather than imported; the OpenAPI
// document is this section's data region, read with the shared fetch helper (REQ-UI-044) and
// handed to Swagger UI as an object. Read-only: no "Try it out", and no validator badge —
// that would ask a server on the internet about the document (ASM-TECH-2).

import { fetchJson, ready, t } from "../app.js"

const BUNDLE = "/assets/vendor/swagger-ui/swagger-ui-bundle.js"

const loadBundle = () => new Promise((resolve, reject) => {
  if (window.SwaggerUIBundle) {
    resolve(window.SwaggerUIBundle)
    return
  }
  const script = document.createElement("script")
  script.src = BUNDLE
  script.addEventListener("load", () => resolve(window.SwaggerUIBundle))
  script.addEventListener("error", () => reject(new Error("bundle")))
  document.head.appendChild(script)
})

const render = async (root) => {
  const status = root.querySelector("[data-clara-api-docs-status]")
  try {
    const [SwaggerUIBundle, spec] = await Promise.all([loadBundle(), fetchJson(root.dataset.claraRegion)])
    const target = document.createElement("div")
    root.replaceChildren(target)
    SwaggerUIBundle({
      domNode: target,
      spec,
      supportedSubmitMethods: [],
      validatorUrl: null,
      docExpansion: "list",
      defaultModelsExpandDepth: 0,
    })
  } catch (error) {
    if (status) status.textContent = t("admin.api.failed")
  }
}

ready(() => {
  for (const root of document.querySelectorAll("[data-clara-api-docs]")) render(root)
})
