#!/usr/bin/env bash
# Launcher for the Incoming project test (incoming_stress.py): run_stress.sh's throwaway API
# server, with incoming_stress.py as the client and its own output directory. All arguments are
# forwarded:
#
#   ./run_incoming.sh                       # 400,000 records — runs for hours
#   ./run_incoming.sh --records 2000        # quick run
#
# Against an already running server (e2e/dev-stack.sh, so the browser timing in
# e2e/tests/incoming-dashboard.spec.ts can follow), set BASE_URL, SERVICE_TOKEN, ADMIN_EMAIL and
# ADMIN_PASSWORD and pass --no-server first — see incoming_stress.py's docstring.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export STRESS_SCRIPT=incoming_stress.py
export STRESS_OUT="${STRESS_OUT:-$HERE/stress-out-incoming}"
exec bash "$HERE/run_stress.sh" "$@"
