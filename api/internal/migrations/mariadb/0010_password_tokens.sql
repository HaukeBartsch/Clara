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
    user_id      BIGINT NOT NULL,
    token_hash   CHAR(64) NOT NULL,
    purpose      VARCHAR(6) NOT NULL,                -- invite | reset
    created_by   BIGINT,
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,                  -- now + AUTH_PASSWORD_TOKEN_TTL_DAYS
    consumed_at  DATETIME,                           -- UTC on first successful completion
    UNIQUE KEY uq_password_tokens_hash (token_hash),
    KEY idx_password_tokens_user (user_id, purpose),
    CONSTRAINT fk_password_tokens_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_password_tokens_creator FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB;
