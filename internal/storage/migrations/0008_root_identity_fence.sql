-- M1.6 pins the enrolled directory object independently of its mutable path.
-- Existing rows have no trustworthy historical identity and must be verified
-- by a host-side operator before a scan can reconcile absence.
ALTER TABLE library_roots
    ADD COLUMN root_identity_kind text,
    ADD COLUMN root_identity_scope text,
    ADD COLUMN root_identity_id bytea,
    ADD COLUMN root_identity_birth_token bytea,
    ADD COLUMN verification_state text NOT NULL DEFAULT 'unverified',
    ADD COLUMN verified_at timestamptz,
    ADD CONSTRAINT library_roots_identity_kind_scope_check CHECK ((root_identity_kind IS NULL) = (root_identity_scope IS NULL)),
    ADD CONSTRAINT library_roots_identity_id_check CHECK ((root_identity_kind IS NULL) = (root_identity_id IS NULL)),
    ADD CONSTRAINT library_roots_identity_birth_check CHECK (root_identity_birth_token IS NULL OR root_identity_id IS NOT NULL),
    ADD CONSTRAINT library_roots_identity_kind_length_check CHECK (root_identity_kind IS NULL OR length(root_identity_kind) BETWEEN 1 AND 64),
    ADD CONSTRAINT library_roots_identity_scope_length_check CHECK (root_identity_scope IS NULL OR length(root_identity_scope) BETWEEN 1 AND 256),
    ADD CONSTRAINT library_roots_identity_id_length_check CHECK (root_identity_id IS NULL OR octet_length(root_identity_id) BETWEEN 1 AND 256),
    ADD CONSTRAINT library_roots_identity_birth_length_check CHECK (root_identity_birth_token IS NULL OR octet_length(root_identity_birth_token) BETWEEN 1 AND 256),
    ADD CONSTRAINT library_roots_verification_state_check CHECK (verification_state IN ('unverified','verified','quarantined')),
    ADD CONSTRAINT library_roots_verification_evidence_check CHECK (
        (verification_state='unverified' AND verified_at IS NULL)
        OR
        (verification_state IN ('verified','quarantined') AND root_identity_kind IS NOT NULL AND verified_at IS NOT NULL)
    );

CREATE INDEX library_roots_enabled_verification_idx
    ON library_roots(verification_state,enabled,created_at,id);
