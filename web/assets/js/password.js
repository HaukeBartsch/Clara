// The client half of the new-password form (§2.6): agree-checking the two fields before the
// browser sends them. This is a convenience — the same check runs authoritatively in PHP, and
// the API applies the length policy again after it verifies whatever token came with the
// request — so a page without this module still refuses a mismatched pair.

import { ready, t } from "../app.js"

ready(() => {
  const form = document.querySelector("[data-clara-password-form]")
  if (!form) {
    return
  }

  const password = form.querySelector("#new-password")
  const repeat = form.querySelector("#repeat-password")
  const notice = form.querySelector("[data-clara-password-mismatch]")
  if (!password || !repeat) {
    return
  }

  const check = () => {
    // An untouched field is not a mismatch: the message appears once there is something to
    // say about what the user typed.
    const mismatched = repeat.value !== "" && password.value !== repeat.value
    if (notice) {
      notice.classList.toggle("d-none", !mismatched)
    }
    // Custom validity rides on the field, so native validation blocks the submit too and a
    // screen reader hears the reason at the control that has the problem.
    repeat.setCustomValidity(mismatched ? t("account.password.mismatch") : "")
  }

  repeat.addEventListener("input", check)
  password.addEventListener("input", () => {
    // Changing the first field can resolve a mismatch the second one reported.
    check()
  })
  form.addEventListener("submit", check)
})
