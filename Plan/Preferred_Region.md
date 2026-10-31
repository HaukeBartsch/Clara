# Preferred Region Plan

Delivers the master spec section "Organization by hospital regions". Each user gets a preferred region, which is one code of the project organization enumeration (REQ-DB-006). The first value comes from the provider's roles at sign-in, or, when there are none, from the administrator who assigns the user to a project. Users can change it on the account page. Administrators map provider role values to regions in the Control Panel. The project overview and the Control Panel project list show the preferred region first.

Normative sources: REQ-DB-052/053, REQ-AUTH-088/089, REQ-API-049 (amended), REQ-API-175…179, REQ-AUD-036, REQ-CFG-037, REQ-UI-071 (amended), REQ-UI-075…079 and DEV-DB-18, DEV-AUTH-19, DEV-UI-26, DEV-CFG-8. The design is in `Database_Schema_Design.md` §4, `Authentication_Authorization_Design.md` §2.12, `API_Endpoints_Design.md` §4.5/§4.6/§4.30, `Audit_Logging_Design.md` §3.4, `System_Configuration_Design.md` §3.4 and `User_Interface_Design.md` §2.8/§4/§5.1/§5.2/§5.13.

## Starting point (verified 2026-10-31)

- **"Last used" is already per user.** `GET /api/v1/projects` computes `last_accessed_at` from the acting user's own audit events (`LatestProjectActivityByUser`, `WHERE user_id = ?`), and `TestProjectsListCarriesLastAccessedAt` shows that a second member's event does not change the first member's value. `dashboard.js` derives "today" and "yesterday" from that value in the browser's zone. Nothing is system-wide. This delivery keeps it that way and adds an end-to-end check with two accounts (D4).
- **The organization list exists only in PHP** (`ControlPanelController::ORGANIZATIONS`). The Go API does not validate `projects.organization`, although `Database_Schema_Design.md` §4 says an API allowlist enforces it. D1 adds the list to Go. Applying it to project create/update closes that gap too, but would reject projects stored with other codes on their next update. Decide that separately before turning it on.
- **Theme and language changes write no audit entry**, despite REQ-API-043. The region choice is audited (REQ-AUD-036). The existing gap is not part of this delivery.

## D1 — Schema and API (Go)

1. Migration `0020_preferred_region.sql` for `sqlite/` and `mariadb/`: the three `users` columns and `provider_role_regions` with its unique key.
2. `admin.Organizations` (ordered, REQ-DB-006) as the one region allowlist, plus the effective-region helper `COALESCE(preferred_region, automatic_region)`.
3. Region values on the user object (REQ-API-176), and `GET`/`PUT /api/v1/users/me/region` with the `preferred_region_changed` audit entry (REQ-API-175).
4. `GET`/`POST /api/v1/provider-roles` and `PUT`/`DELETE /api/v1/provider-roles/{id}` with `region_mapping_changed` (REQ-API-177/178).
5. `in_preferred_region` on `GET /api/v1/projects` (REQ-API-049); the list order stays the same.
6. The assignment placeholder on member add: a conditional update in the membership transaction, plus `region_placeholder` in `membership_changed` (REQ-API-179).
7. Sequence C step 3.5 on the OAuth2 leg: read the claim (ID token, else userinfo), normalize, upsert the values, derive the region (REQ-AUTH-088). A failure never refuses the login.
8. `OAUTH2_N_ROLES_ATTR`: parse, validate at startup, include in the startup dump and the configuration check (REQ-CFG-037). Add the region preview to `POST /api/v1/auth/oauth/test`.

## D2 — Web (PHP + JS)

1. Account page Profile: the region select, `POST /account?action=set_region` (REQ-UI-075).
2. `dashboard.js`: group by organization, preferred group first and the rest in REQ-DB-006 order, rank by recency inside each group, and add the group headings to the refresh comparison (REQ-UI-076).
3. Control Panel Projects list grouped the same way, in name order inside each group, with divider rows (REQ-UI-077).
4. Control Panel Regions section (Tabulator grid, map/add/remove actions) and the new-value count in the left panel (REQ-UI-078).
5. Region column in the Users section (REQ-UI-079).
6. Region preview in the Authentication Test report.
7. Translation keys `account.region.*`, `admin.regions.*`, `dashboard.group.*` in `en`, with `nb`/`nn` rows.

## D3 — Installation

- `.env.example`: `#OAUTH2_N_ROLES_ATTR=` under each provider, and in the PingFederate block a note that the attribute contract must release the roles attribute (to the ID token or to userinfo) under the configured name.
- `INSTALL.md` §14: set up the region mapping — configure the claim, run the Authentication Test, map the values it shows, and expect each user's region at their next sign-in.

## D4 — Tests and exit criteria

- Go: claim normalization (string, list, wrong types, the 50-value and 255-character bounds); derivation order and "no mapped value leaves the region unchanged"; a login is never refused on account of roles; the placeholder applies only when both columns are `NULL`, not on self-assignment, and is replaced by a later provider region; mapping CRUD with audit entries and 400/404/409 cases; `in_preferred_region` per actor and recomputed after `PUT /users/me/region`; non-administrators get 403 on `/provider-roles`.
- PHP (`tests/php/`): region select validation and the `null` mapping for Automatic; ordering of the Control Panel list.
- e2e (`e2e/tests/regions.spec.ts`): the preferred group comes first and the other groups follow in REQ-DB-006 order with recency inside each, and no heading appears when all projects share one region; a region change regroups the open overview on the next refresh; **two accounts that are members of one project, where only the second works in it today: the first account's overview does not show "today" for that project**; the Regions section shows its unconfigured state. The dev stack configures no OAuth2 provider, so mapping a value and the falling count are covered by `tests/php/regions_test.php` and the Go tests rather than end to end.
- Done when `go test ./...`, `go vet ./...`, `php tests/php/run.php` and the e2e suite pass on SQLite, and the migration applies on MariaDB 11.x.
