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
