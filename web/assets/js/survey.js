// The public survey page's behaviour (User_Interface_Design.md §8.8): the same form code as the
// record view (form.js) — branching shows and hides fields while their logic holds (§8.4,
// GD-13), advisory hints follow the field types (REQ-VAL-002), a radio can be cleared. One
// module for that page (REQ-UI-045); labels come from the data-i18n block (REQ-UI-008), and
// the page talks to no API from the browser (REQ-API-084).

import { readContext, wireBranching, wireHints, wireRadioReset, fillTimezone } from "./form.js"
import { ready } from "../app.js"

ready(() => {
  const form = document.querySelector("form[data-clara-survey-form]")
  if (!form) return

  wireBranching(form, readContext())
  wireHints(form)
  wireRadioReset(form)
  // The collection zone of the respondent's dates (GD-16), as in the record form.
  fillTimezone(form)
})
