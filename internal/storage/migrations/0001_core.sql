-- M1 source-of-truth identities. No demo-track row is inserted.
CREATE TABLE tracks (
    id text PRIMARY KEY,
    title text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tracks_id_nonempty CHECK (length(id) BETWEEN 1 AND 128)
);

CREATE TABLE media_objects (
    id text PRIMARY KEY,
    track_id text NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
    sha256 bytea NOT NULL UNIQUE,
    format text NOT NULL,
    byte_length bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT media_objects_id_nonempty CHECK (length(id) BETWEEN 1 AND 128),
    CONSTRAINT media_objects_hash_length CHECK (octet_length(sha256) = 32),
    CONSTRAINT media_objects_format_nonempty CHECK (length(format) BETWEEN 1 AND 32),
    CONSTRAINT media_objects_byte_length CHECK (byte_length >= 0)
);

CREATE TABLE media_locations (
    id text PRIMARY KEY,
    media_object_id text NOT NULL REFERENCES media_objects(id) ON DELETE RESTRICT,
    local_path text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT media_locations_id_nonempty CHECK (length(id) BETWEEN 1 AND 128),
    CONSTRAINT media_locations_path_nonempty CHECK (length(local_path) > 0)
);
