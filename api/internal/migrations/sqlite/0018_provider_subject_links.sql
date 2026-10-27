-- Provider subject binding (REQ-AUTH-080, DEV-AUTH-18): the (iss, sub) pair a
-- verified provider assertion named, against the account it resolved to. Account
-- resolution prefers this row over email equality, which is what stops an address
-- changed or reassigned at the identity provider from moving an account into
-- different hands — trusting the address is what had been done, and finding F10 is
-- what that trust cost (DEV-AUTH-15).
--
-- `subject` is stored verbatim and never parsed: a pairwise identifier is opaque
-- (REQ-AUTH-080), 255 characters being OIDC Core's declared maximum. No token,
-- signature or other claim material lives here — verification happens before this
-- row is consulted or written (REQ-AUTH-036), and the audit trail carries the same
-- pair without any of it (REQ-AUTH-084).
--
-- The pair is the key, not the user: one account may be reachable through several
-- providers, and one provider subject names one account. idx_…user serves the
-- reverse question an administrator asks on a conflict — what else is this account
-- linked to (Sequence J step 8).

CREATE TABLE IF NOT EXISTS provider_subject_links (
    issuer      TEXT NOT NULL,
    subject     TEXT NOT NULL,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL,              -- UTC "YYYY-MM-DD HH:MM:SS" of the first verified login
    PRIMARY KEY (issuer, subject)
);

CREATE INDEX IF NOT EXISTS idx_provider_subject_links_user ON provider_subject_links(user_id);
