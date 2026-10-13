// The Training page's own behaviour (User_Interface_Design.md §2.7, REQ-UI-059): bind the plans
// from this route's data region into the card grid the server rendered empty, and open a plan's
// modal — its picture gallery above its text — when its card is chosen (REQ-UI-032, REQ-UI-044).
// One module for one surface (REQ-UI-045); every label comes from the injected string block or
// the server-rendered modal (REQ-UI-008). A plan's text arrives as blocks of plain-text runs and
// is written with textContent — nothing from the plan files becomes markup (REQ-UI-004).

import { el, fetchJson, fill, ready, t } from "../app.js"

let modal = null
let plansById = new Map()

/** A block's runs as nodes: `code` runs in <code>, `strong` ones in <strong>, the rest as text. */
const runNode = (run) =>
  run.code ? el("code", null, run.text) : run.strong ? el("strong", null, run.text) : document.createTextNode(run.text)

const runNodes = (runs) => (runs || []).map(runNode)

const blockNode = (block) => {
  if (block.type === "heading") {
    return el("h3", { class: "h6 mt-3" }, runNodes(block.text))
  }
  if (block.type === "list") {
    return el(block.ordered ? "ol" : "ul", null, block.items.map((item) => el("li", null, runNodes(item))))
  }
  return el("p", null, runNodes(block.text))
}

function planCard(plan) {
  const open = el("button", { type: "button", class: "btn btn-link stretched-link p-0 text-start fw-semibold clara-training-open", dataset: { plan: plan.id } }, plan.title)
  open.addEventListener("click", () => openPlan(plan.id))

  const picture = plan.images.length
    ? el("img", { src: plan.images[0], alt: "", class: "card-img-top clara-training-cover", loading: "lazy" })
    : null

  return el("div", { class: "col" }, [
    el("div", { class: "card h-100 clara-training-card", dataset: { plan: plan.id } }, [
      picture,
      el("div", { class: "card-body" }, [
        el("h2", { class: "card-title h6 mb-1" }, open),
        el("p", { class: "card-text small text-body-secondary mb-0" }, plan.summary),
      ]),
    ]),
  ])
}

// --- the modal -----------------------------------------------------------------

function setupModal() {
  const root = document.getElementById("clara-training-modal")
  if (!root) {
    return null
  }
  const carousel = root.querySelector("#clara-training-carousel")
  const state = {
    root,
    title: root.querySelector("#clara-training-modal-title"),
    gallery: root.querySelector("[data-training-gallery]"),
    carousel,
    slides: root.querySelector("[data-training-slides]"),
    thumbs: root.querySelector("[data-training-thumbs]"),
    counter: root.querySelector("[data-training-counter]"),
    text: root.querySelector("[data-training-text]"),
    prev: root.querySelector("[data-training-prev]"),
    next: root.querySelector("[data-training-next]"),
    total: 0,
    instance: window.bootstrap.Modal.getOrCreateInstance(root),
  }

  state.prev.addEventListener("click", () => slider()?.prev())
  state.next.addEventListener("click", () => slider()?.next())
  carousel.addEventListener("slid.bs.carousel", (event) => showPosition(event.to))
  // The arrow keys page through the pictures wherever the focus is in the dialog — the
  // carousel's own keyboard handling only reacts while it holds the focus itself.
  root.addEventListener("keydown", (event) => {
    if (state.total < 2 || /input|textarea|select/i.test(event.target.tagName)) {
      return
    }
    if (event.key === "ArrowLeft") {
      slider()?.prev()
    } else if (event.key === "ArrowRight") {
      slider()?.next()
    }
  })
  // The address carries the open plan, so a plan can be linked to (/training#<plan>).
  root.addEventListener("hidden.bs.modal", () => {
    history.replaceState(null, "", location.pathname + location.search)
  })

  return state
}

const slider = () => modal && modal.total > 0 ? window.bootstrap.Carousel.getInstance(modal.carousel) : null

function showPosition(index) {
  modal.counter.textContent = t("training.gallery.counter", { n: index + 1, total: modal.total })
  Array.from(modal.thumbs.children).forEach((thumb, position) => {
    thumb.classList.toggle("active", position === index)
    thumb.setAttribute("aria-current", position === index ? "true" : "false")
  })
}

function renderGallery(plan) {
  window.bootstrap.Carousel.getInstance(modal.carousel)?.dispose()
  modal.total = plan.images.length
  modal.gallery.hidden = modal.total === 0
  if (modal.total === 0) {
    fill(modal.slides, [])
    fill(modal.thumbs, [])
    return
  }

  fill(modal.slides, plan.images.map((src, index) => {
    const alt = t("training.image_alt", { title: plan.title, n: index + 1 })
    // The picture links to itself, so a detailed screenshot can be read at full size.
    const picture = el("a", { href: src, target: "_blank", rel: "noopener" },
      el("img", { src, alt, class: "d-block mx-auto clara-training-image" }))
    return el("div", { class: "carousel-item" + (index === 0 ? " active" : "") }, picture)
  }))
  fill(modal.thumbs, plan.images.map((src, index) => {
    const thumb = el("button", { type: "button", class: "clara-training-thumb", "aria-label": t("training.gallery.thumbnail", { n: index + 1 }) },
      el("img", { src, alt: "" }))
    thumb.addEventListener("click", () => slider()?.to(index))
    return thumb
  }))

  const single = modal.total < 2
  modal.prev.hidden = single
  modal.next.hidden = single
  modal.thumbs.hidden = single
  window.bootstrap.Carousel.getOrCreateInstance(modal.carousel, { interval: false, ride: false, keyboard: false, touch: true, wrap: true })
  showPosition(0)
}

function openPlan(id) {
  const plan = plansById.get(id)
  if (!modal || !plan) {
    return
  }
  modal.title.textContent = plan.title
  renderGallery(plan)
  fill(modal.text, plan.blocks.map(blockNode))
  history.replaceState(null, "", "#" + plan.id)
  modal.instance.show()
}

/** Opens the plan the address names (/training#<plan>); plan ids need no decoding. */
function openLinked() {
  const linked = location.hash.slice(1)
  if (plansById.has(linked)) {
    openPlan(linked)
  }
}

// --- the grid --------------------------------------------------------------------

function message(text, withRetry) {
  const line = el("p", { class: "clara-region-empty mb-0" }, text)
  if (withRetry) {
    const retry = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary ms-2" }, t("js.retry"))
    retry.addEventListener("click", loadPlans)
    line.appendChild(retry)
  }
  return el("div", { class: "col-12" }, line)
}

async function loadPlans() {
  const grid = document.querySelector("[data-region='plans']")
  if (!grid) {
    return
  }

  try {
    const plans = await fetchJson("/training")
    plansById = new Map(plans.map((plan) => [plan.id, plan]))
    fill(grid, plans.length ? plans.map(planCard) : [message(t("training.empty"), false)])

    openLinked()
  } catch (error) {
    fill(grid, [message(t("js.load_failed"), true)])
  }
}

ready(() => {
  modal = setupModal()
  loadPlans()
  // A link to another plan from within the page changes only the fragment.
  window.addEventListener("hashchange", openLinked)
})
