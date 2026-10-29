# Static Analysis Triage

Triage record for semgrep scans of this repository. It exists so that the same
twenty findings are not re-argued by hand every scan: a finding whose disposition
is recorded here needs no further discussion unless its `Recheck when` condition
fires.

Scan triaged here: 2026-10-16, rules `go.lang.security.*`, `php.lang.security.*`,
`javascript.lang.security.*`, `typescript.react.security.*`,
`python.lang.security.audit.*`. Re-run against `p/default` on 2026-10-21 with semgrep
1.182.0 — the version `.github/workflows/ci.yml` installs, because a baseline recorded
against one version and checked by another fails for reasons nobody can reproduce.
The `MonitoringStressTest.php` row was added on 2026-10-29 from a `p/default` scan with
semgrep 1.181.0.

## Policy

Three places record a disposition, and which one applies is not a matter of taste:

- **`.semgrepignore`** — trees that are not CLARA's to review: the historic FIONA
  application under `assets/`, vendored libraries, and development/CI tooling.
  Excluded from the scan with a reason.
- **This document** — findings inside the shipped application (`api/`, `web/`).
  These are triaged, **not** marked up in source. There are deliberately no
  `nosemgrep` comments anywhere in the codebase: a suppression comment silences
  whatever occupies that line later, and the useful property of this report is
  that it cannot drift into covering a real bug.
- **`.semgrep/baseline.txt`** — the machine-readable mirror of a row above, one
  `<rule id><TAB><file>` pair per accepted finding. It exists because prose cannot
  be a gate: `ci/run.sh` runs semgrep with `--json` and pipes it to
  `ci/semgrep_baseline.php`, which accepts exactly the pairs this file lists and
  fails on everything else, so an unrecorded finding is red while the upstream
  ruleset keeps its full coverage. A pair enters only together with its row here —
  the baseline carries no reasoning of its own. Identity is rule + file, never line
  number, which moves with every edit; the price is that a further hit of an
  accepted rule in the same file joins the accepted set silently, and each row's
  `Recheck when` column is what bounds that. A baseline entry that matched nothing
  is reported (not fatal): prune it when the site goes away.

A finding that is genuinely a weakness gets fixed, and the fix — not an entry
here — is the record.

## Fixed

| Site | Rule | Change |
|---|---|---|
| `api/internal/mailer/mailer.go`, implicit-TLS dial and STARTTLS upgrade | `missing-ssl-minversion` | Both TLS client configurations now come from `relayTLSConfig`, which states `MinVersion: tls.VersionTLS12`. The floor was previously Go's default rather than an expressed intent, on a connection that carries SMTP credentials. 1.2 rather than 1.3 because institutional relays of record still terminate 1.2 (REQ-CFG-028 configures the transport as `starttls` \| `tls` \| `none` and says nothing about versions). |
| `api/internal/admin/breaking.go`, `storeValues.anyRow` | `string-formatted-query` | The query text is now a closed `probeQueries` map of complete literal statements, one per data-table column the `valueProbe` questions name; an unknown column is an error instead of a fragment of SQL. Values were always bound placeholders — this removes the free string from the query path rather than fixing an injection. |

## Accepted as false positives (shipped code)

