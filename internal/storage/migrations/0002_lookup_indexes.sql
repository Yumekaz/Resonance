-- Forward-only additive migration, safe for populated core tables.
CREATE INDEX media_objects_track_id_idx ON media_objects(track_id);
CREATE INDEX media_locations_media_object_id_idx ON media_locations(media_object_id);
