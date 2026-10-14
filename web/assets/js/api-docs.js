// The Control Panel's API documentation section (§5.9, REQ-UI-057, REQ-UI-060). One module for
// that section (REQ-UI-045); no UI string lives here — the failure line and the two tooltip
// patterns come from the injected data-i18n block, and every node label is translated server-side
// into the embedded payload (REQ-UI-008).
//
// The vendored Swagger UI bundle is a classic script that attaches SwaggerUIBundle to the global
// object, so it is loaded with a script element rather than imported; the OpenAPI document is
// this section's data region, read with the shared fetch helper (REQ-UI-044) and handed to
// Swagger UI as an object. Read-only: no "Try it out", and no validator badge — that would ask a
// server on the internet about the document (ASM-TECH-2).
//
// The relevance Sankey above it is drawn by the vendored d3 + d3-sankey pair — also classic
// scripts: d3 attaches `d3` to the global object and d3-sankey's UMD wrapper extends that same
// global with `sankey`, so they load in order. Its structure is embedded in the page as JSON
// (generated from the classification document; the browser fetches nothing for it), and a missing
// or unreadable payload simply leaves the block out — the OpenAPI document below renders either way.

import { fetchJson, ready, t } from "../app.js"

const BUNDLE = "/assets/vendor/swagger-ui/swagger-ui-bundle.js"
const D3_BUNDLE = "/assets/vendor/d3/d3.v7.min.js"
const D3_SANKEY_BUNDLE = "/assets/vendor/d3/d3-sankey.min.js"

// The five relevance classes' fixed colours (REQ-UI-060): mid-saturation hues that keep their
// contrast on both the light and the dark themes, matching the legend dots to the bands.
const CLASS_COLORS = {
  security: "#cf4b50",
  performance: "#d98e32",
  workflow: "#4a86c8",
  scalability: "#3ba07f",
  resilience: "#9073c9",
}

const loadScript = (src) => new Promise((resolve, reject) => {
  const script = document.createElement("script")
  script.src = src
  script.addEventListener("load", resolve)
  script.addEventListener("error", () => reject(new Error(src)))
  document.head.appendChild(script)
})

// The section an administrator comes here for (§5.9): listed first and the only one open; the
// others keep the document's order, collapsed.
const OPEN_TAG = "Data API"

const tagOrder = (spec) => {
  const names = (spec.tags ?? []).map((tag) => tag.name)
  const rank = (name) => (name === OPEN_TAG ? -1 : names.includes(name) ? names.indexOf(name) : names.length)
  return (a, b) => rank(a) - rank(b)
}

const loadBundle = () => new Promise((resolve, reject) => {
  if (window.SwaggerUIBundle) {
    resolve(window.SwaggerUIBundle)
    return
  }
  loadScript(BUNDLE).then(() => resolve(window.SwaggerUIBundle), reject)
})

// d3-sankey extends the global `d3` at load time, so the second script only resolves once the
// first one has run.
const loadSankeyLibs = () => {
  if (window.d3 && window.d3.sankey) {
    return Promise.resolve(window.d3)
  }

  return loadScript(D3_BUNDLE)
    .then(() => loadScript(D3_SANKEY_BUNDLE))
    .then(() => window.d3)
}

const render = async (root) => {
  const status = root.querySelector("[data-clara-api-docs-status]")
  try {
    const [SwaggerUIBundle, spec] = await Promise.all([loadBundle(), fetchJson(root.dataset.claraRegion)])
    const target = document.createElement("div")
    root.replaceChildren(target)
    const ui = SwaggerUIBundle({
      domNode: target,
      spec,
      supportedSubmitMethods: [],
      validatorUrl: null,
      docExpansion: "none",
      tagsSorter: tagOrder(spec),
      defaultModelsExpandDepth: 0,
      onComplete: () => ui.layoutActions.show(["operations-tag", OPEN_TAG], true),
    })
  } catch (error) {
    if (status) status.textContent = t("admin.api.failed")
  }
}

// --- the relevance Sankey (REQ-UI-060) ----------------------------------------------------

const classColor = (node) => CLASS_COLORS[node.class] || "#8a93a6"

/** The node's tooltip line: what it is, and how many codes stand behind it. */
const nodeTooltip = (node) => {
  const parts = [node.kind ? `${node.label} · ${node.kind}` : node.label]
  if (node.codes !== undefined) parts.push(t("admin.api.sankey.tooltip.codes", { count: node.codes }))
  if (node.desc) parts.push(node.desc)

  return parts.join("\n")
}

