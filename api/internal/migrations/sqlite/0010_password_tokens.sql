-- Out-of-band password setup (REQ-DB-039, GD-22/GD-23): the single-use
-- invite and reset tokens of Sequence H. Only the SHA-256 hash of the emailed
-- token is stored — the value itself exists only in the email and the
-- completion request and never lands here or in any log (REQ-AUTH-060/062).
-- purpose is enforced in code ('invite' | 'reset'), mirroring how ui_theme
-- enumerations are handled; expiry is checked at redemption, not by a job.
-- Re-issue replaces the outstanding row of the same purpose for the account
-- (a re-invite kills the old link), so no uniqueness beyond token_hash is
-- needed. created_by names the inviting administrator; NULL marks a
-- self-service reset request (no acting user).

CREATE TABLE IF NOT EXISTS password_tokens (
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,               -- SHA-256 hex of the emailed token
    purpose      TEXT NOT NULL,                      -- invite | reset
    created_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL,                      -- UTC "YYYY-MM-DD HH:MM:SS"
    expires_at   TEXT NOT NULL,                      -- now + AUTH_PASSWORD_TOKEN_TTL_DAYS
    consumed_at  TEXT                                -- UTC on first successful completion
);

CREATE INDEX IF NOT EXISTS idx_password_tokens_user ON password_tokens(user_id, purpose);
