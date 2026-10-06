// The role editor's matrix (§5.4, REQ-UI-014). Three behaviours, all assistance: the submitted
// form carries the same values without them and the API decides at save (REQ-AUTH-033).
//
// 1. A right on a `no_access` row does nothing, so its checkboxes are rendered disabled with the
//    reason as their title rather than silently ignored (REQ-UI-003) — this keeps that state true
//    while the admin moves a row between levels.
// 2. Per-column bulk setting: a project's pair count is unbounded, so one click sets every row of
//    the arm tab to one value.
// 3. The dirty marker: all arm tabs submit together, so a change in a hidden tab is marked on its
//    tab label (User_Interface_Design.md §5.4).

import { ready, t } from "../app.js"

const rightsOf = (row) => [...row.querySelectorAll("[data-clara-role-cell=delete_values], [data-clara-role-cell=edit_surveys]")]

// A right belongs to a pair that is readable; denial wins over the right it names (REQ-AUTH-070).
const syncRow = (row) => {
  const level = row.querySelector("[data-clara-role-cell=data]:checked")
  const denied = level !== null && level.value === "no_access"
  rightsOf(row).forEach((box) => {
    box.disabled = denied
    if (denied) {
      box.title = t("roles.right_denied")
    } else {
      box.removeAttribute("title")
    }
  })
}

const tabOf = (table) => document.getElementById(`role-arm-${table.dataset.claraRoleArm}-tab`)

const markDirty = (input) => {
  const table = input.closest("[data-clara-role-arm]")
  const tab = table === null ? null : tabOf(table)
  const badge = tab === null ? null : tab.querySelector(".clara-dirty")
  if (badge !== null) {
    badge.classList.remove("d-none")
  }
}

ready(() => {
  const form = document.querySelector("[data-clara-roles-form]")
  if (form === null) {
    return
  }

  form.querySelectorAll("[data-clara-role-row]").forEach(syncRow)

  form.addEventListener("change", (event) => {
    const target = event.target
    if (!(target instanceof HTMLInputElement) && !(target instanceof HTMLSelectElement)) {
      return
    }
    markDirty(target)
    const row = target.closest("[data-clara-role-row]")
    if (row !== null && target.dataset.claraRoleCell === "data") {
      syncRow(row)
    }
  })

  // Bulk setting walks the table of the arm tab the button sits in.
  form.querySelectorAll(".clara-bulk-set").forEach((button) => {
    button.addEventListener("click", () => {
      const table = button.closest("[data-clara-role-arm]")
      if (table === null) {
        return
      }
      const { column, value } = button.dataset
      table.querySelectorAll("[data-clara-role-row]").forEach((row) => {
        row.querySelectorAll(`[data-clara-role-cell="${column}"]`).forEach((cell) => {
          if (cell.disabled) {
            return
          }
          cell.checked = cell.type === "checkbox" || cell.value === value
          cell.dispatchEvent(new Event("change", { bubbles: true }))
        })
      })
    })
  })
})
