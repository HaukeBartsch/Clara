// The export page's behaviour (User_Interface_Design.md §6.4): the sensitivity badge follows
// the arm selection — the lowest level among the checked arms is the one the download is
// delivered at (REQ-EXP-003), so unchecking a weaker arm visibly raises it before the file
// starts — the delimiter only shows for CSV, and a download with no arm is not started. One
// module for one surface (REQ-UI-045); labels come from the data-i18n block (REQ-UI-008).
// Without it the page still works: the badge then states the level of all arms, which is
// what the form sends by default.

import { ready, t } from "../app.js"

const RANK = ["export_none", "export_de_identified", "export_no_identifiers", "export_full"]

const lowest = (levels) =>
  levels.reduce((low, level) => (low === "" || RANK.indexOf(level) < RANK.indexOf(low) ? level : low), "")

ready(() => {
  const form = document.querySelector("form[data-clara-export-form]")
  if (!form) return

  const badge = form.querySelector("[data-clara-export-level]")
  const help = form.querySelector("[data-clara-export-level-help]")
  const noArm = form.querySelector("[data-clara-export-no-arm]")
  const download = form.querySelector("[data-clara-export-download]")
  const arms = [...form.querySelectorAll("input[data-clara-export-arm]")]
  const csvOnly = form.querySelector("[data-clara-export-csv]")

  const syncLevel = () => {
    if (arms.length === 0) return // one arm: the server-rendered badge is already exact
    const level = lowest(arms.filter((arm) => arm.checked).map((arm) => arm.dataset.level))
    noArm.classList.toggle("d-none", level !== "")
    download.disabled = level === ""
    badge.classList.toggle("d-none", level === "")
    help.classList.toggle("d-none", level === "")
    if (level === "") return
    badge.dataset.level = level
    badge.textContent = t("export.level." + level)
    help.textContent = t("export.level_help." + level)
  }

  const syncFormat = () => {
    const format = form.querySelector("input[data-clara-export-format]:checked")
    csvOnly.classList.toggle("d-none", !format || format.value !== "csv")
  }

  for (const arm of arms) arm.addEventListener("change", syncLevel)
  for (const radio of form.querySelectorAll("input[data-clara-export-format]")) radio.addEventListener("change", syncFormat)
  syncLevel()
  syncFormat()
})
