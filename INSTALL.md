# Installing CLARA on Red Hat Enterprise Linux

This guide installs CLARA for production on a single Red Hat Enterprise Linux (RHEL) host: the Go API, the PHP web application behind nginx and PHP-FPM, and MariaDB, all on one machine. Rebuilds such as AlmaLinux and Rocky Linux work the same way.

The deployment rules come from [Design/Technology_Stack_Design.md](Design/Technology_Stack_Design.md) §5 (build, reverse proxy, placement) and [Design/System_Configuration_Design.md](Design/System_Configuration_Design.md) (every configuration variable). When this guide and a design document disagree, the design document wins. Please report the mismatch so this guide can be fixed.

> **Tested:** the package, configuration, database, PHP-FPM, nginx and backup steps were run end to end on AlmaLinux 9.8 (PHP 8.3.33, nginx 1.20.1, MariaDB 11.4.13). The Playwright browser suite then passed against the installed system over HTTPS. The SELinux and systemd steps use standard RHEL tooling but were not part of that container run.

---

## Contents

1. [How the pieces fit together](#1-how-the-pieces-fit-together)
2. [Requirements](#2-requirements)
3. [Build a release (on a build machine)](#3-build-a-release-on-a-build-machine)
4. [Prepare the server](#4-prepare-the-server)
5. [Service account and files](#5-service-account-and-files)
6. [Configuration: `/opt/clara/.env`](#6-configuration-optclaraenv)
7. [SELinux](#7-selinux)
8. [MariaDB](#8-mariadb)
9. [The API service and database privileges](#9-the-api-service-and-database-privileges)
10. [PHP-FPM](#10-php-fpm)
11. [Reverse proxy and TLS](#11-reverse-proxy-and-tls)
12. [Firewall](#12-firewall)
13. [First login and verification](#13-first-login-and-verification)
14. [Optional: sign-in sources, email, two-factor](#14-optional-sign-in-sources-email-two-factor)
15. [Operations: logs, backups, restore, upgrades](#15-operations-logs-backups-restore-upgrades)
16. [Troubleshooting](#16-troubleshooting)
17. [Security checklist](#17-security-checklist)

---

## 1. How the pieces fit together

```
              users and REDCap-API clients (e.g. Fiona)
                              │ HTTPS :443
                       ┌──────▼──────┐
                       │    nginx    │  TLS, routing rules, /assets/
                       └──┬───────┬──┘
          POST /api/ ─────┘       └───── every other path
       (REDCap data API)                  │ FastCGI, unix socket
              │                    ┌──────▼──────┐
              │                    │  PHP-FPM    │  web interface (pool "clara")
              │                    └──────┬──────┘
              │                           │ HTTP + X-Internal-Service-Token
              ▼                           ▼
        ┌────────────────────────────────────────┐
        │  Go API   127.0.0.1:8080  (clara-api)  │
        └───────────────────┬────────────────────┘
                            │ TCP 127.0.0.1:3306
                     ┌──────▼──────┐
                     │  MariaDB    │  11.4 LTS
                     └─────────────┘
```

- Only nginx is reachable from the network. The API, PHP-FPM and MariaDB listen on loopback or a local socket only.
- The **data API** (`POST /api/`, the REDCap-compatible endpoint) is public. The **administration API** (`/api/v1/*`) and the API documentation (`/docs`, `/openapi.json`, `/healthz`) are never routed by nginx. Only PHP calls them, over loopback, with the shared `INTERNAL_SERVICE_TOKEN` (REQ-TECH-018, REQ-AUTH-014). Administrators read the API documentation in the Control Panel (**API documentation**), which PHP fills from `/openapi.json`.
- PHP holds the user session and renders pages. It contains no SQL: all data goes through the API (REQ-TECH-006).
- The API applies database migrations itself every time it starts (REQ-DB-003). There is no separate migration tool.

### Files on the server

| Path | Purpose | Owner, mode |
|---|---|---|
| `/opt/clara/bin/api-server` | API binary (static, no runtime dependencies) | `root`, `0755` |
| `/opt/clara/web/` | PHP application, templates, vendored CSS/JS/fonts | `root`, `0755`/`0644` |
| `/opt/clara/.env` | the single configuration file, including secrets | `root:clara`, `0640` |
| `/var/lib/clara/sessions/` | PHP session files | `clara:clara`, `0700` |
| `/etc/systemd/system/clara-api.service` | API service | `root`, `0644` |
| `/etc/php-fpm.d/clara.conf` | PHP-FPM pool | `root`, `0644` |
| `/etc/nginx/conf.d/clara.conf` — or `/etc/httpd/conf.d/clara.conf` for the Apache alternative (§11.3) | reverse proxy | `root`, `0644` |
| `/etc/my.cnf.d/clara.cnf` | MariaDB settings | `root`, `0644` |
| `/usr/local/sbin/clara-db-grants` | switches database privileges for upgrades | `root`, `0750` |
| `/usr/local/sbin/clara-backup` | database + configuration backup | `root`, `0750` |

**Keep `bin/`, `web/` and `.env` together under `/opt/clara`.** The API reads `.env` from its working directory (`/opt/clara`), and the web layer reads `.env` from the directory above `web/`. Both components must see the same file (ASM-CFG-1).

---

## 2. Requirements

| | |
|---|---|
| Operating system | **RHEL 9.6 or later** (PHP 8.3 is available as a module stream) or **RHEL 10** (PHP 8.3 is the default). x86_64 or aarch64. SELinux in enforcing mode is supported. |
| Repositories | RHEL BaseOS and AppStream, plus the upstream MariaDB repository `rpm.mariadb.org` (or a local mirror of it). RHEL itself ships only MariaDB 10.x. |
| Hardware | Reference host: 4 vCPU, 8 GB RAM, SSD ([Technology_Stack_Design.md](Design/Technology_Stack_Design.md) §7). |
| Network | A DNS name (examples here use `clara.example.org`) and a TLS certificate for it. CLARA needs **no internet access at runtime**, because every browser asset is vendored. |
| Build machine | Any OS with `git` and the Go version declared in [`api/go.mod`](api/go.mod) (currently Go 1.27.1). The server itself needs no Go toolchain. |
| Optional | An OAuth2/OIDC identity provider, LDAP directories, an SMTP relay (for emailed login codes, invitations and password resets). |

Component versions ([Technology_Stack_Design.md](Design/Technology_Stack_Design.md) §2):

| Component | Version | Source on RHEL |
|---|---|---|
| PHP | 8.3 or 8.4 | AppStream (`php:8.3` module on RHEL 9, default on RHEL 10) |
| nginx | any supported (RHEL 9: 1.20+, RHEL 10: 1.26) | AppStream |
| MariaDB | 11.x LTS, ≥ 11.4 | `rpm.mariadb.org` |
| Go API | built from this repository | static binary from the build machine |

SQLite (`DB_CONNECTION=sqlite`) is for development and tests only. Production uses MariaDB.

---

## 3. Build a release (on a build machine)

The API compiles to one static binary (`CGO_ENABLED=0`, pure-Go SQLite driver; REQ-TECH-013). The binary already contains the database migrations, the OpenAPI document and the Swagger UI. The web application needs no build step.

```bash
git clone https://github.com/HaukeBartsch/Clara.git clara-src
cd clara-src
git checkout <release tag or commit>

# optional but recommended: the API test suite
(cd api && go vet ./... && go test ./...)

# the API binary — GOARCH=amd64 for x86_64 servers, arm64 for aarch64
mkdir -p dist/clara/bin
(cd api && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o ../dist/clara/bin/api-server ./cmd/server)

# the web application and the configuration reference, exactly as committed
git archive --format=tar HEAD web .env.example | tar -x -C dist/clara

# one tarball to copy to the server
tar -C dist -czf clara-$(git rev-parse --short HEAD).tar.gz clara
```

On a **macOS** build machine, create the tarball with `COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C dist -czf …`. Without these options, macOS adds `._*` metadata files and extended attributes to the archive.

Check the binary (`file dist/clara/bin/api-server` should report *statically linked*), then copy the tarball to the server, e.g. `scp clara-*.tar.gz admin@clara.example.org:/tmp/`.

---

## 4. Prepare the server

All server commands below run as **root** (`sudo -i`).

### 4.1 Base packages and time

```bash
dnf -y update
dnf -y install tar openssl policycoreutils-python-utils chrony
systemctl enable --now chronyd
```

An accurate clock matters for CLARA: TOTP login codes and audit timestamps depend on it.

### 4.2 PHP 8.3, PHP-FPM and nginx

On **RHEL 9** (9.6 or later), switch the PHP module stream to 8.3 first:

```bash
dnf -y module reset php
dnf -y module enable php:8.3
```

On **RHEL 10** PHP 8.3 is the default, so skip that step. Then, on both:

```bash
dnf -y install nginx php-fpm php-cli php-mbstring php-ldap php-opcache php-pdo php-mysqlnd
php -v        # must report 8.3.x (or 8.4.x)
php -m | grep -E '^(curl|json|ldap|mbstring|openssl|session)$'
```

Why each package is needed:

- **`php-cli`**: required even though the site runs under PHP-FPM. An LDAP login tries every directory in parallel, one child process per directory, and those children run with the PHP command-line interpreter (`/usr/bin/php`).
- **`php-mbstring`, `php-ldap`**: used by the web layer. `curl`, `json`, `openssl` and `session` are part of `php-common`.
- **`php-opcache`**: performance.
- **`php-pdo`, `php-mysqlnd`**: listed in the design's extension set. The web layer itself never opens a database connection.

If `dnf module list php` shows no `8.3` stream, the host is older than RHEL 9.6. Update the host, or use the Remi repository's `php:remi-8.3` stream.

As defense in depth, disable PHP's shell-execution functions that CLARA does not use. Add the
following to `/etc/php.ini` (merge with any existing `disable_functions` setting rather than
creating a second directive), then restart PHP-FPM:

```ini
disable_functions = exec,passthru,system,popen,shell_exec
```

**Keep `proc_open` enabled.** CLARA uses it to start parallel LDAP login attempts, so including it
in `disable_functions` breaks LDAP authentication. This setting is defense in depth, not a
replacement for the service-account and SELinux restrictions below. If CLARA or a later dependency
needs one of the disabled functions, remove only that function from the list when required, after
reviewing the need and testing the changed configuration. Confirm the effective setting for both
the CLI and PHP-FPM SAPIs with `php --ri "Core"` and `php-fpm -i`.

### 4.3 MariaDB 11.4 LTS

Add the upstream repository as `/etc/yum.repos.d/mariadb.repo`:

```ini
# MariaDB 11.4 LTS — upstream repository (RHEL ships only 10.x)
[mariadb]
name = MariaDB 11.4
baseurl = https://rpm.mariadb.org/11.4/rhel/$releasever/$basearch
gpgkey = https://rpm.mariadb.org/RPM-GPG-KEY-MariaDB
gpgcheck = 1
module_hotfixes = 1
```

```bash
dnf -y remove mariadb-server     # only if the RHEL package was installed earlier
dnf -y install MariaDB-server MariaDB-client
mariadb --version                # 11.4.x
```

At the first install, `dnf` asks you to accept the signing key. Its fingerprint is `177F 4010 FE56 CA33 3630 0305 F165 6F24 C74C D1D8` ("MariaDB Signing Key").

---

## 5. Service account and files

```bash
# one unprivileged account runs both the API and the PHP-FPM pool
useradd --system --home-dir /var/lib/clara --shell /sbin/nologin clara
install -d -o clara -g clara -m 0750 /var/lib/clara
install -d -o clara -g clara -m 0700 /var/lib/clara/sessions

# unpack the release from section 3
tar -xzf /tmp/clara-<rev>.tar.gz -C /opt --no-same-owner
chown -R root:root /opt/clara
chmod -R u=rwX,go=rX /opt/clara/web
chmod 0755 /opt/clara/bin/api-server
ls -l /opt/clara        # bin/  web/  .env.example
```

The code is owned by `root` and only readable by `clara`, so a compromised PHP or API process cannot modify the application.

---

## 6. Configuration: `/opt/clara/.env`

A single file configures both components ([System_Configuration_Design.md](Design/System_Configuration_Design.md) §3). Create `/opt/clara/.env` with the following content:

```dotenv
# /opt/clara/.env — CLARA production configuration.
# Read by the API (from its working directory) and by the web layer (from the
# directory above web/). One KEY=VALUE per line; no comments after a value.

# --- core
APP_ENV=production
API_ADDR=127.0.0.1:8080
# Keep the loopback bind: whoever reaches this port holding INTERNAL_SERVICE_TOKEN may act as any
# account, because the actor arrives in a header next to that token (§2.3 of the authentication
# design). Production refuses a non-loopback API_ADDR unless API_ALLOW_REMOTE=1 names that
# exception — set it only behind mTLS or on a network you control end to end (REQ-CFG-033).
#API_ALLOW_REMOTE=0
WEB_PUBLIC_URL=https://clara.example.org
LOG_LEVEL_API=info
LOG_LEVEL_WEB=info

# --- database
DB_CONNECTION=mariadb
DB_HOST=127.0.0.1
DB_PORT=3306
DB_DATABASE=clara
DB_USERNAME=clara
DB_PASSWORD=CHANGE_ME

# --- shared secrets (no production defaults exist)
INTERNAL_SERVICE_TOKEN=CHANGE_ME
ANON_SALT=CHANGE_ME
ANON_DATE_SHIFT_MIN=0
ANON_DATE_SHIFT_MAX=364

# --- first administrator (local account, created on first start)
ADMIN_BOOTSTRAP_EMAIL=admin@example.org
ADMIN_BOOTSTRAP_PASSWORD=CHANGE_ME

# --- sessions (web layer)
SESSION_DIR=/var/lib/clara/sessions
SESSION_COOKIE_NAME=csms_session
SESSION_LIFETIME=28800
SESSION_IDLE_TIMEOUT=1800
SESSION_ROTATE_INTERVAL=900
SESSION_COOKIE_SECURE=1

# --- rate limiting: whose forwarded X-Real-IP may name the caller (both components read this)
# one host, as this guide installs it: nginx and PHP reach the API over loopback
# split hosts: list nginx (PHP's peer) and the PHP host (the API's peer), or the address
#             of every caller behind an unlisted proxy collapses into one rate-limit bucket
TRUSTED_PROXY_CIDRS=127.0.0.0/8,::1

# --- account policy, time zone, appearance, monitoring, two-factor
AUTH_INACTIVITY_LIMIT_DAYS=180
AUTH_PASSWORD_TOKEN_TTL_DAYS=7
APP_TIMEZONE=UTC
UI_THEME=bootstrap
MONITORING_PERIOD_MINUTES=10
AUTH_REQUIRE_2FA=0
TOTP_ISSUER=CLARA
TFA_EMAIL_CODE_TTL=600
```

Restrict the file, then fill in the four secrets with random values:

```bash
chown root:clara /opt/clara/.env
chmod 0640 /opt/clara/.env

sed -i \
  -e "s|^DB_PASSWORD=.*|DB_PASSWORD=$(openssl rand -hex 24)|" \
  -e "s|^INTERNAL_SERVICE_TOKEN=.*|INTERNAL_SERVICE_TOKEN=$(openssl rand -hex 32)|" \
  -e "s|^ANON_SALT=.*|ANON_SALT=$(openssl rand -hex 32)|" \
  -e "s|^ADMIN_BOOTSTRAP_PASSWORD=.*|ADMIN_BOOTSTRAP_PASSWORD=$(openssl rand -hex 12)|" \
  /opt/clara/.env

grep -c CHANGE_ME /opt/clara/.env     # must print 0
```

Then edit by hand:

- **`WEB_PUBLIC_URL`**: the public `https://` address. OAuth2 redirect URIs and survey links are built from it, so it must match what users type.
- **`ADMIN_BOOTSTRAP_EMAIL`**: the first administrator's email address.
- **`APP_TIMEZONE`**: the collection time zone (an IANA name such as `Europe/Oslo`). It applies to date/time values imported without an explicit zone (REQ-CFG-026).

Things to know about the values:

| Variable | Notes |
|---|---|
| `APP_ENV=production` | Selects production defaults, and makes both components **refuse to start** if `INTERNAL_SERVICE_TOKEN`, `ANON_SALT` or `WEB_PUBLIC_URL` is missing or still a development default (REQ-CFG-004/005). |
| `INTERNAL_SERVICE_TOKEN` | Shared secret between PHP and the API. Both read it from this file. |
| `ANON_SALT` | Determines the date shift in anonymized exports. **Never change it after go-live, and back it up.** A different salt produces different shifts, so new exports no longer line up with earlier ones. |
| `ADMIN_BOOTSTRAP_PASSWORD` | Must be at least 12 characters. It is used **only to create** the account on the first start and is stored as an argon2id hash. Changing it later in `.env` does not change the password. The value stays required at startup while no OAuth2 provider and no LDAP server are configured. |
| `ADMIN_BOOTSTRAP_EMAIL` | While `ADMIN_BOOTSTRAP_PASSWORD` is set, every API start re-enables this account and makes it an administrator again, so disabling it in the interface does not last. With OAuth2/LDAP configured, the account with this email becomes administrator at its first login through any source. |
| `SESSION_COOKIE_SECURE=1` | Session cookies are sent over HTTPS only. That is why nginx must terminate TLS. With `APP_ENV=production` the web layer refuses to start with `0`. The cookie is then named `__Host-csms_session`: a browser accepts it only from this exact host over HTTPS. |
| `SESSION_IDLE_TIMEOUT`, `SESSION_LIFETIME` | A session ends after 30 minutes without a request, and 8 hours after sign-in however active it was. Users who fill in long forms may need a longer idle timeout; `0` turns it off. |
| `SESSION_ROTATE_INTERVAL` | Every 15 minutes a signed-in session gets a new id. If an old id is used again more than 60 seconds later, the session was copied: every session of that account ends, and the audit log records `session_replay_detected`. |
| `DB_HOST=127.0.0.1` | Must match the database account's host part `'clara'@'127.0.0.1'` (section 8). |

The format is plain `KEY=VALUE`: no `export`, no comment after a value, and quotes are optional. Variables set in the process environment override the file (REQ-CFG-002). The complete variable list, with every optional key, is in `/opt/clara/.env.example` and [System_Configuration_Design.md](Design/System_Configuration_Design.md) §3.

The API reads configuration once at startup. After any change to `.env`, run `systemctl restart clara-api && systemctl reload php-fpm`.

---

## 7. SELinux

Leave SELinux enforcing. nginx and PHP-FPM both run in the `httpd_t` domain. They need read access to the code, write access to the session directory, and permission to open network connections: nginx and PHP connect to the API on `127.0.0.1:8080`, and PHP connects to OAuth2/LDAP servers.

```bash
semanage fcontext -a -t bin_t                  '/opt/clara/bin(/.*)?'
semanage fcontext -a -t httpd_sys_content_t    '/opt/clara/web(/.*)?'
semanage fcontext -a -t httpd_sys_content_t    '/opt/clara/\.env'
semanage fcontext -a -t httpd_sys_rw_content_t '/var/lib/clara/sessions(/.*)?'
restorecon -Rv /opt/clara /var/lib/clara

setsebool -P httpd_can_network_connect on
```

Run `restorecon -R /opt/clara` again after every upgrade (section 15.4). To find a denial, use `ausearch -m AVC -ts recent`.

---

## 8. MariaDB

### 8.1 Server settings

Create `/etc/my.cnf.d/clara.cnf`:

```ini
# /etc/my.cnf.d/clara.cnf — server settings CLARA relies on
[mariadb]
# API and database share the host; never listen on the network
bind-address           = 127.0.0.1
# the schema and the API connection use utf8mb4 / utf8mb4_unicode_ci
character-set-server   = utf8mb4
collation-server       = utf8mb4_unicode_ci
# CLARA stores UTC timestamps; keep the server in UTC as well
default-time-zone      = '+00:00'
# room for single values from a 32 MiB REDCap import
max_allowed_packet     = 64M
# size to roughly 25-50 % of RAM on a dedicated host (reference host: 8 GB)
innodb_buffer_pool_size = 2G
```

```bash
systemctl enable --now mariadb
mariadb-secure-installation
```

In `mariadb-secure-installation`, keep root on unix-socket authentication, remove anonymous users, disallow remote root login, and remove the test database. Afterwards `root` connects only through the local socket (`mariadb` run as root). The commands below rely on that.

Check the settings:

```bash
mariadb -N -e "SELECT VERSION(), @@character_set_server, @@collation_server, @@time_zone, @@bind_address"
# 11.4.x-MariaDB  utf8mb4  utf8mb4_unicode_ci  +00:00  127.0.0.1
```

### 8.2 Database and account

The password comes from `.env`, so there is only one place to change it:

```bash
DB_PASSWORD=$(grep '^DB_PASSWORD=' /opt/clara/.env | cut -d= -f2-)
mariadb <<SQL
CREATE DATABASE clara CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'clara'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD}';
GRANT ALL PRIVILEGES ON clara.* TO 'clara'@'127.0.0.1';
SQL
unset DB_PASSWORD
```

The full grant exists only for the first start, which creates the schema. Section 9 narrows it.

---

## 9. The API service and database privileges

### 9.1 The systemd unit

Create `/etc/systemd/system/clara-api.service`:

```ini
# /etc/systemd/system/clara-api.service
[Unit]
Description=CLARA API (Go)
After=network-online.target mariadb.service
Wants=network-online.target mariadb.service

[Service]
Type=simple
User=clara
Group=clara
# the API reads .env from its working directory
WorkingDirectory=/opt/clara
ExecStart=/opt/clara/bin/api-server
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s

# hardening — the API only needs the network and (with SQLite) /var/lib/clara
NoNewPrivileges=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/clara
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
UMask=0077

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
```

### 9.2 The privilege helper

Audit tables must not be open to `UPDATE` or `DELETE` by the application's database account. MariaDB enforces this at the privilege level (REQ-DB-024, REQ-AUD-002). The API still needs broad rights while it applies migrations. This helper switches between the two states. Save it as `/usr/local/sbin/clara-db-grants`:

```bash
#!/usr/bin/env bash
# /usr/local/sbin/clara-db-grants — switch the CLARA database account between
#   install   full rights on the schema: first start and upgrades run migrations
#   runtime   audit tables append-only for the application (REQ-DB-024)
# Run as root (connects through the MariaDB root socket). Restart clara-api
# afterwards: open connections keep schema-level rights until they reconnect.
set -euo pipefail
db=clara user=clara host=127.0.0.1

case "${1:-}" in
  install)
    mariadb -e "GRANT ALL PRIVILEGES ON \`$db\`.* TO '$user'@'$host'"
    ;;
  runtime)
    mariadb -e "REVOKE ALL PRIVILEGES ON \`$db\`.* FROM '$user'@'$host';
                GRANT SELECT, INSERT, CREATE, ALTER ON \`$db\`.* TO '$user'@'$host'"
    # UPDATE/DELETE on every table except the two audit tables
    mariadb -N -e "
      SELECT CONCAT('GRANT UPDATE, DELETE ON \`$db\`.\`', TABLE_NAME, '\` TO ''$user''@''$host'';')
      FROM information_schema.TABLES
      WHERE TABLE_SCHEMA = '$db' AND TABLE_TYPE = 'BASE TABLE'
        AND TABLE_NAME NOT IN ('audit_events', 'audit_record_views')" | mariadb
    ;;
  *)
    echo "usage: $0 install|runtime" >&2
    exit 2
    ;;
esac
mariadb -e "SHOW GRANTS FOR '$user'@'$host'"
```

```bash
chmod 0750 /usr/local/sbin/clara-db-grants
```

In the `runtime` state, the account keeps:

- `SELECT, INSERT`: everything.
- `UPDATE, DELETE`: every table except `audit_events` and `audit_record_views`.
- `CREATE, ALTER`: needed at runtime. Every start checks the migration table, and once a year the API adds the next audit partition itself (`ALTER TABLE … ADD PARTITION`, [Audit_Logging_Design.md](Design/Audit_Logging_Design.md) §6.1).

`DROP` is not granted, so audit rows cannot be removed with `TRUNCATE`, `DROP PARTITION` or `DROP TABLE` either.

### 9.3 First start, then lock down

```bash
# 1. first start: creates the schema, partitions the audit tables,
#    creates the bootstrap administrator
systemctl start clara-api
journalctl -u clara-api -n 20 --no-pager
```

Expect, at the end of the log (secrets in the config dump above it are shown as `***`):

```
level=INFO msg="bootstrap admin ready" email=admin@example.org user_id=1
level=INFO msg=listening addr=127.0.0.1:8080 env=production db=mariadb schema_version=11
```

```bash
curl -s http://127.0.0.1:8080/healthz      # {"db":"ok","status":"ok"}

# 2. switch to runtime privileges, and restart so every connection picks them up
systemctl stop clara-api
clara-db-grants runtime
systemctl enable --now clara-api
curl -s http://127.0.0.1:8080/healthz
```

If the API refuses to start, its log names every missing or invalid variable after `configuration invalid:`.

---

## 10. PHP-FPM

Create the pool `/etc/php-fpm.d/clara.conf`:

```ini
; /etc/php-fpm.d/clara.conf — PHP-FPM pool for the CLARA web layer
[clara]
user  = clara
group = clara

listen       = /run/php-fpm/clara.sock
listen.owner = nginx
listen.group = nginx
listen.mode  = 0660

pm                   = dynamic
pm.max_children      = 16
pm.start_servers     = 4
pm.min_spare_servers = 2
pm.max_spare_servers = 6
pm.max_requests      = 500

; CLARA logs one JSON object per line on stderr — keep it
catch_workers_output    = yes
decorate_workers_output = no

; all settings come from /opt/clara/.env; do not leak the FPM environment
clear_env = yes
security.limit_extensions = .php

php_admin_flag[display_errors]       = off
php_admin_flag[log_errors]           = on
php_admin_value[error_log]           = /var/log/php-fpm/clara-error.log
php_admin_value[memory_limit]        = 256M
php_admin_value[post_max_size]       = 32M
php_admin_value[upload_max_filesize] = 32M
```

```bash
php-fpm -t
systemctl enable --now php-fpm
ls -l /run/php-fpm/clara.sock        # srw-rw---- nginx nginx
```

Notes:

- **Keep `proc_open` enabled** (including in `disable_functions`, if configured). LDAP logins start their parallel directory attempts with it; see §4.2 for the recommended hardening list.
- The RHEL default pool `www` (in `www.conf`) stays installed but CLARA does not use it. You may disable it if nothing else on the host needs PHP.
- Size `pm.max_children` to the host. Each worker needs roughly 30–60 MB.

---

## 11. Reverse proxy and TLS

One reverse proxy stands in front of CLARA: it terminates TLS and owns the routing rules of
[`Technology_Stack_Design.md`](Design/Technology_Stack_Design.md) §5. nginx is the reference server
(§11.2), and Apache httpd 2.4 is an approved alternative (§11.3). Both configurations ship with the
repository — `deploy/nginx/clara.conf` and `deploy/apache2/clara.conf` — and hold to the same rules, so
a deployment answers identically either way, including every check in §13.1. Use one of them per host;
never chain the application between both.

### 11.1 Certificate

Place the certificate (with the intermediate chain) and its key here:

```bash
install -m 0644 clara.crt /etc/pki/tls/certs/clara.crt
install -m 0600 clara.key /etc/pki/tls/private/clara.key
```

For a test system only, create a self-signed certificate:

```bash
openssl req -x509 -newkey rsa:2048 -nodes -days 365 -subj "/CN=clara.example.org" \
  -keyout /etc/pki/tls/private/clara.key -out /etc/pki/tls/certs/clara.crt
chmod 0600 /etc/pki/tls/private/clara.key
```

### 11.2 Site configuration

The configuration below ships as [`deploy/nginx/clara.conf`](deploy/nginx/clara.conf). Copy it to
`/etc/nginx/conf.d/clara.conf`, replacing `clara.example.org` with the real name:

```nginx
# /etc/nginx/conf.d/clara.conf — CLARA reverse proxy (Technology_Stack_Design.md §5)

log_format clara_safe '$remote_addr - $remote_user [$time_local] "$request_method $uri $server_protocol" $status $body_bytes_sent "$http_user_agent"';

upstream clara_api {
    server 127.0.0.1:8080;        # API_ADDR
    keepalive 8;
}

server {
    listen 80;
    listen [::]:80;
    server_name clara.example.org;
    access_log /var/log/nginx/clara.access.log clara_safe;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    # HTTP/2: nginx >= 1.25.1 (RHEL 10): add "http2 on;"
    #         older nginx (RHEL 9):       use "listen 443 ssl http2;" instead
    server_name clara.example.org;

    ssl_certificate     /etc/pki/tls/certs/clara.crt;
    ssl_certificate_key /etc/pki/tls/private/clara.key;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_session_cache   shared:clara_tls:10m;
    ssl_session_timeout 1d;

    add_header Strict-Transport-Security "max-age=31536000" always;
    fastcgi_hide_header X-Powered-By;

    # $uri excludes query parameters; OAuth callback codes must not enter access logs.
    access_log /var/log/nginx/clara.access.log clara_safe;
    error_log  /var/log/nginx/clara.error.log;

    root /opt/clara/web/public;

    # REDCap-style record imports may be up to 32 MiB
    client_max_body_size 32m;

    # --- data API (REDCap-compatible): POST /api/ is the only public API route
    location = /api/ {
        limit_except POST { deny all; }

        proxy_pass         http://clara_api;
        proxy_http_version 1.1;
        proxy_set_header   Connection "";
        proxy_set_header   Host $host;
        # the API rate-limits by this address; never trust a client-supplied value
        proxy_set_header   X-Real-IP $remote_addr;
        proxy_set_header   X-Forwarded-For "";
        # internal-only headers are stripped from every external request
        proxy_set_header   X-Internal-Service-Token "";
        proxy_set_header   X-Internal-User-Id "";
        proxy_set_header   X-Internal-Session-Epoch "";

        # exports stream and can take minutes
        proxy_buffering    off;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }

    # --- administration API (/api/v1/*) and everything else under /api/: never public
    location /api/ {
        return 404;
    }

    # --- static CSS/JS/fonts (outside the document root)
    # Third-party libraries change only on an upgrade and may be cached for a day.
    location /assets/vendor/ {
        alias /opt/clara/web/assets/vendor/;
        expires 1d;
        add_header Strict-Transport-Security "max-age=31536000" always;
        add_header X-Content-Type-Options "nosniff" always;
    }
    # CLARA's own scripts and styles change with every deployment, while pages are
    # never cached: browsers must revalidate them (a cheap 304 via ETag) or a
    # day-old script runs under a new page and its controls stay dead.
    location /assets/ {
        alias /opt/clara/web/assets/;
        add_header Cache-Control "no-cache" always;
        add_header Strict-Transport-Security "max-age=31536000" always;
        add_header X-Content-Type-Options "nosniff" always;
    }

    # --- every page goes through the front controller
    location / {
        try_files $uri /index.php$is_args$args;
    }

    location = /index.php {
        include       fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root/index.php;
        # PHP's caller address is REMOTE_ADDR, which fastcgi_params above already sets from
        # $remote_addr; this header matters only where PHP's own peer is a listed proxy. Either
        # way it is overwritten, never appended to whatever the client sent.
        fastcgi_param HTTP_X_REAL_IP $remote_addr;
        fastcgi_param HTTP_X_INTERNAL_SERVICE_TOKEN "";
        fastcgi_param HTTP_X_INTERNAL_USER_ID "";
        fastcgi_param HTTP_X_INTERNAL_SESSION_EPOCH "";
        fastcgi_pass  unix:/run/php-fpm/clara.sock;
        fastcgi_read_timeout 600s;
    }

    # no other PHP file is ever executed
    location ~ \.php$ {
        return 404;
    }
}
```

```bash
nginx -t
systemctl enable --now nginx
```

What the configuration enforces ([Technology_Stack_Design.md](Design/Technology_Stack_Design.md) §5):

| Rule | Where |
|---|---|
| TLS termination; HTTP redirects to HTTPS | both `server` blocks |
| Only `POST /api/` reaches the API from outside; `GET /api/` is refused | `location = /api/` |
| `/api/v1/*` (administration) is never routable | `location /api/` → 404 |
| `/docs`, `/openapi.json`, `/healthz` never reach the API | not proxied; PHP answers 404 |
| `X-Internal-Service-Token`, `X-Internal-User-Id` and `X-Internal-Session-Epoch` are stripped from every external request | `proxy_set_header … ""` / `fastcgi_param … ""` |
| `X-Real-IP` is always nginx's own `$remote_addr`, for both the API and PHP — and neither believes it from an unlisted peer | `proxy_set_header` / `fastcgi_param HTTP_X_REAL_IP`; `dataapi.SourceIP`, `Request::clientIp` |
| Access logs omit query strings and referrers, so OAuth callback codes are not logged | `log_format clara_safe` (`$uri`, not `$request`) |
| Exports stream instead of buffering | `proxy_buffering off`; PHP sends `X-Accel-Buffering: no` |

The stock `/etc/nginx/nginx.conf` still contains a default `server` on port 80. It does not interfere, and you can remove it.

### 11.3 Apache httpd instead of nginx

Apache httpd 2.4 implements the same routing rules and is an approved alternative to nginx. Nothing else
in this guide changes: the certificate paths (§11.1), the PHP-FPM socket (§10), the SELinux labels and
booleans (§7) and the firewall (§12) are identical, because httpd runs in the same `httpd_t` domain that
§7 already provisions for nginx.

Its `clara_safe` access-log format uses `%U` (path only) and omits referrers; do not replace it with
`combined`, whose request line includes query parameters such as OAuth callback codes.

```bash
# RHEL: a drop-in in conf.d/. Debian/Ubuntu: copy to /etc/apache2/sites-available/ and a2ensite instead.
install -m 0644 deploy/apache2/clara.conf /etc/httpd/conf.d/clara.conf
apachectl -t && apachectl graceful
```

Three host-level differences from the nginx setup, each noted in the file's header:

| | nginx (§11.2) | Apache httpd (§11.3) |
|---|---|---|
| Modules | `http_ssl_module` | `mod_rewrite`, `mod_headers`, `mod_alias`, `mod_authz_core`, `mod_dir`, `mod_ssl`, `mod_proxy`, `mod_proxy_http`, `mod_proxy_fcgi` (unix-socket support needs httpd ≥ 2.4.7) |
| The distribution's own default host | a harmless `server` on port 80 | RHEL's `conf.d/ssl.conf` ships a **second `*:443` vhost** serving the welcome page — remove that vhost and keep its `Listen 443`, or a request with no SNI, or any other name, can answer something that is not CLARA. `ServerTokens Prod` belongs in the main configuration |
| Output compression | not enabled here | keep `mod_deflate` away from this vhost — a streamed export must not buffer (the file sets `no-gzip` for `/api/`) |

The rule-for-rule mapping is short enough to keep in mind: `client_max_body_size` ↔ `LimitRequestBody`,
`proxy_set_header X-Internal-* ""` ↔ `RequestHeader unset X-Internal-*`, `alias /assets/` ↔
`Alias /assets/`, `location = /api/ { proxy_pass … }` ↔ a `RewriteRule [P]` matching exactly `/api/` — and
deliberately no `ProxyPass` prefix, because mod_proxy answers before mod_rewrite could refuse
`/api/v1/*` — `fastcgi_pass unix:…` ↔ `SetHandler proxy:unix:…|fcgi://localhost` on `index.php` alone,
and `try_files $uri /index.php` ↔ a `RewriteCond !-f`/`!-d` pair that must live inside `<Directory>`: at
vhost level mod_rewrite runs before the URL is mapped to a file, where `-f` always matches and every real
asset would be routed to PHP.

Check either server with §13.1. `e2e/tests/deployment.spec.ts` automates those checks plus a parity check
of the two files against this guide (`Design/Technology_Stack_Design.md` §6); point it at a deployed front door
with `CLARA_DEPLOY_URL` and name the server with `CLARA_PROXY_KIND=nginx|apache`.

---

## 12. Firewall

Open only HTTP (for the redirect) and HTTPS. Ports 8080 (API) and 3306 (MariaDB) stay closed.

```bash
firewall-cmd --permanent --add-service=http --add-service=https
firewall-cmd --reload
firewall-cmd --list-services
```

---

## 13. First login and verification

### 13.1 Smoke checks

Run these from another machine (drop `-k` once a trusted certificate is installed). They hold for either
reverse proxy — nginx (§11.2) or Apache (§11.3) — and `e2e/tests/deployment.spec.ts` automates them:

```bash
H=https://clara.example.org
curl -sk -o /dev/null -w '%{http_code}\n' $H/login                          # 200
curl -sk -o /dev/null -w '%{http_code}\n' $H/                               # 302 → /login
curl -sk -o /dev/null -w '%{http_code} %{content_type}\n' \
     $H/assets/vendor/bootstrap/bootstrap.min.css                           # 200 text/css
curl -sk -o /dev/null -D - $H/assets/app.js | grep -i '^cache-control'     # Cache-Control: no-cache
curl -sk -w ' [%{http_code}]\n' -X POST -d token=0000 -d content=version $H/api/   # Invalid token [401] (from the API)
curl -sk -o /dev/null -w '%{http_code}\n' $H/api/                           # 403 (GET refused)
curl -sk -o /dev/null -w '%{http_code}\n' $H/api/v1/health                  # 404 (admin API hidden)
curl -sk -o /dev/null -w '%{http_code}\n' $H/docs                           # 404
curl -sk -o /dev/null -w '%{http_code}\n' --path-as-is $H/assets/../.env    # 404
curl -s  -o /dev/null -w '%{http_code} %{redirect_url}\n' http://clara.example.org/login   # 301 → https
```

On the server:

```bash
systemctl is-active mariadb clara-api php-fpm nginx     # active ×4
curl -s http://127.0.0.1:8080/healthz                   # {"db":"ok","status":"ok"}
```

### 13.2 First login

1. Open `https://clara.example.org/`. You are sent to `/login`.
2. Sign in with `ADMIN_BOOTSTRAP_EMAIL` and the `ADMIN_BOOTSTRAP_PASSWORD` generated in section 6 (`grep ^ADMIN_BOOTSTRAP_PASSWORD= /opt/clara/.env`).
3. Change that password right away at `/account/password`. Consider enrolling two-factor authentication at `/account/two-factor`.
4. Create users and projects from the Control Panel. REDCap-API clients then call `POST https://clara.example.org/api/` with their project token.
5. Open **Control Panel → Configuration Check**. It checks most of this guide from inside the running installation (PHP, `.env`, the API service, MariaDB settings and privileges, and the web server's routing as your browser sees it) and names the section of this guide for every finding.

---

## 14. Optional: sign-in sources, email, two-factor

All of these are `.env` settings. Restart afterwards (`systemctl restart clara-api && systemctl reload php-fpm`). The commented examples are in `/opt/clara/.env.example`.

| Feature | Variables | Notes |
|---|---|---|
| OAuth2 / OpenID Connect (up to 3) | `OAUTH2_n_ISSUER`, `OAUTH2_n_CLIENT_ID`, `OAUTH2_n_CLIENT_SECRET`, `OAUTH2_n_EMAIL_ATTR`, `OAUTH2_n_NAMES` | Register the redirect URI `https://clara.example.org/auth/callback` with the provider. It defaults to `WEB_PUBLIC_URL` + `/auth/callback`. |
| LDAP (up to 3) | `LDAP_SERVER_n_URL`, `LDAP_SERVER_n_SEARCH_BASE`, `LDAP_SERVER_n_BIND_DN`, `LDAP_SERVER_n_BIND_PASSWORD`, `…_UID_ATTR`, `…_EMAIL_ATTR`, `…_NAME_ATTR`, `…_NAMES` | Prefer `ldaps://`. Trust the directory's CA by copying it to `/etc/pki/ca-trust/source/anchors/` and running `update-ca-trust`. All directories under one login name are tried in parallel. |
| Login-page names | `LOCAL_LOGIN_NAMES`, `OAUTH2_n_NAMES`, `LDAP_SERVER_n_NAMES` | Comma-separated names users pick on the login page (REQ-CFG-032). Leave them all unset for one implicit default. |
| Email (login codes, invitations, password resets) | `SMTP_HOST`, `SMTP_PORT`, `SMTP_SECURITY` (`starttls`/`tls`/`none`), `SMTP_USERNAME`, `SMTP_PASSWORD`, `OTP_MAIL_FROM` | Without `SMTP_HOST` the email methods are unavailable. `OTP_MAIL_FROM` is required once `SMTP_HOST` is set. |
| Mandatory two-factor | `AUTH_REQUIRE_2FA=1` | Local and LDAP accounts must enroll (TOTP or email code) after their password step. |
| Default theme | `UI_THEME=bootstrap\|darkly\|yeti` | Users can override it personally. |
| Monitoring chart span | `MONITORING_PERIOD_MINUTES=10` | How far back the Control Panel's Monitoring charts reach, in minutes (1–1440). The page samples once a second while it is open and drops every older timepoint, so a tab left open all day keeps ten minutes and not the day. Set `60` for the old hour. |
| Rate limiting | *(no variable)* | Enable it and set the thresholds in the administration interface (system settings). It is off by default. |

`ADMIN_BOOTSTRAP_PASSWORD` may be left empty once at least one OAuth2 provider or LDAP server is configured.

### 14.1 The in-browser assistant (optional)

The assistant runs a model in the browser with WebGPU, so it needs no external API — but it does need
the model files, which are not in git. `tools/fetch_assistant_model.sh` pulls them into
`web/assets/models/` on a network-connected machine; copy that directory to
`/opt/clara/web/assets/models/` afterwards. Without the files the assistant is simply unavailable, and
everything else works (`Design/AI_Assistant_Design.md` §5).

The script installs nothing it cannot match against `tools/assistant_model_hashes.txt`, one SHA-256 per
artifact:

```console
# once, on a machine you trust, then review the diff and commit the file
tools/fetch_assistant_model.sh --pin                       # downloads, records hashes
git diff tools/assistant_model_hashes.txt                  # this review is the control
# every later run, air-gapped included — it verifies and writes nothing
tools/fetch_assistant_model.sh                             # fetches what is missing, checking hashes
tools/fetch_assistant_model.sh --verify                    # checks an existing install, no network
```

A byte count is not integrity here. The compiled `.wasm` runs as browser code under this application's
own origin, with the assistant's access to its API, so trusting transport security alone would mean
trusting whatever `huggingface.co` answers on the day the script happens to run — hence a manifest that
is produced once, reviewed like any other pinned dependency (REQ-TECH-021), and demanded afterwards.
The same hashes are written into `app-config.json` as the engine's own `integrity` record, so the check
repeats in the browser on every load: a model directory modified after install fails with an
`IntegrityError` instead of executing.

---

## 15. Operations: logs, backups, restore, upgrades

### 15.1 Services and logs

| Component | Service | Logs |
|---|---|---|
| API | `clara-api` | `journalctl -u clara-api` (structured `key=value` lines; secrets masked) |
| Web layer | `php-fpm` | `/var/log/php-fpm/error.log` (one JSON object per line, `"component":"web"`), PHP errors in `/var/log/php-fpm/clara-error.log` |
| nginx | `nginx` | `/var/log/nginx/clara.access.log`, `clara.error.log` |
| MariaDB | `mariadb` | `journalctl -u mariadb` |

The RHEL packages already rotate the nginx and PHP-FPM logs. For monitoring, poll `http://127.0.0.1:8080/healthz` on the host. It returns HTTP 503 with `"db":"error"` when the database is unreachable.

### 15.2 Backups

Back up two things: the **database** and **`/opt/clara/.env`**. The `.env` file holds `ANON_SALT` and the service token; without the same `ANON_SALT`, anonymized exports change. Save this as `/usr/local/sbin/clara-backup`:

```bash
#!/usr/bin/env bash
# /usr/local/sbin/clara-backup — logical backup of the CLARA database and its .env
set -euo pipefail
umask 077
dir=/var/backups/clara
stamp=$(date +%Y%m%d-%H%M%S)
mkdir -p "$dir"
mariadb-dump --single-transaction --routines --triggers --databases clara \
  | gzip > "$dir/clara-$stamp.sql.gz"
cp /opt/clara/.env "$dir/env-$stamp"
# keep 14 days locally; ship copies off the host according to your backup policy
find "$dir" -type f -mtime +14 -delete
```

```bash
chmod 0750 /usr/local/sbin/clara-backup
echo '30 2 * * * root /usr/local/sbin/clara-backup' > /etc/cron.d/clara-backup
```

`--single-transaction` gives a consistent dump without stopping CLARA. The dump includes the yearly audit partitions. The backups contain study data and secrets: encrypt them and copy them off the host as your data-protection rules require.

### 15.3 Restore

```bash
systemctl stop clara-api
mariadb -e "DROP DATABASE clara"
zcat /var/backups/clara/clara-<stamp>.sql.gz | mariadb     # recreates the database
clara-db-grants runtime                                    # re-applies table grants
systemctl start clara-api
```

If you restore onto a new host, restore the matching `env-<stamp>` file as `/opt/clara/.env` too, and create the database account first (section 8.2, without `CREATE DATABASE`).

### 15.4 Upgrades

Database migrations run only forward, so always take a backup first.

```bash
# 0. build the new release as in section 3; on the build machine, check for new
#    configuration keys:  git diff <old>..<new> -- .env.example
clara-backup

# 1. let the API run migrations
clara-db-grants install

# 2. swap the code (keep the previous version for a rollback)
systemctl stop clara-api
cd /opt/clara && mv bin bin.previous && mv web web.previous
tar -xzf /tmp/clara-<new>.tar.gz -C /opt --no-same-owner
chown -R root:root /opt/clara/bin /opt/clara/web
chmod -R u=rwX,go=rX /opt/clara/web
restorecon -R /opt/clara

# 3. migrate and check
systemctl start clara-api
journalctl -u clara-api -n 20 --no-pager       # "listening … schema_version=<new>"

# 4. back to runtime privileges; restart so every connection picks them up
systemctl stop clara-api
clara-db-grants runtime
systemctl start clara-api
systemctl reload php-fpm

# 5. verify (section 13.1 and Control Panel → Configuration Check),
#    then remove the previous version
rm -rf /opt/clara/bin.previous /opt/clara/web.previous
```

**Rollback:** stop `clara-api`, move `bin.previous` and `web.previous` back, restore the backup from step 0 (section 15.3), then start again.

If you skip step 1, a migration that needs more than the runtime rights stops the API at startup with `apply migration …: … command denied`. Run `clara-db-grants install` and continue with step 3. Always run step 4, even when startup succeeded: only step 4 gives tables created by new migrations their `UPDATE`/`DELETE` grants.

### 15.5 Audit tables

Each audit table has one partition per year (`p2026`, `p2027`, …). The API creates the current and the next year's partition at startup and on the first audit write of a new year. Nothing needs scheduling. Removing old years is a deliberate retention decision. Only an administrator can do it, as MariaDB `root` (`ALTER TABLE audit_events DROP PARTITION p<year>`); the application account cannot.

---

## 16. Troubleshooting

| Symptom | Likely cause and fix |
|---|---|
| `clara-api` exits right after start | `journalctl -u clara-api`: after `configuration invalid:` every bad variable is listed, e.g. a development default for `INTERNAL_SERVICE_TOKEN`/`ANON_SALT` with `APP_ENV=production`, or a bootstrap password under 12 characters. |
| `ping database (mariadb): … Access denied` | `DB_PASSWORD` differs from the account's password, or `DB_HOST` is not `127.0.0.1` (the account is `'clara'@'127.0.0.1'`). |
| `apply migration …: … command denied` | Upgrade without `clara-db-grants install` (section 15.4). |
| Browser shows "The web application is misconfigured" | The web layer refused its configuration. The reason is in `/var/log/php-fpm/error.log` (`"configuration refused startup"`). Common causes: `.env` not readable by `clara` (mode/owner or SELinux label), or `SESSION_DIR` not writable. |
| `502 Bad Gateway` on pages | PHP-FPM is not running, or the socket is not `/run/php-fpm/clara.sock`. Check `systemctl status php-fpm`. |
| `502 Bad Gateway` on `POST /api/`, or pages report the service unavailable | API down (`systemctl status clara-api`), or SELinux blocks the connection (`getsebool httpd_can_network_connect` must be `on`). |
| Login seems to work but you land on the login page again | The session cookie is HTTPS-only (`SESSION_COOKIE_SECURE=1`, named `__Host-csms_session`). Use `https://` and the exact host name in `WEB_PUBLIC_URL` — the `__Host-` cookie is not shared with any other host or subdomain. |
| Users report "signed out everywhere" | A session id was used again after it had been replaced: either a copied cookie, or a response that never reached the browser followed by more than a minute without a request. Each case is in the audit log as `session_replay_detected` with the address. Repeated cases from one address warrant a closer look. |
| `413 Request Entity Too Large` on an import | Body over `client_max_body_size` (32 MiB, the API's own limit). |
| LDAP login always says the sign-in service cannot be reached | Network/firewall towards the directory, untrusted `ldaps` CA, `php-cli` missing, or `proc_open` disabled in `php.ini`. |
| Anything works with `setenforce 0` but not with SELinux on | `ausearch -m AVC -ts recent`; re-run the labels and booleans of section 7. |

---

## 17. Security checklist

Control Panel → **Configuration Check** verifies most of these items automatically; the firewall, the certificate's trust and the backups remain yours to check.

- [ ] `APP_ENV=production`; all four secrets are random and unique to this installation.
- [ ] `/opt/clara/.env` is `root:clara 0640`; the code under `/opt/clara` is owned by root.
- [ ] `clara-db-grants runtime` has been applied, and `clara-api` restarted after it.
- [ ] MariaDB listens on `127.0.0.1` only; `mariadb-secure-installation` has been run.
- [ ] Only ports 80/443 are open; `/api/v1/*`, `/docs`, `/healthz` return 404 from outside (section 13.1).
- [ ] A trusted TLS certificate is installed; HTTP redirects to HTTPS.
- [ ] SELinux is enforcing.
- [ ] `chronyd` is running.
- [ ] Nightly backups run, are copied off the host, and a restore has been tried once.
- [ ] The bootstrap administrator's password has been changed after the first login.
