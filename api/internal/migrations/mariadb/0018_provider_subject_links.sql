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
-- providers, and one provider subject names one account. KEY …user serves the
-- reverse question an administrator asks on a conflict — what else is this account
-- linked to (Sequence J step 8).
--
-- Key-size note for InnoDB: the primary key is 500 + 255 characters, which fits the
-- 3072-byte limit of the default DYNAMIC row format even as utf8mb4 (3020 bytes). A
-- deployment that has lowered innodb_default_row_format to COMPACT (limit 767) must
-- raise it back rather than shorten these columns — a silently truncated subject
-- would bind two people to one account.

CREATE TABLE IF NOT EXISTS provider_subject_links (
    issuer      VARCHAR(500) NOT NULL,      -- same value as OAUTH2_N_ISSUER
    subject     VARCHAR(255) NOT NULL,
    user_id     INTEGER NOT NULL,
    created_at  DATETIME NOT NULL,          -- UTC, first verified login
    PRIMARY KEY (issuer, subject),
    KEY idx_provider_subject_links_user (user_id),
    CONSTRAINT fk_provider_subject_links_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB;
