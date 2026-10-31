-- Preferred region (REQ-DB-052/053, master spec "Organization by hospital regions").
-- A user's region is a projects.organization code that decides which projects the
-- overview shows first (REQ-UI-076). It orders presentation only: no authorization
-- path reads these columns or the mapping (REQ-AUTH-089).
--
-- preferred_region is the user's own choice (NULL = automatic); automatic_region is
-- set from provider roles at sign-in or as the placeholder of an administrator's
-- project assignment, and automatic_region_source says which (provider | assignment,
-- NULL exactly when automatic_region is). The effective region is
-- COALESCE(preferred_region, automatic_region) and is never stored (DEV-DB-18).
-- The codes are checked against the API's allowlist, not a CHECK (REQ-DB-006).
ALTER TABLE users ADD COLUMN preferred_region TEXT;
ALTER TABLE users ADD COLUMN automatic_region TEXT;
ALTER TABLE users ADD COLUMN automatic_region_source TEXT;

-- The provider role mapping (REQ-DB-053): one row per (issuer, role value) a sign-in
-- carried or an administrator entered. There is deliberately no user column — which
-- roles a person holds is not stored, so a changed mapping reaches an account at its
-- next sign-in (DEV-DB-18). region NULL is "undecided" while reviewed = 0 and "names
-- no region" once reviewed = 1.
CREATE TABLE IF NOT EXISTS provider_role_regions (
    id            INTEGER PRIMARY KEY,
    issuer        TEXT NOT NULL,              -- same value as OAUTH2_N_ISSUER
    role_value    TEXT NOT NULL,              -- verbatim, at most 255 characters
    region        TEXT,
    reviewed      INTEGER NOT NULL DEFAULT 0,
    first_seen_at TEXT,                       -- UTC; NULL for a value entered before any sign-in carried it
    last_seen_at  TEXT,                       -- UTC; the latest sign-in that carried it
    seen_count    INTEGER NOT NULL DEFAULT 0,
    UNIQUE (issuer, role_value)
);
