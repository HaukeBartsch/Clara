# System Configuration Plan

This document outlines the configuration management for the clinical study management system, supporting different environments.

## Configuration Management
- Method: Environment variables stored in a .env file (not tracked in version control).
- Example Configuration:

# Environment

```yaml
APP_ENV=development
```

# Database

```yaml
DB_CONNECTION=sqlite
DB_DATABASE=/path/to/database.sqlite
# DB_CONNECTION=mariadb
# DB_HOST=localhost
# DB_PORT=3306
# DB_DATABASE=clinical_db
# DB_USERNAME=root
# DB_PASSWORD=secret
```

# Authentication

```yaml
OAUTH_PROVIDER_URL=https://auth.example.com
LDAP_SERVER_1=ldap://ldap1.example.com
LDAP_SERVER_2=ldap://ldap2.example.com
LDAP_SERVER_3=ldap://ldap3.example.com
```

# Two-factor authentication (GD-21)

```yaml
AUTH_REQUIRE_2FA=0
TOTP_ISSUER=CLARA
TFA_EMAIL_CODE_TTL=600
SMTP_HOST=mail.internal.example.org
SMTP_PORT=587
SMTP_SECURITY=starttls
SMTP_USERNAME=
SMTP_PASSWORD=
OTP_MAIL_FROM=clara-noreply@example.org
```

# Key Settings
- APP_ENV: Toggles between development (SQLite default) and production (MariaDB).
- DB_*: Database connection details.
- OAUTH_ / LDAP_*:* Authentication server details.
- Two-factor (GD-21): `AUTH_REQUIRE_2FA` (installation-wide mandate, default off), `TOTP_ISSUER`, `TFA_EMAIL_CODE_TTL`, and the SMTP relay (`SMTP_*`, `OTP_MAIL_FROM`) that delivers email codes — optional; without a relay the `email` method is unavailable. Security posture keys stay environment-only, unlike the runtime-edited rate-limit settings (REQ-CFG-027/028/029).
- Runtime system settings are not environment variables: the rate-limit enable flag, the requests-per-minute threshold per source IP and the blockout period an over-budget IP stays blocked live in the `system_settings` table and are edited in the administration interface (REQ-CFG-020, REQ-API-112/113). Only `TRUSTED_PROXY_CIDRS` — which proxy addresses may supply the caller IP for rate limiting — stays an environment variable.
