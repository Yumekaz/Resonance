-- M1.5 single-owner durable user-library state. All references are Track IDs.
CREATE TABLE active_queue (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    current_item_id text,
    selection_token uuid,
    selection_state text NOT NULL DEFAULT 'stopped' CHECK (selection_state IN ('stopped','selected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT active_queue_selection_check CHECK (
        (selection_state = 'stopped' AND selection_token IS NULL)
        OR (selection_state = 'selected' AND current_item_id IS NOT NULL AND selection_token IS NOT NULL)
    )
);
INSERT INTO active_queue(singleton) VALUES(true);

CREATE TABLE queue_items (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    track_id text NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
    position bigint NOT NULL CHECK (position >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_skip_code text CHECK (last_skip_code IS NULL OR last_skip_code IN ('track_unavailable','resolver_failed')),
    last_skipped_at timestamptz,
    CONSTRAINT queue_items_skip_pair CHECK ((last_skip_code IS NULL) = (last_skipped_at IS NULL)),
    CONSTRAINT queue_items_position_key UNIQUE(position) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX queue_items_track_idx ON queue_items(track_id);
ALTER TABLE active_queue ADD CONSTRAINT active_queue_current_item_fkey
    FOREIGN KEY (current_item_id) REFERENCES queue_items(id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE playlists (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 256),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX playlists_created_idx ON playlists(created_at DESC,id DESC);

CREATE TABLE playlist_items (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    playlist_id text NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    track_id text NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
    position bigint NOT NULL CHECK (position >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT playlist_items_position_key UNIQUE(playlist_id,position) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX playlist_items_track_idx ON playlist_items(track_id);

CREATE TABLE favorite_tracks (
    track_id text PRIMARY KEY REFERENCES tracks(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX favorite_tracks_created_idx ON favorite_tracks(created_at DESC,track_id DESC);

CREATE TABLE playback_sessions (
    id uuid PRIMARY KEY,
    track_id text NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
    queue_item_id text,
    selection_token uuid,
    client_instance_id uuid NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    meaningful_at timestamptz,
    completed_at timestamptz,
    ended_at timestamptz,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    listened_ms bigint NOT NULL DEFAULT 0 CHECK (listened_ms >= 0),
    last_position_ms bigint NOT NULL DEFAULT 0 CHECK (last_position_ms >= 0),
    reported_duration_ms bigint CHECK (reported_duration_ms IS NULL OR reported_duration_ms > 0),
    seek_count bigint NOT NULL DEFAULT 0 CHECK (seek_count >= 0),
    terminal_reason text CHECK (terminal_reason IS NULL OR terminal_reason IN ('ended','interrupted','stopped','decoder_error','disconnected')),
    CONSTRAINT playback_sessions_completion_check CHECK (completed_at IS NULL OR (ended_at IS NOT NULL AND terminal_reason = 'ended')),
    CONSTRAINT playback_sessions_queue_pair CHECK ((queue_item_id IS NULL) = (selection_token IS NULL))
);
CREATE INDEX playback_sessions_recent_idx ON playback_sessions(started_at DESC,id DESC);
CREATE INDEX playback_sessions_client_open_idx ON playback_sessions(client_instance_id,started_at DESC) WHERE ended_at IS NULL;

CREATE TABLE mutation_receipts (
    operation_scope text NOT NULL CHECK (length(operation_scope) BETWEEN 1 AND 80),
    idempotency_key uuid NOT NULL,
    canonical_request bytea NOT NULL CHECK (octet_length(canonical_request) <= 262144),
    response_status integer NOT NULL CHECK (response_status BETWEEN 200 AND 299),
    response_body bytea NOT NULL CHECK (octet_length(response_body) <= 16384),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '7 days'),
    last_replayed_at timestamptz,
    CONSTRAINT mutation_receipts_expiry_check CHECK (expires_at > created_at),
    PRIMARY KEY(operation_scope,idempotency_key)
);
CREATE INDEX mutation_receipts_expiry_idx ON mutation_receipts(expires_at);
