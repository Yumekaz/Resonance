-- M1.2 initial import only. No deletion, reconciliation, or watcher state.
CREATE TABLE library_roots (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    canonical_path text NOT NULL CHECK (length(canonical_path) > 0),
    path_key text NOT NULL UNIQUE,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE artists (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    display_name text NOT NULL CHECK (length(display_name) > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE albums (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    title text NOT NULL CHECK (length(title) > 0),
    album_artist_credit text,
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE tracks
    ADD COLUMN artist_id text REFERENCES artists(id),
    ADD COLUMN album_id text REFERENCES albums(id),
    ADD COLUMN artist_credit text,
    ADD COLUMN album_artist_credit text,
    ADD COLUMN track_number integer CHECK (track_number > 0),
    ADD COLUMN disc_number integer CHECK (disc_number > 0),
    ADD COLUMN release_year integer CHECK (release_year BETWEEN 1 AND 9999),
    ADD COLUMN genre text,
    ADD COLUMN title_source text CHECK (title_source IN ('tag','filename'));

ALTER TABLE media_objects
    ADD COLUMN artwork_sha256 bytea CHECK (artwork_sha256 IS NULL OR octet_length(artwork_sha256) = 32),
    ADD COLUMN artwork_mime text;

ALTER TABLE media_locations
    ADD COLUMN root_id uuid REFERENCES library_roots(id),
    ADD COLUMN relative_path text,
    ADD CONSTRAINT media_locations_root_pair CHECK ((root_id IS NULL) = (relative_path IS NULL));
CREATE UNIQUE INDEX media_locations_root_relative_idx ON media_locations(root_id, relative_path) WHERE root_id IS NOT NULL;

CREATE TABLE scan_runs (
    id uuid PRIMARY KEY,
    root_id uuid NOT NULL REFERENCES library_roots(id),
    status text NOT NULL CHECK (status IN ('running','succeeded','partial','failed','canceled')),
    files_visited bigint NOT NULL DEFAULT 0,
    files_supported bigint NOT NULL DEFAULT 0,
    imported bigint NOT NULL DEFAULT 0,
    skipped bigint NOT NULL DEFAULT 0,
    failed bigint NOT NULL DEFAULT 0,
    bytes_hashed bigint NOT NULL DEFAULT 0,
    metadata_extractions bigint NOT NULL DEFAULT 0,
    error_code text,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX scan_runs_root_started_idx ON scan_runs(root_id, started_at DESC);

CREATE TABLE scan_errors (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    relative_path text NOT NULL CHECK (length(relative_path) <= 512),
    code text NOT NULL CHECK (length(code) BETWEEN 1 AND 64),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scan_errors_run_idx ON scan_errors(run_id);