| Rule family | Sites | Why it does not apply | Recheck when |
|---|---|---|---|
| `xss.no-direct-write-to-responsewriter`, `xss.no-fprintf-to-responsewriter` | `admin/export_dictionary.go` (instrument ZIP), `dataapi/handler.go` (`writeCount`, `writeError`), `dataapi/record_export.go` (JSON row stream), `httpapi/docs.go` (`openAPIJSONHandler`) | Every one writes JSON, CSV or archive bytes with an explicit `Content-Type`. The Go service renders no HTML at all — rendering is the PHP layer's job (`Design/Technology_Stack_Design.md` §4). `template.Execute` would be wrong here, and HTML-escaping a CSV field or a ZIP body corrupts the payload. | Go starts serving a document type a browser interprets (HTML, SVG, unescaped inline XML), or a handler stops setting `Content-Type`. |
| `audit.database.string-formatted-query`, `injection.tainted-sql-string`, `audit.sqli.gosql-sqli` | `audit/audit.go` (`rolloverSQLite`, `rebuildView`, `rolloverMariaDB`, `partitionMariaDB`) | The concatenated identifier is `base`, which only ever ranges over the literal slice `{"audit_events", "audit_record_views"}`; partition names are `"p" + strconv.Itoa(year)`; the view's table list comes from `sqlite_master` rows this same package created. No request data reaches any of it — DDL cannot be parameterized, and these strings are not user-controlled. | A caller other than the year rollover passes a name in, or audit object names become configurable. |
| `injection.tainted-sql-string` | `admin/queries.go`, `listAudit` (and `queryAuditViews`) | The appended text is `" WHERE " + joinConditions(conds) + " ORDER BY created_at DESC, id DESC LIMIT ?"`. Every condition in `conds` is a literal fragment ending in `?` built by `parseAuditFilter`, and every value travels in `args`. Filters that are not plain values (`type`) are matched against a closed set first. | `parseAuditFilter` grows a condition that embeds a value instead of a placeholder. |
| `audit.sqli.gosql-sqli` | `testdb/testdb.go` (`CREATE DATABASE`, `DROP DATABASE`) | Test helper. The database name is `"clara_t_" + hex` of 12 bytes read from `crypto/rand`, never external input. | The helper starts accepting a name from the environment or an argument. |
| `php.lang.security.exec-use` | `web/app/PhpCli.php::probe`, `web/app/SubprocessLdapAttempts.php::spawn` | `proc_open` is called with an **array** command, which bypasses the shell: each element is one argv entry, so no metacharacter is interpreted and there is nothing to inject. The interpreter path comes from the fixed candidate list in `PhpCli::binary()` (documented there as deliberately not configurable), and the script argument is the constant `CLARA_WEB_ROOT . '/app/ldap_attempt.php'`. | The command becomes a string (shell form), or a binary path or script path starts coming from configuration or request data. |
| `php.lang.security.exec-use` | `web/app/MonitoringStressTest.php::start` (worker launch) and `::python` (version probe) | The probe is a constant array command, `['/usr/bin/env', 'python3', '--version']`. The launch is the web layer's one shell form, and deliberately so: `nohup … & echo $!` detaches the stress worker from the PHP request and hands back its pid, which is what keeps the Monitoring page and the PHP workers available while a run goes on (DEV-UI-22, `Design/User_Interface_Design.md` §5.11). Every element of the command passes `escapeshellarg`, and none comes from the request: the interpreter is the constant `/usr/bin/env`, the script path is fixed relative to the repository, the base URL is the validated `API_ADDR` configuration, the user id is the session administrator's integer id, the other flags are literals, and the paths lie in a 0700 temporary directory named from a hash. The service token reaches the worker through its environment, never its command line. | An element of the command stops going through `escapeshellarg`, request data other than the integer administrator id joins it, or the worker can detach itself so the launch no longer needs a shell — then use the array form like the row above. |
| `php.lang.security.ldap-bind-without-password` | `web/app/ldap_attempt.php`, step 1 search bind | Anonymous bind is the designed behavior when no read account is configured, and says so in the comment above the call: with no `bind_dn` the directory search runs anonymously, and a refused anonymous search is reported as an installation fault (`outcome=unreachable`, `reason=search_bind`), never as a user failure (design §2.2 step 1). The user's own credential check is the separate bind with their submitted password. | An installation cannot set `LDAP_SERVER_n_BIND_DN` and its directory holds entries an anonymous search may read — then the exposure is the directory's, and it should be fixed there. |
| `detect-non-literal-regexp` | `web/assets/js/form.js`, `formatPattern` and `registryPattern` | The pattern sources are the field's configured date format and a `validation_types` registry regex — design-time configuration authored by project administrators, not per-record input. Server-side validation is authoritative and runs RE2 (linear time), so a pathological regex cannot be used against the API; client-side the result only feeds an advisory hint, and `registryPattern` already returns `null` for a pattern JavaScript rejects. | Client-side hints start consuming a pattern that record data influences, or a registry regex proves slow enough to matter in the browser — then bound it at authoring time in the validation registry, not here. |

## Excluded from the scan

Recorded with reasons in `.semgrepignore`: `assets/` (historic FIONA
application — MD5 password hashing, `CURLOPT_SSL_VERIFYPEER=false`, and request
filenames built from the submitted username are the state CLARA replaces, not
code under review), `web/assets/vendor/` and `api/docs/swaggerui/` (upstream
copies served locally per REQ-TECH-021), and `docs/`, `e2e/`, `stress/`,
`tests/`, `tools/`, `ci/` (development and CI tooling, never deployed
per REQ-TECH-028 — which covers the Playwright HTTP fetch to a localhost server,
the screenshot script's argument-built regexes, and the load generator's
`HTTPSConnection` notice). Also `**/*_test.go` and `api/internal/testdb/`: Go tests
and their database helper never reach the binary, and their findings are fake
secrets and throwaway DDL by construction — a bcrypt hash of a known test password,
a fixed TOTP seed, the TLS server a test starts itself, a `CREATE DATABASE` name
drawn from `crypto/rand`. The non-test files of those same packages stay in scope.
