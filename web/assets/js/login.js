// The login page's source-name picker (User_Interface_Design.md §2.2): selecting a name
// re-renders the page for it, because what the name offers underneath — a credential form,
// one provider button, several — is exactly what changes (REQ-UI-042). One module for one
// surface (REQ-UI-045), and no UI string in it: every label comes from the injected
// data-i18n block (REQ-UI-008).
//
// The picker has no submit button: choosing a name submits it at once, so this module is
// required for signing in (owner decision 2026-10-03, DEV-UI-13). Nothing below the picker
// renders until a name has been chosen.

import { ready } from "../app.js"

ready(() => {
  const form = document.querySelector("form[data-clara-source-picker]")
  if (!form) return

  form.addEventListener("change", () => {
    // REQ-UI-042 asks for the entered credentials to be cleared when the selection
    // changes: a password typed for one institution must not still be sitting in the form
    // when the user picks another. The reload would have dropped it anyway — this just
    // does not wait for that.
    const password = document.getElementById("login-password")
    if (password) password.value = ""

    // Show the choice straight away (the others grey out) while the page reloads for it.
    const tiles = form.querySelector(".clara-sources")
    if (tiles) tiles.classList.add("clara-sources-chosen")

    form.submit()
  })
})
