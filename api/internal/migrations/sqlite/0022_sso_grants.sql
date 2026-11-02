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
    id               INTEGER PRIMARY KEY,
    client_id        TEXT NOT NULL,              -- SSO_CLIENT_N_ID
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sid_hash         TEXT NOT NULL,              -- SHA-256 hex of the CLARA session's sign-in id
    code_hash        TEXT NOT NULL UNIQUE,       -- SHA-256 hex of the authorization code
    code_challenge   TEXT NOT NULL,              -- PKCE S256 challenge, base64url
    redirect_uri     TEXT NOT NULL,              -- the registered URI the code was sent to
    session_epoch    INTEGER NOT NULL,
    code_expires_at  TEXT NOT NULL,              -- UTC "YYYY-MM-DD HH:MM:SS", issue + 60 s
    grant_hash       TEXT UNIQUE,                -- SHA-256 hex of the grant handle; NULL until redeemed
    redeemed_at      TEXT,
    expires_at       TEXT NOT NULL,              -- CLARA session sign-in + SESSION_LIFETIME
    ended_at         TEXT,                       -- sign-out, or a refused redemption
    created_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sso_grants_sid ON sso_grants(sid_hash);
CREATE INDEX IF NOT EXISTS idx_sso_grants_expires ON sso_grants(expires_at);
