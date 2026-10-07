-- M2 listening-source projection. Existing catalog/user-library data is untouched.
CREATE TABLE queue_playback_context (
 singleton boolean PRIMARY KEY DEFAULT true REFERENCES active_queue(singleton) ON DELETE CASCADE CHECK(singleton),
 id uuid NOT NULL,
 source_kind text NOT NULL CHECK(source_kind IN ('library','artist','album','playlist','favorites','selection')),
 source_id text,
 source_name text NOT NULL CHECK(octet_length(source_name) <= 512),
 source_order text NOT NULL CHECK(source_order IN ('original','title','title_desc','artist','album','recent')),
 source_query text NOT NULL DEFAULT '' CHECK(octet_length(source_query) <= 512),
 shuffled boolean NOT NULL DEFAULT false,
 next_rank bigint NOT NULL DEFAULT 0 CHECK(next_rank >= 0),
 exhausted boolean NOT NULL DEFAULT false,
 total bigint NOT NULL CHECK(total >= 0),
 round bigint NOT NULL DEFAULT 0 CHECK(round >= 0),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE queue_context_tracks (
 singleton boolean NOT NULL DEFAULT true REFERENCES queue_playback_context(singleton) ON DELETE CASCADE CHECK(singleton),
 ordinal bigint PRIMARY KEY CHECK(ordinal >= 0),
 track_id text NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
 source_item_id text,
 play_rank bigint NOT NULL CHECK(play_rank >= 0),
 seen boolean NOT NULL DEFAULT false,
 excluded boolean NOT NULL DEFAULT false,
 failed boolean NOT NULL DEFAULT false,
 CONSTRAINT queue_context_tracks_rank_key UNIQUE(play_rank) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX queue_context_tracks_track_idx ON queue_context_tracks(track_id);
CREATE INDEX queue_context_tracks_pending_idx ON queue_context_tracks(play_rank) WHERE NOT seen AND NOT excluded AND NOT failed;
CREATE TABLE queue_context_items (
 queue_item_id text PRIMARY KEY REFERENCES queue_items(id) ON DELETE CASCADE,
 ordinal bigint NOT NULL REFERENCES queue_context_tracks(ordinal) ON DELETE CASCADE,
 round bigint NOT NULL CHECK(round >= 0)
);
CREATE INDEX queue_context_items_ordinal_idx ON queue_context_items(ordinal);
