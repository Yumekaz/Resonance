-- Local catalog groupings; source identities and raw Track observations remain authoritative.
ALTER TABLE tracks ADD COLUMN catalog_title_key text;
CREATE TABLE catalog_grouping_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    rule_version integer NOT NULL CHECK (rule_version > 0),
    completed boolean NOT NULL DEFAULT false,
    completed_at timestamptz
);
INSERT INTO catalog_grouping_state(singleton,rule_version,completed) VALUES(true,1,false);

CREATE TABLE catalog_artists (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    display_credit text NOT NULL CHECK (length(display_credit) BETWEEN 1 AND 2048),
    normalized_credit text NOT NULL CHECK (length(normalized_credit) BETWEEN 1 AND 2048),
    evidence_scope text NOT NULL CHECK (length(evidence_scope) BETWEEN 1 AND 1024),
    evidence_code text NOT NULL CHECK (evidence_code IN ('artist_folder','album_scope','track_scope')),
    source_location_id text REFERENCES media_locations(id),
    rule_version integer NOT NULL CHECK (rule_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT catalog_artists_group_key UNIQUE(normalized_credit,evidence_scope,rule_version)
);
CREATE INDEX catalog_artists_browse_idx ON catalog_artists(normalized_credit,id);

CREATE TABLE catalog_albums (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    display_title text NOT NULL CHECK (length(display_title) BETWEEN 1 AND 2048),
    normalized_title text NOT NULL CHECK (length(normalized_title) BETWEEN 1 AND 2048),
    album_artist_id text REFERENCES catalog_artists(id),
    credit_key text NOT NULL CHECK (length(credit_key) BETWEEN 1 AND 2048),
    root_id uuid NOT NULL REFERENCES library_roots(id),
    evidence_scope text NOT NULL CHECK (length(evidence_scope) BETWEEN 1 AND 1024),
    release_year integer CHECK (release_year BETWEEN 1 AND 9999),
    source_location_id text REFERENCES media_locations(id),
    evidence_code text NOT NULL CHECK (evidence_code IN ('folder','disc_parent')),
    rule_version integer NOT NULL CHECK (rule_version > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX catalog_albums_identity_idx ON catalog_albums(root_id,evidence_scope,normalized_title,credit_key,COALESCE(release_year,0),rule_version);
CREATE INDEX catalog_albums_browse_idx ON catalog_albums(normalized_title,id);

CREATE TABLE track_album_memberships (
    track_id text PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    album_id text NOT NULL REFERENCES catalog_albums(id),
    source_location_id text NOT NULL REFERENCES media_locations(id),
    raw_album_title text NOT NULL,
    raw_album_artist_credit text,
    raw_track_artist_credit text,
    raw_release_year integer,
    evidence_code text NOT NULL CHECK (evidence_code IN ('folder','disc_parent')),
    rule_version integer NOT NULL CHECK (rule_version > 0)
);
CREATE INDEX track_album_memberships_album_idx ON track_album_memberships(album_id,track_id);

CREATE TABLE track_artist_memberships (
    track_id text NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    artist_id text NOT NULL REFERENCES catalog_artists(id),
    role text NOT NULL CHECK (role IN ('track_credit','album_artist_credit')),
    source_location_id text NOT NULL REFERENCES media_locations(id),
    raw_credit text NOT NULL,
    evidence_code text NOT NULL CHECK (evidence_code IN ('artist_folder','album_scope','track_scope')),
    rule_version integer NOT NULL CHECK (rule_version > 0),
    PRIMARY KEY(track_id,role)
);
CREATE INDEX track_artist_memberships_artist_idx ON track_artist_memberships(artist_id,track_id);
CREATE INDEX tracks_browse_idx ON tracks(catalog_title_key,id);
