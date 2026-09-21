package storage

import (
	"context"
	"errors"
)

// These records are authoritative catalog identities, not scanner projections.
// Paths remain internal and are never serialized by the HTTP API.
type Track struct {
	ID    string
	Title *string
}

type MediaObject struct {
	ID         string
	TrackID    string
	SHA256     [32]byte
	Format     string
	ByteLength int64
}

type MediaLocation struct {
	ID            string
	MediaObjectID string
	LocalPath     string
}

func (s *Store) InsertTrack(ctx context.Context, track Track) error {
	if track.ID == "" || len(track.ID) > 128 {
		return errors.New("invalid track ID")
	}
	_, err := s.pool.Exec(ctx, "INSERT INTO tracks(id, title) VALUES($1, $2)", track.ID, track.Title)
	return err
}

func (s *Store) InsertMediaObject(ctx context.Context, object MediaObject) error {
	if object.ID == "" || object.TrackID == "" || object.Format == "" || object.ByteLength < 0 {
		return errors.New("invalid media object")
	}
	_, err := s.pool.Exec(ctx, "INSERT INTO media_objects(id, track_id, sha256, format, byte_length) VALUES($1, $2, $3, $4, $5)", object.ID, object.TrackID, object.SHA256[:], object.Format, object.ByteLength)
	return err
}

func (s *Store) InsertLocation(ctx context.Context, location MediaLocation) error {
	if location.ID == "" || location.MediaObjectID == "" || location.LocalPath == "" {
		return errors.New("invalid media location")
	}
	_, err := s.pool.Exec(ctx, "INSERT INTO media_locations(id, media_object_id, local_path) VALUES($1, $2, $3)", location.ID, location.MediaObjectID, location.LocalPath)
	return err
}
