#!/usr/bin/env bash
# Launcher for the CLARA API stress test.
#
# Starts a throwaway Go API server (SQLite in a temp directory, bootstrap
# administrator configured via the environment), waits for /healthz, runs
# stress_api.py against it and shuts the server down again. All arguments are
# forwarded to stress_api.py, so scale can be tuned per run:
#
#   ./run_stress.sh                                   # full default scale
#   ./run_stress.sh --projects 5 --max-events 10      # quick smoke run
#
# To stress an already running server instead, set BASE_URL (plus
# SERVICE_TOKEN / ADMIN_EMAIL / ADMIN_PASSWORD) and pass --no-server:
#
#   BASE_URL=http://127.0.0.1:8080 ./run_stress.sh --no-server
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(dirname "$HERE")"

# Fixed defaults the Python client also reads from the environment.
export SERVICE_TOKEN="${SERVICE_TOKEN:-stress-internal-token}"
export ADMIN_EMAIL="${ADMIN_EMAIL:-admin@stress.local}"
export ADMIN_PASSWORD="${ADMIN_PASSWORD:-stress-bootstrap-password}"
API_LISTEN="${API_LISTEN:-127.0.0.1:8099}"
export BASE_URL="${BASE_URL:-http://$API_LISTEN}"
OUT_DIR="${STRESS_OUT:-$HERE/stress-out}"
mkdir -p "$OUT_DIR"
export STRESS_OUT="$OUT_DIR"

SERVER_PID=""
cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "stopping server $SERVER_PID"
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

if [[ "${1:-}" == "--no-server" ]]; then
  shift
  echo "using the already running API at $BASE_URL"
else
  WORK="$(mktemp -d "${TMPDIR:-/tmp}/clara-stress-XXXXXX")"
  echo "throwaway server: sqlite=$WORK/stress.sqlite addr=$API_LISTEN"
  (
    cd "$REPO/api"
    APP_ENV=development \
    API_ADDR="$API_LISTEN" \
    WEB_PUBLIC_URL="http://localhost:8000" \
    LOG_LEVEL_API="${LOG_LEVEL_API:-warn}" \
    DB_CONNECTION=sqlite \
    DB_DATABASE="$WORK/stress.sqlite" \
    INTERNAL_SERVICE_TOKEN="$SERVICE_TOKEN" \
    ADMIN_BOOTSTRAP_EMAIL="$ADMIN_EMAIL" \
    ADMIN_BOOTSTRAP_PASSWORD="$ADMIN_PASSWORD" \
    ANON_SALT=stress-salt \
    APP_TIMEZONE=UTC \
    exec go run ./cmd/server
  ) &
  SERVER_PID=$!

  echo -n "waiting for $BASE_URL/healthz "
  for _ in $(seq 1 120); do
    if curl -fsS "$BASE_URL/healthz" >/dev/null 2>&1; then echo " ok"; break; fi
    echo -n "."
    sleep 1
  done
  curl -fsS "$BASE_URL/healthz" >/dev/null  # fail loudly if it never came up
fi

python3 "$HERE/stress_api.py" "$@"

# Report the size the database grew to (only meaningful for the throwaway run).
if [[ -n "$SERVER_PID" ]]; then
  DB_FILE="$WORK/stress.sqlite"
else
  DB_FILE=""
fi
if [[ -n "$DB_FILE" && -f "$DB_FILE" ]]; then
  SIZE=$(du -m "$DB_FILE" | cut -f1)
  echo "database file: ${SIZE} MB ($DB_FILE)"
fi
echo "exports and report: $OUT_DIR"
