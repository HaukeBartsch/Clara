import { test as base, expect, type BrowserContext, type Page } from "@playwright/test"

// Specs act only on a page whose modules have run.
//
// Every page script is a deferred ES module that wires its behaviour from ready() (web/assets/app.js):
// the field-name warning, the reference picker, the confirmation dialogs, the switches that save at
// once. A click that opens a page returns as soon as the new document commits, so the next fill or
// check can land before that wiring exists — the input event reaches no listener and the spec fails
// on behaviour that was simply not loaded yet. Under parallel load it happened often enough to
// fail a full run on every attempt.
//
// app.js marks <html data-clara-ready> once the page's modules have run. Until then this script keeps
// aria-disabled="true" on <html>; Playwright counts every element below an aria-disabled one as
// disabled, so click, fill, check and selectOption wait for the marker on their own — after every
// navigation, without a wait written into each step. Two consequences to keep in mind:
//   - a page that never sets the marker is released anyway, RELEASE_AFTER_LOAD_MS after its load
//     event, with a console warning: the marker follows DOMContentLoaded by one task, so by then a
//     module has failed to load (or never will run), and the page behaves as it would without this
//     fixture — a spec that needs the module then fails on that behaviour, and one that does not
//     goes on, instead of every action hanging until the test timeout;
//   - toBeDisabled() is satisfied by the hold as well, so assert it only after an action on the same
//     page, which has waited for the marker (as export.spec.ts and assistant.spec.ts do), or after
//     pageReady() (translations.spec.ts).
// A document without module scripts is released at DOMContentLoaded: it has nothing to wait for.

const RELEASE_AFTER_LOAD_MS = 2000

const holdUntilReadyScript = (releaseAfterLoadMs: number): void => {
  const hold = (root: HTMLElement): void => {
    if (root.hasAttribute("data-clara-ready")) return
    root.setAttribute("aria-disabled", "true")
    const observer = new MutationObserver(() => {
      if (root.hasAttribute("data-clara-ready")) release()
    })
    const release = (): void => {
      observer.disconnect()
      root.removeAttribute("aria-disabled")
    }
    observer.observe(root, { attributes: true, attributeFilter: ["data-clara-ready"] })
    document.addEventListener("DOMContentLoaded", () => {
      if (document.querySelector('script[type="module"]') === null) release()
    }, { once: true })
    window.addEventListener("load", () => {
      setTimeout(() => {
        if (root.hasAttribute("data-clara-ready") || !root.hasAttribute("aria-disabled")) return
        console.warn(`clara fixture: no data-clara-ready ${releaseAfterLoadMs} ms after load on ${location.pathname} — a page module did not run; releasing the hold`)
        release()
      }, releaseAfterLoadMs)
    }, { once: true })
  }
  if (document.documentElement) {
    hold(document.documentElement)
    return
  }
  // The init script can run before the parser has created <html>.
  new MutationObserver((_, observer) => {
    if (!document.documentElement) return
    observer.disconnect()
    hold(document.documentElement)
  }).observe(document, { childList: true })
}

/** Holds actions on every page of `context` until that page's modules have run. */
export async function holdUntilReady(context: BrowserContext): Promise<BrowserContext> {
  await context.addInitScript(holdUntilReadyScript, RELEASE_AFTER_LOAD_MS)
  return context
}

/** Waits until the page's modules have run — for the steps no actionability check covers (press, focus). */
export async function pageReady(page: Page): Promise<void> {
  await expect(page.locator("html[data-clara-ready]"), "the page's modules never ran (app.js)").toHaveCount(1)
}

/** `test` whose default context holds actions until each page is ready. */
export const test = base.extend({
  context: async ({ context }, use) => {
    await use(await holdUntilReady(context))
  },
})

export { expect }
