// The login page's source-name picker (User_Interface_Design.md §2.2): selecting a name
// re-renders the page for it, because what the name offers underneath — a credential form,
// one provider button, several — is exactly what changes (REQ-UI-042). One module for one
// surface (REQ-UI-045), and no UI string in it: every label comes from the injected
// data-i18n block (REQ-UI-008).
//
// The form keeps its submit button. This is a convenience, not a requirement for working:
// a login page that needed JavaScript to choose an institution would leave a user with a
// broken script permanently locked out.

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

    form.submit()
  })
})
