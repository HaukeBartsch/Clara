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
ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_region VARCHAR(16);
ALTER TABLE users ADD COLUMN IF NOT EXISTS automatic_region VARCHAR(16);
ALTER TABLE users ADD COLUMN IF NOT EXISTS automatic_region_source VARCHAR(10);

-- The provider role mapping (REQ-DB-053): one row per (issuer, role value) a sign-in
-- carried or an administrator entered. There is deliberately no user column — which
-- roles a person holds is not stored, so a changed mapping reaches an account at its
-- next sign-in (DEV-DB-18). region NULL is "undecided" while reviewed = 0 and "names
-- no region" once reviewed = 1.
--
-- Key-size note for InnoDB: the unique key is 500 + 255 characters, 3020 bytes in
-- utf8mb4 — the same width as provider_subject_links' primary key, so it needs the
-- default DYNAMIC row format just as that table does.
CREATE TABLE IF NOT EXISTS provider_role_regions (
    id            INTEGER PRIMARY KEY AUTO_INCREMENT,
    issuer        VARCHAR(500) NOT NULL,      -- same value as OAUTH2_N_ISSUER
    role_value    VARCHAR(255) NOT NULL,      -- verbatim as the provider sent it
    region        VARCHAR(16),
    reviewed      INTEGER NOT NULL DEFAULT 0,
    first_seen_at DATETIME,                   -- UTC; NULL for a value entered before any sign-in carried it
    last_seen_at  DATETIME,                   -- UTC; the latest sign-in that carried it
    seen_count    INTEGER NOT NULL DEFAULT 0,
    UNIQUE KEY uq_provider_role_regions (issuer, role_value)
) ENGINE=InnoDB;
