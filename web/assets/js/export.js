// The export page's behaviour (User_Interface_Design.md §6.4): the sensitivity badge follows
// the arm selection — the most protective level among the checked arms is the one the download
// states, so unchecking an arm visibly raises it before the file starts — alongside the count
// of instrument-and-event combinations those arms leave out for want of a permission
// (Data_Export_Anonymization_Design.md §4.3). The CSV options — delimiter, decimal separator,
// byte-order mark — only show for CSV, and a download with no arm is not started. One module
// for one surface (REQ-UI-045); labels come from the data-i18n block (REQ-UI-008). Without it
// the page still works: the badge then states the level of all arms, which is what the form
// sends by default.

import { ready, t } from "../app.js"

const RANK = ["export_none", "export_de_identified", "export_no_identifiers", "export_full"]

const lowest = (levels) =>
  levels.reduce((low, level) => (low === "" || RANK.indexOf(level) < RANK.indexOf(low) ? level : low), "")

ready(() => {
  const form = document.querySelector("form[data-clara-export-form]")
  if (!form) return

  const badge = form.querySelector("[data-clara-export-level]")
  const help = form.querySelector("[data-clara-export-level-help]")
  const dropped = form.querySelector("[data-clara-export-dropped]")
  const noArm = form.querySelector("[data-clara-export-no-arm]")
  const download = form.querySelector("[data-clara-export-download]")
  const arms = [...form.querySelectorAll("input[data-clara-export-arm]")]
  const csvOnly = [...form.querySelectorAll("[data-clara-export-csv]")]

  const syncLevel = () => {
    if (arms.length === 0) return // one arm: the server-rendered badge is already exact
    const chosen = arms.filter((arm) => arm.checked)
    const level = lowest(chosen.map((arm) => arm.dataset.level))
    noArm.classList.toggle("d-none", level !== "")
    download.disabled = level === ""
    badge.classList.toggle("d-none", level === "")
    help.classList.toggle("d-none", level === "")
    if (level === "") return
    badge.dataset.level = level
    badge.textContent = t("export.level." + level)
    help.textContent = t("export.level_help." + level)

    // What the selection leaves out is part of what it delivers (§6.4): an arm whose
    // pairs sit outside the member's permission says so, and unchecking it takes that
    // sentence away with it.
    if (dropped) {
      const count = chosen.reduce((sum, arm) => sum + Number(arm.dataset.dropped || 0), 0)
      dropped.classList.toggle("d-none", count === 0)
      dropped.textContent = count === 0 ? "" : t("export.dropped_pairs", { count })
    }
  }

  const syncFormat = () => {
    const format = form.querySelector("input[data-clara-export-format]:checked")
    for (const el of csvOnly) el.classList.toggle("d-none", !format || format.value !== "csv")
  }

  for (const arm of arms) arm.addEventListener("change", syncLevel)
  for (const radio of form.querySelectorAll("input[data-clara-export-format]")) radio.addEventListener("change", syncFormat)
  syncLevel()
  syncFormat()
})