const renderSankey = async (canvas, payload) => {
  const d3 = await loadSankeyLibs()
  // Geist arrives with the stylesheet; measuring before it is ready would sort on fallback metrics.
  await document.fonts.ready

  canvas.replaceChildren()
  const width = Math.max(canvas.clientWidth || 960, 720)
  const groups = payload.nodes.filter((node) => node.column === 1).length
  const height = Math.max(560, groups * 22 + 48)
  const margin = { left: 200, right: 120, top: 10, bottom: 10 }

  // A proportional font makes character count no proxy for rendered length, so each area label is
  // measured once in pixels — in a scratch svg carrying the diagram's own class, under the same
  // stylesheet rules as the real labels. Added and removed within this task: nothing paints it.
  const widths = new Map()
  const scratch = d3.select(canvas).append("svg").attr("class", "clara-sankey")
  for (const node of payload.nodes) {
    if (node.column === 0) {
      const text = scratch.append("text").attr("class", "clara-sankey-label").text(node.label)
      widths.set(node.id, text.node().getComputedTextLength())
    }
  }
  scratch.remove()

  const layout = d3.sankey()
    .nodeId((node) => node.id)
    .nodeWidth(14)
    .nodePadding(Math.max(4, Math.min(10, height / groups / 3)))
    // The generated `order` keeps families grouped under their area and the classes in the
    // classification's own order; the areas themselves stack widest label first, so the left
    // column reads as a descending staircase. d3-sankey applies this comparator within each
    // column and skips its own relaxation re-sort — the same picture on every render.
    .nodeSort((a, b) => (a.column === 0 ? widths.get(b.id) - widths.get(a.id) || a.order - b.order : a.order - b.order))
    .extent([[margin.left, margin.top], [width - margin.right, height - margin.bottom]])

  const graph = layout({
    nodes: payload.nodes.map((node) => ({ ...node })),
    links: payload.links.map((link) => ({ ...link })),
  })

  // The SVG is decorative next to its labelled section and the classification table it mirrors;
  // aria-hidden keeps screen readers on the text, which carries every value.
  const svg = d3.select(canvas).append("svg")
    .attr("class", "clara-sankey")
    .attr("viewBox", `0 0 ${width} ${height}`)
    .attr("width", width)
    .attr("height", height)
    .attr("aria-hidden", "true")

  const label = (id) => graph.nodes.find((node) => node.id === id).label

  const link = svg.append("g").selectAll("path").data(graph.links).join("path")
    .attr("class", "clara-sankey-link")
    .attr("d", d3.sankeyLinkHorizontal())
    .attr("stroke", (l) => classColor(l.target))
    .attr("stroke-width", (l) => Math.max(1, l.width))
    .attr("data-source", (l) => l.source.id)
    .attr("data-target", (l) => l.target.id)
  link.append("title").text((l) => t("admin.api.sankey.tooltip.link", { from: label(l.source.id), to: label(l.target.id), count: l.value }))

  const node = svg.append("g").selectAll("g").data(graph.nodes).join("g")
    .attr("class", (d) => `clara-sankey-node clara-sankey-col-${d.column}`)
    .attr("data-id", (d) => d.id)
  node.append("rect")
    .attr("x", (d) => d.x0)
    .attr("y", (d) => d.y0)
    .attr("width", (d) => d.x1 - d.x0)
    .attr("height", (d) => Math.max(1, d.y1 - d.y0))
    .attr("fill", (d) => (d.column === 2 ? classColor(d) : null))
  node.append("title").text(nodeTooltip)

  const text = (d) => {
    const left = d.column === 0
    return {
      x: left ? d.x0 - 8 : d.x1 + 8,
      y: (d.y0 + d.y1) / 2,
      anchor: left ? "end" : "start",
    }
  }
  node.append("text")
    .attr("x", (d) => text(d).x)
    .attr("y", (d) => text(d).y)
    .attr("dy", "0.35em")
    .attr("text-anchor", (d) => text(d).anchor)
    .attr("class", (d) => (d.column === 1 ? "clara-sankey-label clara-sankey-label-group" : "clara-sankey-label"))
    .text((d) => d.label)

  // Hover: dim everything, light up the hovered node's own flows.
  const highlight = (event) => {
    const hoveredLink = event.target.closest("path.clara-sankey-link")
    const hoveredNode = event.target.closest("g.clara-sankey-node")
    if (!hoveredLink && !hoveredNode) return
    svg.selectAll("path.is-active").classed("is-active", false)
    canvas.classList.add("clara-sankey-hover")
    if (hoveredLink) {
      svg.selectAll(`path[data-source="${hoveredLink.dataset.source}"][data-target="${hoveredLink.dataset.target}"]`).classed("is-active", true)
    } else {
      const id = hoveredNode.dataset.id
      svg.selectAll(`path[data-source="${id}"], path[data-target="${id}"]`).classed("is-active", true)
    }
  }
  const clear = () => {
    canvas.classList.remove("clara-sankey-hover")
    svg.selectAll("path.is-active").classed("is-active", false)
  }
  svg.node().addEventListener("mouseover", highlight)
  svg.node().addEventListener("mouseout", clear)

  // A legend of the five classes, built from the payload so it follows the translated labels.
  const legend = document.createElement("div")
  legend.className = "clara-sankey-legend small"
  for (const cls of graph.nodes.filter((n) => n.column === 2)) {
    const chip = document.createElement("span")
    chip.className = "clara-sankey-legend-item"
    const dot = document.createElement("span")
    dot.className = "clara-sankey-legend-dot"
    dot.style.backgroundColor = classColor(cls)
    chip.append(dot, cls.label)
    legend.append(chip)
  }
  canvas.prepend(legend)
}

const renderSankeyBlock = async (canvas) => {
  const data = document.querySelector("script[data-clara-sankey-data]")
  if (!data || canvas.dataset.claraRendered === "1") return
  let payload
  try {
    payload = JSON.parse(data.textContent)
  } catch (error) {
    console.warn("relevance sankey: payload unreadable", error)
    return
  }

  const draw = () => renderSankey(canvas, payload).catch((error) => console.warn("relevance sankey:", error))
  canvas.dataset.claraRendered = "1"
  await draw()

  let width = canvas.clientWidth
  window.addEventListener("resize", () => {
    if (canvas.clientWidth !== width) {
      width = canvas.clientWidth
      draw()
    }
  })
}

ready(() => {
  for (const root of document.querySelectorAll("[data-clara-api-docs]")) render(root)
  for (const canvas of document.querySelectorAll("[data-clara-relevance-sankey]")) renderSankeyBlock(canvas)
})
