import { defineConfig, devices } from "@playwright/test"

// Playwright configuration for the CLARA web layer (REQ-TECH-028).
// Development and CI tooling only — nothing in this tree is served
// (Technology_Stack_Design.md §2/§6). Chromium is the single project; the UI has to work
// without a build step or CDN, so one engine keeps the harness honest and cheap.
export default defineConfig({
  testDir: "./tests",
  // macOS writes AppleDouble ._ companion files on non-HFS volumes (exFAT/FAT); they are not tests.
  testIgnore: /(^|\/)\._/,
  fullyParallel: true,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    // PHP application under test — set CLARA_WEB_URL once web/public exists (§8 of the plan).
    baseURL: process.env.CLARA_WEB_URL ?? "http://127.0.0.1:8080",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
})
