-- Sign-in grants for registered clients (REQ-DB-054, REQ-AUTH-092…095, Sequence K):
-- CLARA signs a user in to another application of the installation — FIONA — by
-- handing that application's server a single-use code it redeems for the identity.
--
-- One row is one authorization: born as a code when /sso/authorize runs for a
-- signed-in browser, it becomes a grant when the client redeems the code, and it
-- ends when CLARA signs that session out, when its lifetime runs out, or when the
-- account stops being active. Only SHA-256 hashes of the three random values are
-- stored — the code, the grant handle the client keeps, and the CLARA session's
-- sign-in id — never the values themselves (REQ-AUTH-036).
--
-- session_epoch is the account's epoch when the code was issued: raising it ends
-- every CLARA session of the account (REQ-AUTH-074), and the client's grants with
-- them. expires_at is the CLARA session's own absolute end (sign-in +
-- SESSION_LIFETIME), so a client is never signed in longer than the CLARA session
-- it came from could have lasted. Expired rows are removed when new ones are
-- written; no job runs.

CREATE TABLE IF NOT EXISTS sso_grants (
    id               INTEGER PRIMARY KEY AUTO_INCREMENT,
    client_id        VARCHAR(64) NOT NULL,       -- SSO_CLIENT_N_ID
    user_id          INTEGER NOT NULL,           -- same type as users.id (FK)
    sid_hash         CHAR(64) NOT NULL,          -- SHA-256 hex of the CLARA session's sign-in id
    code_hash        CHAR(64) NOT NULL,          -- SHA-256 hex of the authorization code
    code_challenge   VARCHAR(128) NOT NULL,      -- PKCE S256 challenge, base64url
    redirect_uri     VARCHAR(2048) NOT NULL,     -- the registered URI the code was sent to
    session_epoch    INTEGER NOT NULL,
    code_expires_at  DATETIME NOT NULL,          -- UTC, issue + 60 s
    grant_hash       CHAR(64),                   -- SHA-256 hex of the grant handle; NULL until redeemed
    redeemed_at      DATETIME,
    expires_at       DATETIME NOT NULL,          -- CLARA session sign-in + SESSION_LIFETIME
    ended_at         DATETIME,                   -- sign-out, or a refused redemption
    created_at       DATETIME NOT NULL,
    UNIQUE KEY uq_sso_grants_code (code_hash),
    UNIQUE KEY uq_sso_grants_grant (grant_hash),
    KEY idx_sso_grants_sid (sid_hash),
    KEY idx_sso_grants_expires (expires_at),
    CONSTRAINT fk_sso_grants_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB;
