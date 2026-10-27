#!/usr/bin/env bash
# CI pipeline (Design/Technology_Stack_Design.md §4 and §6), in the order §4's
# layout line names them: go vet + unit + integration tests against a temporary
# SQLite file with no external services (REQ-TECH-021), the Fiona compatibility
# fixtures — the table-driven encodings of the call examples in
# VISION_AND_REQUIREMENTS.md ("Endpoints used by Fiona"), charter success
# criterion 1 as an executable regression test (REQ-TECH-022, REQ-API-037) —
# then php -l, the PHP smoke harness and the Playwright browser specs
# (REQ-TECH-028).
#
# Nothing here installs anything: a suite that cannot run because its tooling is
# absent is a failure with a remedy, never a green tick. That is the same stance
# as the Fiona fixtures below, and it matters most for the browser specs — see
# the skip guard, which is the reason this script exists in its current form.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "==> go vet"
(cd "$root/api" && go vet ./...)

echo "==> unit + integration tests (SQLite, no external services)"
(cd "$root/api" && go test ./...)

echo "==> Fiona compatibility fixtures (REQ-API-037)"
# Fixture convention: tests named TestFiona* in the Go tree. When they are
# absent the run is NOT green — success criterion 1 stays uncovered until the
# examples are encoded against a seeded project.
if grep -rqE '^func Test[A-Za-z0-9_]*Fiona' "$root/api" --include='*_test.go'; then
	(cd "$root/api" && go test ./... -run 'Fiona' -v)
else
	echo "FAIL: no Fiona fixture tests (TestFiona*) found — REQ-API-037 uncovered." >&2
	exit 1
fi

