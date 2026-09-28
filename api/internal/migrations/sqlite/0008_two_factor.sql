-- By-user two-factor authentication (REQ-DB-038, DEV-DB-12; GD-21): the
-- second factor of a local or LDAP login. Sparse on purpose — absence of a
-- row means "off", so enabling 2FA is an insert and disabling it a delete;
-- no column on `users` is touched (mirrors REQ-DB-036). All challenge state
-- lives here, keeping the login endpoint itself stateless (DEV-API-15).
--
-- method 'totp' stores the shared secret and the last accepted time step
-- (replay prevention); method 'email' stores only the SHA-256 hash of the
-- pending code plus its expiry — a delivered code is never persisted in the
-- clear. recovery_codes holds the JSON array of one-time recovery-code
-- hashes issued at activation; codes themselves are shown exactly once and
-- never logged (REQ-AUTH-059).

CREATE TABLE IF NOT EXISTS user_two_factor (
    user_id               INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    method                TEXT NOT NULL DEFAULT 'off',  -- off | totp | email
    totp_secret           TEXT,                         -- base32 shared secret
    totp_last_step        INTEGER,                      -- last accepted TOTP step
    email_code_hash       TEXT,                         -- SHA-256 of pending code
    email_code_expires_at TEXT,                         -- RFC3339 UTC expiry
    recovery_codes        TEXT NOT NULL DEFAULT '[]',   -- JSON [{"h":…,"used":bool}]
    enrolled_at           TEXT
);
