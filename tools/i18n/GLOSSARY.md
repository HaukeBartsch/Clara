# CLARA i18n draft glossary (nb / nn) — DRAFT, awaiting human review

Rules for every batch:

- Output strict JSON objects with exactly the input keys, same order. No comments, no trailing commas.
- `{placeholder}` tokens are copied verbatim (`{count}`, `{mode}`, `{key}`, …). Never translate or rename them.
- Keep `…` (U+2026) where English uses it; keep leading/trailing spaces and colons as in English.
- Never translated: CLARA, REDCap, FIONA, Tabulator, Bootstrap, Darkly, Yeti, LDAP, OAuth2, OIDC, API, JSON, CSV, SQL, SQLite, MariaDB, TOTP, CSRF, IP, URL, ID.
- `app.name` (CLARA) stays `CLARA`. Mode names `development`/`analysis`/`production` are language-independent tokens — never translated; sentences around them must read naturally with an English token inside.

Core terminology (bokmål / nynorsk) — use consistently:

| English | bokmål | nynorsk |
|---|---|---|
| project | prosjekt | prosjekt |
| record | registrering | registrering |
| instrument (data-collection form) | instrument | instrument |
| form | skjema | skjema |
| field | felt | felt |
| event | hendelse | hending |
| survey (patient-facing link) | undersøkelsesskjema | undersøkingsskjema |
| user | bruker | brukar |
| member | medlem | medlem |
| group | gruppe | gruppe |
| role | rolle | rolle |
| permission | rettighet | rettigheit |
| sign in / sign out | logg inn / logg ut | logg inn / logg ut |
| password | passord | passord |
| two-factor authentication | tofaktorautentisering | tofaktorautentisering |
| audit trail / audit log | revisjonsspor / revisjonslogg | revisjonsspor / revisjonslogg |
| translation (i18n string) | oversettelse | omsetjing |
| language | språk | språk |
| missing | mangler / manglende | manglar / manglande |
| save / cancel / delete / close | lagre / avbryt / slett / lukk | lagre / avbryt / slett / lukk |
| add | legg til | legg til |
| edit | rediger | rediger |
| settings | innstillinger | innstillingar |
| dashboard | dashbord | dashbord |
| export (verb/noun) | eksporter / eksport | eksporter / eksport |
| staging (kept as loanword) | staging | staging |
| staged changes | stagede endringer | stagede endringar |
| commit (make active) | gjør gjeldende | gjer gjeldande |
| discard | forkast | forkast |
| breaking change | ødeleggende endring | øydeleggjande endring |
| draft | utkast | utkast |
| enabled / disabled | aktivert / deaktivert | aktivert / deaktivert |
| production / analysis / development (mode) | production / analyse / utvikling — but where the sentence embeds the token {mode}, leave it untranslated | same |
| confirmation | bekreftelse | stadfesting |
| required | påkrevd | påkravd |
| optional | valgfri | valfri |
| unsaved changes | ulagrede endringer | ulagra endringar |

Style:

- Bokmål uses `ik`… standard (ikke, mulighet, endring); nynorsk uses ikkje, mogleg, endring. Address the user as "du" in both.
- Buttons and labels: imperative, short ("Lagre", "Fortsett"). Sentences: full stops as in English.
- Nynorsk noun endings: `-a/-en` common-gender plural `-ar`, neuter `-a` (innstillingar, registreringar, verdiane).
- Do not rephrase away placeholders even if word order must bend to keep them; Norwegian verb-second order applies to the first constituent.