# --- ODM dialect conformance ----------------------------------------------------
echo "==> ODM dialect conformance (REQ-ODM-055, advisory)"
# A round trip cannot see a dialect error: the exporter and importer agreed with each
# other that ODM 2.0's data nesting was ODM 1.3, and the suite stayed green (O-7). So
# the Go suite dumps real exports of both dialects, and tools/odm_conformance.py holds
# them to CDISC's own schemas — vendored under tools/odm_schema/, no network at run
# time — through tools/odm_schema/clara_odm_ext.xsd, CLARA's redefinition of the
# extension hooks 1.3.2 leaves empty.
#
# This is deliberately the one advisory step in the run, against the stance stated at
# the top of this file: lxml is not part of the toolchain AGENTS.md fixes, so a machine
# without it still builds. Findings print as WARN and never fail the build; fixing them
# stays the point of printing them.
odm_dump="$(mktemp -d)"
(cd "$root/api" && CLARA_ODM_DUMP_DIR="$odm_dump" go test ./internal/admin/ -run 'TestExportODMConformanceDump' >/dev/null 2>&1) || true
if ! ls "$odm_dump"/*.xml >/dev/null 2>&1; then
	echo "WARN: the Go suite produced no ODM documents, so conformance went unchecked." >&2
else
	odm_status=0
	python3 "$root/tools/odm_conformance.py" "$odm_dump"/*.xml || odm_status=$?
	case "$odm_status" in
		0) ;;
		1) echo "WARN: an exported ODM document deviates from the CDISC schema above (REQ-ODM-055)." >&2 ;;
		3) echo "NOTE: ODM conformance skipped — python3 lxml is not installed on this machine." >&2 ;;
		4) echo "WARN: a vendored ODM schema will not load, so the check produced no verdict at all (REQ-ODM-055)." >&2 ;;
		*) echo "WARN: the ODM conformance check exited $odm_status without a verdict." >&2 ;;
	esac
fi
rm -rf "$odm_dump"

# --- the web layer -------------------------------------------------------------
echo "==> php -l (web/, tests/php/)"
lint_failed=0
while IFS= read -r -d '' file; do
	# php -l reports a parse error on stdout, so capture it and print only failures.
	if ! lint=$(php -l "$file" 2>&1); then
		echo "$lint" >&2
		lint_failed=1
	fi
done < <(find "$root/web" "$root/tests/php" -type f -name '*.php' -print0)
# The historic FIONA application under assets/ is reference material (AGENTS.md,
# "Repository layout"), so it is deliberately outside the gate.
if (( lint_failed )); then
	echo "FAIL: php -l reported syntax errors." >&2
	exit 1
fi

echo "==> no external references in web/ (ASM-TECH-2)"
# Only reference positions — the forms that make a browser fetch something — because
# web/ legitimately holds licence-header URLs and SVG xmlns strings, none of which
# reaches the network. A bare http grep would be noise, so it is not used.
external_reference="(src|href)[[:space:]]*=[[:space:]]*[\"']?https?:|@import[^;]*https?:|url\([[:space:]]*[\"']?https?:|(from|import)[[:space:]]*\(?[[:space:]]*[\"']https?:"
if hits=$(grep -rInoE "$external_reference" "$root/web"); then
	echo "FAIL: web/ references an external URL — the application must render with no" >&2
	echo "internet access (ASM-TECH-2, REQ-TECH-027); vendor the asset instead:" >&2
	echo "$hits" >&2
	exit 1
fi

# The reference positions above cannot see a runtime request: `fetch("https://…")`,
# `new WebSocket("wss://…")` and `navigator.sendBeacon(…)` carry the URL in an argument,
# not in src= or href=, so a page could phone home through them and this gate would stay
# green. Those forms are checked over first-party code, where no legitimate reason to
# name an outside host exists. Vendored libraries are excluded — web-llm.mjs calls fetch
# by design (for its locally hosted weights) — because upstream code is held to the pinned
# hashes in Technology_Stack_Design.md §3 rather than to a pattern (REQ-TECH-021).
runtime_reference="(fetch|open)[[:space:]]*\([[:space:]]*[\"'\`]https?:|(fetch|open)[[:space:]]*\([[:space:]]*[\"'\`][/.]{2}|[Nn]ew[[:space:]]+WebSocket\(|EventSource\(|sendBeacon\(|importScripts\(|\.src[[:space:]]*=[[:space:]]*[\"'\`][/.]{2}|(file_get_contents|fopen|curl_init)\([[:space:]]*[\"']https?:"
if hits=$(grep -rInoE "$runtime_reference" "$root/web" \
	--exclude-dir=vendor --exclude-dir=minified --include='*.js' --include='*.mjs' \
	--include='*.php' --include='*.phtml' --include='*.html'); then
	echo "FAIL: first-party code opens a connection at runtime to an absolute URL — the" >&2
	echo "application must work with no internet access (ASM-TECH-2, REQ-TECH-027), and CSP" >&2
	echo "connect-src 'self' only blocks it at run time, which is a backstop and not the rule:" >&2
	echo "$hits" >&2
	exit 1
fi

echo "==> static analysis of the shipped code (semgrep)"
# .semgrepignore names what is out of scope (the legacy FIONA application, vendored copies,
# dev-only tooling); api/ and web/ are scanned in full, with findings triaged per site in
# docs/Static_Analysis_Triage.md and deliberately no inline nosemgrep markers, so a finding
# cannot hide behind a suppression comment. --metrics=off matters: the scanner must not post
# anything about this codebase to semgrep.dev.
if ! command -v semgrep >/dev/null 2>&1; then
	echo "FAIL: semgrep is not installed, so the static-analysis gate did not run — an" >&2
	echo "unrun check is not a passed one (same rule as the browser specs below)." >&2
	echo "setup: python3 -m pip install --user semgrep   (or: pipx install semgrep)" >&2
	exit 1
fi
# A committed rules directory wins over the registry ruleset, so a run stays reproducible and
# works from a cached copy on a network-restricted runner. SEMGREP_CONFIG overrides either.
semgrep_config="${SEMGREP_CONFIG:-}"
if [[ -z "$semgrep_config" && -d "$root/.semgrep/rules" ]]; then semgrep_config="$root/.semgrep/rules"; fi
if [[ -z "$semgrep_config" ]]; then semgrep_config="p/default"; fi
# The verdict comes from ci/semgrep_baseline.php rather than from semgrep's exit code, so that the
# findings docs/Static_Analysis_Triage.md has already reasoned about do not have to be re-argued by
# hand while genuinely new ones still fail the build. That is how this repository runs a gate with
# no inline `nosemgrep` markers at all: upstream rule coverage stays intact, and accepting a finding
# costs one reviewable line in .semgrep/baseline.txt next to its document row. semgrep exits 1 when
# it finds anything, so its status says nothing usable here — the report does, including the errors
# field, because a file semgrep could not analyze is a hole in the scan rather than a clean result.
semgrep_report="$(mktemp "${TMPDIR:-/tmp}/clara-semgrep.XXXXXX")"
semgrep_status=0
semgrep --metrics=off --quiet --json --config "$semgrep_config" "$root/api" "$root/web" >"$semgrep_report" || semgrep_status=$?
if ! php "$root/ci/semgrep_baseline.php" "$root/.semgrep/baseline.txt" "$root" <"$semgrep_report"; then
	rm -f "$semgrep_report"
	echo "FAIL: semgrep reported findings outside the recorded triage (semgrep exit $semgrep_status)." >&2
	exit 1
fi
rm -f "$semgrep_report"

echo "==> relevance Sankey data is fresh (REQ-UI-060)"
# web/app/data/relevance_sankey.php ships inside web/ (production carries no docs/, INSTALL.md §3)
# and must match its source table in docs/Requirement_Relevance_Classification.md.
php "$root/tools/build_relevance_sankey.php" --check

echo "==> PHP smoke harness (tests/php/run.php)"
# No framework and no HTTP: a fake transport answers in place of the API (§6).
php "$root/tests/php/run.php"

# --- browser specs (REQ-TECH-028) ----------------------------------------------
echo "==> Playwright browser specs (e2e/, REQ-TECH-028)"
remedy="setup: cd e2e && npm ci && npx playwright install chromium (Plan/Web_Implementation.md §8)"

if ! command -v node >/dev/null 2>&1; then
	echo "FAIL: node is not installed, so the browser specs cannot run — REQ-TECH-028 uncovered." >&2
	echo "$remedy" >&2
	exit 1
fi
node_major=$(node -p 'Number(process.versions.node.split(".")[0])')
if (( node_major < 20 )); then
	echo "FAIL: Playwright 1.63 needs Node >= 20 (found $(node --version))." >&2
	echo "$remedy" >&2
	exit 1
fi
if [[ ! -x "$root/e2e/node_modules/.bin/playwright" ]]; then
	echo "FAIL: e2e/node_modules is not installed, so the browser specs cannot run — REQ-TECH-028 uncovered." >&2
	echo "$remedy" >&2
	exit 1
fi

state="$(mktemp -d "${TMPDIR:-/tmp}/clara-ci.XXXXXX")"
web_port="${CLARA_WEB_PORT:-8090}"
stack_pid=""

# The stack has to come down on the failure paths as well as the success path: a
# php -S left holding :8090 makes every later run fail somewhere it did not fail here.
teardown() {
	trap - EXIT INT TERM
	if [[ -n "$stack_pid" ]] && kill -0 "$stack_pid" 2>/dev/null; then
		# dev-stack.sh traps TERM itself and stops the API, php -S and its SQLite file.
		kill "$stack_pid" 2>/dev/null || true
		for _ in $(seq 1 20); do
			kill -0 "$stack_pid" 2>/dev/null || break
			sleep 0.5
		done
		kill -9 "$stack_pid" 2>/dev/null || true
	fi
	wait "$stack_pid" 2>/dev/null || true
	rm -rf "$state"
}
trap teardown EXIT INT TERM

# The real stack: the Go API on a throwaway SQLite file plus web/public under
# php -S, with the authentication-source names the login spec asserts against.
CLARA_WEB_PORT="$web_port" CLARA_API_PORT="${CLARA_API_PORT:-8085}" \
	"$root/e2e/dev-stack.sh" > "$state/dev-stack.log" 2>&1 &
stack_pid=$!

printf '==> waiting for the stack on :%s ' "$web_port"
for _ in $(seq 1 60); do
	if curl -sf -o /dev/null "http://127.0.0.1:$web_port/login"; then
		echo " up"
		break
	fi
	# The stack exits on its own when the API or the web layer fails to start.
	kill -0 "$stack_pid" 2>/dev/null || break
	printf '.'
	sleep 1
done
if ! curl -sf -o /dev/null "http://127.0.0.1:$web_port/login"; then
	echo
	echo "FAIL: the development stack never answered on :$web_port." >&2
	tail -30 "$state/dev-stack.log" >&2
	exit 1
fi

# The JSON report is what tells a spec that failed from one that never ran.
if ! (cd "$root/e2e" && CLARA_WEB_URL="http://127.0.0.1:$web_port" \
	PLAYWRIGHT_JSON_OUTPUT_NAME="$state/e2e.json" \
	./node_modules/.bin/playwright test --reporter=list,json); then
	echo "FAIL: Playwright reported failing browser specs (REQ-TECH-028)." >&2
	exit 1
fi

if [[ ! -s "$state/e2e.json" ]]; then
	echo "FAIL: Playwright wrote no report, so nothing can be said about its coverage." >&2
	exit 1
fi
read -r pw_expected pw_skipped < <(node -e '
	const report = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
	process.stdout.write(`${report.stats.expected} ${report.stats.skipped}\n`);
' "$state/e2e.json")

# The skip guard: every web spec calls test.skip(!process.env.CLARA_WEB_URL), and
# Plan/Web_Implementation.md §8 wants those specs "skipped, not red" on a machine with
# no running application. REQ-TECH-028 says they run in CI, so inside this gate a skip
# is uncovered coverage — the one way this script could otherwise report green while
# testing nothing of the web layer.
if (( pw_skipped > 0 )); then
	echo "FAIL: $pw_skipped browser spec(s) were skipped — REQ-TECH-028 coverage did not run." >&2
	echo "The specs skip themselves when CLARA_WEB_URL is unset; a skip here is uncovered, not green." >&2
	exit 1
fi
if (( pw_expected == 0 )); then
	echo "FAIL: no browser spec ran at all — REQ-TECH-028 uncovered." >&2
	exit 1
fi
echo "==> $pw_expected browser specs ran, none skipped"
