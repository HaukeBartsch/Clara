// The shared client runtime (REQ-UI-045, User_Interface_Design.md §3.7).
//
// Three jobs and nothing else: talk to *this application's* PHP routes (never
// /api/v1/* or the data API — the browser has no path to them, REQ-UI-002), bind
// the JSON those routes serve into data regions, and read the UI strings the
// server injected. A section module under assets/js/ imports from here; it never
// carries a UI string of its own (REQ-UI-045) and no page loads a script that it
// does not use.
//
// Vanilla ES2020, ES modules loaded by relative path with <script type="module">:
// no framework, no jQuery, no bundler, no import map (REQ-UI-001, REQ-TECH-001).
// Everything is written with createElement/textContent — innerHTML with server
// data is forbidden (REQ-UI-004); the one allowlist-HTML case stays on the server.

const stringsElement = document.querySelector("script[data-i18n]")
const strings = stringsElement ? JSON.parse(stringsElement.textContent || "{}") : {}

/** A UI string injected by the server, with {placeholders} substituted. */
export function t(key, params) {
  let text = strings[key] !== undefined ? strings[key] : key
  if (params) {
    for (const name of Object.keys(params)) {
      text = text.split("{" + name + "}").join(String(params[name]))
    }
  }
  return text
}

/** The per-session CSRF token, present on authenticated pages. */
export function csrfToken() {
  const meta = document.querySelector("meta[name=csrf-token]")
  return meta ? meta.getAttribute("content") || "" : ""
}

/**
 * GETs one of this application's routes and reads its data region — the same URL
 * that served the page, selected by Accept (REQ-UI-044). Throws with the API's
 * stable error code so a caller can render the translated line for it.
 */
export async function fetchJson(path) {
  const response = await fetch(path, {
    method: "GET",
    headers: { Accept: "application/json" },
    credentials: "same-origin",
  })

  if (response.status === 403 || response.status === 401) {
    // Session gone or permission refused: reload so the server's guard decides,
    // rather than rendering a page state the router would never have produced.
    throw new Error("forbidden")
  }

  const body = await readBody(response)
  if (!response.ok) {
    throw new Error(body.error || "internal")
  }

  return body
}

/** POSTs a mutation to a page route with ?action=<name> and the CSRF header. */
export async function postAction(path, action, fields) {
  const form = new URLSearchParams(fields || {})
  const response = await fetch(path + (path.includes("?") ? "&" : "?") + "action=" + encodeURIComponent(action), {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded",
      "X-CSRF-Token": csrfToken(),
    },
    credentials: "same-origin",
    body: form.toString(),
  })

  const body = await readBody(response)
  if (!response.ok) {
    throw new Error(body.error || "internal")
  }

  return body
}

async function readBody(response) {
  const text = await response.text()
  if (text === "") {
    return {}
  }
  try {
    return JSON.parse(text)
  } catch (error) {
    // An HTML body where JSON was expected means a proxy or error page answered.
    throw new Error("internal")
  }
}

/** Creates an element with attributes and children. Text is always textContent. */
export function el(tag, attributes, children) {
  const node = document.createElement(tag)
  if (attributes) {
    for (const name of Object.keys(attributes)) {
      const value = attributes[name]
      if (value === null || value === undefined || value === false) {
        continue
      }
      if (name === "class") {
        node.className = String(value)
        continue
      }
      if (name === "dataset") {
        Object.assign(node.dataset, value)
        continue
      }
      node.setAttribute(name, String(value))
    }
  }
  appendAll(node, children)

  return node
}

/** A <td> holding text — the common case, and impossible to inject into. */
export function td(text, className) {
  const cell = document.createElement("td")
  cell.textContent = text === null || text === undefined ? "" : String(text)
  if (className) {
    cell.className = className
  }
  return cell
}

/**
 * Empties a container and fills it. `items` are mapped through `renderItem` when
 * one is given; otherwise entries that are already nodes are appended as they are,
 * which is how a caller inserts a placeholder row.
 */
export function fill(target, items, renderItem) {
  if (!target) {
    return
  }
  target.replaceChildren()
  for (const item of items || []) {
    const node = renderItem ? renderItem(item) : item
    if (node) {
      target.appendChild(node)
    }
  }
}

/** A placeholder row or paragraph saying the region is empty, failed, or loading. */
export function notice(colspan, message, modifier) {
  const cell = el("td", { colspan: String(colspan), class: "clara-region-empty" + (modifier ? " " + modifier : "") })
  cell.textContent = message

  return el("tr", { class: "clara-region-notice" }, cell)
}

function appendAll(node, children) {
  if (children === null || children === undefined) {
    return
  }
  for (const child of Array.isArray(children) ? children : [children]) {
    if (child === null || child === undefined || child === false) {
      continue
    }
    node.appendChild(typeof child === "string" || typeof child === "number" ? document.createTextNode(String(child)) : child)
  }
}

/** Runs `fn` once the DOM is parsed — section modules use it as their entry point. */
export function ready(fn) {
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", fn, { once: true })
    return
  }
  fn()
}
