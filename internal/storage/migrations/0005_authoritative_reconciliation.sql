-- M1.3A scan generations: retain old physical occurrences and reconcile only
-- after a complete, stable traversal.

ALTER TABLE media_locations
    ADD COLUMN availability text NOT NULL DEFAULT 'available'
        CHECK (availability IN ('available', 'unavailable')),
    ADD COLUMN unavailable_reason text,
    ADD COLUMN unavailable_at timestamptz,
    ADD COLUMN observed_size bigint CHECK (observed_size IS NULL OR observed_size >= 0),
    ADD COLUMN observed_mtime_ns bigint,
    ADD COLUMN native_id_kind text,
    ADD COLUMN native_id_scope text,
    ADD COLUMN native_id bytea,
    ADD COLUMN native_birth_token bytea,
    ADD COLUMN last_seen_run_id uuid,
    ADD COLUMN state_updated_at timestamptz NOT NULL DEFAULT now(),
    ADD CONSTRAINT media_locations_availability_state_check CHECK (
        (availability = 'available' AND unavailable_reason IS NULL AND unavailable_at IS NULL)
        OR
        (availability = 'unavailable' AND unavailable_reason IS NOT NULL AND unavailable_at IS NOT NULL)
    ),
    ADD CONSTRAINT media_locations_native_identity_check CHECK (
        (native_id_kind IS NULL AND native_id_scope IS NULL AND native_id IS NULL)
        OR
        (native_id_kind IS NOT NULL AND native_id_scope IS NOT NULL AND native_id IS NOT NULL)
    );

UPDATE media_locations AS ml
SET observed_size = mo.byte_length
FROM media_objects AS mo
WHERE mo.id = ml.media_object_id;

DROP INDEX media_locations_root_relative_idx;
CREATE UNIQUE INDEX media_locations_active_root_relative_idx
    ON media_locations(root_id, relative_path)
    WHERE root_id IS NOT NULL AND availability = 'available';
CREATE INDEX media_locations_root_availability_idx
    ON media_locations(root_id, availability);
CREATE INDEX media_locations_native_lookup_idx
    ON media_locations(root_id, native_id_kind, native_id_scope, native_id)
    WHERE availability = 'available' AND native_id IS NOT NULL;

ALTER TABLE scan_runs
    ADD COLUMN phase text NOT NULL DEFAULT 'discovering'
        CHECK (phase IN ('discovering', 'publishing', 'finished')),
    ADD COLUMN traversal_complete boolean NOT NULL DEFAULT false,
    ADD COLUMN observations_applied boolean NOT NULL DEFAULT false,
    ADD COLUMN absence_reconciled boolean NOT NULL DEFAULT false,
    ADD COLUMN files_unchanged bigint NOT NULL DEFAULT 0,
    ADD COLUMN files_hashed bigint NOT NULL DEFAULT 0,
    ADD COLUMN stat_changed_same_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN changed_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN locations_added bigint NOT NULL DEFAULT 0,
    ADD COLUMN locations_moved bigint NOT NULL DEFAULT 0,
    ADD COLUMN locations_unavailable bigint NOT NULL DEFAULT 0,
    ADD COLUMN media_objects_created bigint NOT NULL DEFAULT 0,
    ADD COLUMN tracks_created bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT scan_runs_counters_nonnegative CHECK (
        files_visited >= 0 AND files_supported >= 0 AND imported >= 0 AND skipped >= 0 AND failed >= 0
        AND bytes_hashed >= 0 AND metadata_extractions >= 0 AND files_unchanged >= 0 AND files_hashed >= 0
        AND stat_changed_same_bytes >= 0 AND changed_bytes >= 0 AND locations_added >= 0
        AND locations_moved >= 0 AND locations_unavailable >= 0 AND media_objects_created >= 0 AND tracks_created >= 0
    ),
    ADD CONSTRAINT scan_runs_root_id_id_key UNIQUE (root_id, id);

-- Existing M1.2 runs are terminal history, not active M1.3A traversals.
UPDATE scan_runs SET phase = 'finished' WHERE status <> 'running';

ALTER TABLE library_roots
    ADD COLUMN last_successful_scan_id uuid,
    ADD CONSTRAINT library_roots_last_successful_scan_fkey
        FOREIGN KEY (id, last_successful_scan_id) REFERENCES scan_runs(root_id, id);

ALTER TABLE media_locations
    ADD CONSTRAINT media_locations_last_seen_run_fkey
        FOREIGN KEY (root_id, last_seen_run_id) REFERENCES scan_runs(root_id, id);

ALTER TABLE tracks
    ADD COLUMN metadata_source_location_id text,
    ADD CONSTRAINT tracks_metadata_source_location_fkey
        FOREIGN KEY (metadata_source_location_id) REFERENCES media_locations(id)
        DEFERRABLE INITIALLY DEFERRED;

UPDATE tracks AS t
SET metadata_source_location_id = (
    SELECT ml.id
    FROM media_objects AS mo
    JOIN media_locations AS ml ON ml.media_object_id = mo.id
    WHERE mo.track_id = t.id
    ORDER BY ml.created_at, ml.id
    LIMIT 1
)
WHERE EXISTS (
    SELECT 1
    FROM media_objects AS mo
    JOIN media_locations AS ml ON ml.media_object_id = mo.id
    WHERE mo.track_id = t.id
);
