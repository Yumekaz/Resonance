-- M1.2 stores parsed credits, but M1.4 owns Artist/Album grouping identity.
-- Preserve already-imported album titles before removing the premature
-- one-Artist/one-Album-per-Track identity rows introduced by migration 0003.
ALTER TABLE tracks ADD COLUMN album_title text;

UPDATE tracks AS t
SET album_title = a.title
FROM albums AS a
WHERE t.album_id = a.id;

ALTER TABLE tracks
    DROP COLUMN artist_id,
    DROP COLUMN album_id;

DROP TABLE albums;
DROP TABLE artists;
