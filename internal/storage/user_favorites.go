package storage

import (
	"context"
	"time"
)

type FavoriteTrack struct {
	TrackID      string    `json:"track_id"`
	CreatedAt    time.Time `json:"created_at"`
	Title        *string   `json:"title"`
	ArtistCredit *string   `json:"artist_credit"`
	Available    bool      `json:"available"`
}

func (s *Store) SetFavorite(ctx context.Context, trackID string, present bool) error {
	if !ValidCatalogID(trackID, "track") {
		return ErrUserInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	exists, err := publicTrackExists(ctx, tx, trackID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}
	if present {
		_, err = tx.Exec(ctx, "INSERT INTO favorite_tracks(track_id) VALUES($1) ON CONFLICT(track_id) DO NOTHING", trackID)
	} else {
		_, err = tx.Exec(ctx, "DELETE FROM favorite_tracks WHERE track_id=$1", trackID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ListFavorites(ctx context.Context, limit int, beforeTime *time.Time, beforeID string) ([]FavoriteTrack, error) {
	var id *string
	if beforeTime != nil {
		id = &beforeID
	}
	rows, err := s.pool.Query(ctx, `SELECT f.track_id,f.created_at,t.title,t.artist_credit,EXISTS(SELECT 1 FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE mo.track_id=f.track_id AND ml.availability='available' AND lr.enabled) FROM favorite_tracks f JOIN tracks t ON t.id=f.track_id WHERE ($1::timestamptz IS NULL OR (f.created_at,f.track_id)<($1,$2::text)) ORDER BY f.created_at DESC,f.track_id DESC LIMIT $3`, beforeTime, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FavoriteTrack{}
	for rows.Next() {
		var x FavoriteTrack
		if err := rows.Scan(&x.TrackID, &x.CreatedAt, &x.Title, &x.ArtistCredit, &x.Available); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
