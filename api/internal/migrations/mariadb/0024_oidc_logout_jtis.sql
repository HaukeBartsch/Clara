-- Logout-token replay window (REQ-DB-057, REQ-AUTH-098, Sequence L): one row per provider
-- back-channel logout token CLARA accepted. The key is the SHA-256 hex of the issuer, a zero
-- byte and the token's jti, so it has a fixed width whatever the provider writes there and one
-- provider cannot block another's jti; a second insert of the same key is the replay.
--
-- A rolling window, not a log: every accept first deletes the rows whose expires_at has
-- passed. Past that moment (the token's iat + 10 minutes + the 120 s clock-skew allowance) the
-- token is refused by its age anyway. The row is written in the transaction of the sign-out
-- it records, so a failed sign-out leaves none and the provider's retry is not mistaken for a
-- replay. No account is stored here: provider_logout in the audit trail is the history.

CREATE TABLE IF NOT EXISTS oidc_logout_jtis (
    key_hash    CHAR(64) NOT NULL,              -- SHA-256 hex of issuer || 0x00 || jti
    expires_at  DATETIME NOT NULL,              -- UTC; the token's iat + 10 min + 120 s skew
    created_at  DATETIME NOT NULL,
    PRIMARY KEY (key_hash),
    KEY idx_oidc_logout_jtis_expires (expires_at)
) ENGINE=InnoDB;
