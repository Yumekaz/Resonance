package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type PlaybackCandidate struct {
	TrackID         string
	MediaObjectID   string
	LocationID      string
	RootPath        string
	RelativePath    string
	Format          string
	ObservedSize    *int64
	ObservedMTimeNS *int64
	ArtworkSHA256   []byte
	ArtworkMIME     *string
	NativeKind      *string
	NativeScope     *string
	NativeID        []byte
	NativeBirth     []byte
}

func (s *Store) PlaybackCandidates(ctx context.Context, trackID string) ([]PlaybackCandidate, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tracks t JOIN media_objects mo ON mo.track_id=t.id JOIN media_locations ml ON ml.media_object_id=mo.id WHERE t.id=$1 AND ml.root_id IS NOT NULL)`, trackID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrCatalogNotFound
	}
	rows, err := s.pool.Query(ctx, `SELECT mo.track_id,mo.id,ml.id,lr.canonical_path,ml.relative_path,mo.format,ml.observed_size,ml.observed_mtime_ns,mo.artwork_sha256,mo.artwork_mime,ml.native_id_kind,ml.native_id_scope,ml.native_id,ml.native_birth_token
		FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id JOIN tracks t ON t.id=mo.track_id
		WHERE mo.track_id=$1 AND ml.availability='available' AND lr.enabled
		ORDER BY (ml.id=t.metadata_source_location_id) DESC,mo.created_at DESC,mo.id,ml.id`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlaybackCandidate{}
	for rows.Next() {
		var c PlaybackCandidate
		if err := rows.Scan(&c.TrackID, &c.MediaObjectID, &c.LocationID, &c.RootPath, &c.RelativePath, &c.Format, &c.ObservedSize, &c.ObservedMTimeNS, &c.ArtworkSHA256, &c.ArtworkMIME, &c.NativeKind, &c.NativeScope, &c.NativeID, &c.NativeBirth); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CandidateStillAvailable(ctx context.Context, candidate PlaybackCandidate) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_locations ml JOIN library_roots lr ON lr.id=ml.root_id WHERE ml.id=$1 AND ml.media_object_id=$2 AND ml.relative_path=$3 AND ml.observed_size IS NOT DISTINCT FROM $4::bigint AND ml.observed_mtime_ns IS NOT DISTINCT FROM $5::bigint AND ml.native_id_kind IS NOT DISTINCT FROM $6::text AND ml.native_id_scope IS NOT DISTINCT FROM $7::text AND ml.native_id IS NOT DISTINCT FROM $8::bytea AND ml.native_birth_token IS NOT DISTINCT FROM $9::bytea AND ml.availability='available' AND lr.enabled)`, candidate.LocationID, candidate.MediaObjectID, candidate.RelativePath, candidate.ObservedSize, candidate.ObservedMTimeNS, candidate.NativeKind, candidate.NativeScope, candidate.NativeID, candidate.NativeBirth).Scan(&ok)
	return ok, err
}

func IsCatalogNotFound(err error) bool {
	return errors.Is(err, ErrCatalogNotFound) || errors.Is(err, pgx.ErrNoRows)
}
