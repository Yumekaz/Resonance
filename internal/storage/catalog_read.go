package storage

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var ErrCatalogNotFound = errors.New("catalog item not found")
var ErrCatalogInvalid = errors.New("catalog record is invalid")

func validDisplay(value *string) bool {
	return value == nil || len(*value) <= 65536 && utf8.ValidString(*value)
}

type CatalogTrack struct {
	ID                string  `json:"id"`
	Title             *string `json:"title"`
	ArtistCredit      *string `json:"artist_credit"`
	AlbumTitle        *string `json:"album_title"`
	AlbumArtistCredit *string `json:"album_artist_credit"`
	TrackNumber       *int    `json:"track_number"`
	DiscNumber        *int    `json:"disc_number"`
	ReleaseYear       *int    `json:"release_year"`
	Genre             *string `json:"genre"`
	ArtistID          *string `json:"artist_id"`
	AlbumID           *string `json:"album_id"`
	Format            *string `json:"format"`
	Available         bool    `json:"available"`
	StreamURL         string  `json:"stream_url"`
	ArtworkURL        *string `json:"artwork_url"`
	SortKey           string  `json:"-"`
}

type CatalogArtist struct {
	ID            string `json:"id"`
	DisplayCredit string `json:"display_credit"`
	Available     bool   `json:"available"`
	SortKey       string `json:"-"`
}

type CatalogAlbum struct {
	ID            string  `json:"id"`
	DisplayTitle  string  `json:"display_title"`
	AlbumArtistID *string `json:"album_artist_id"`
	ReleaseYear   *int    `json:"release_year"`
	Available     bool    `json:"available"`
	SortKey       string  `json:"-"`
}

const trackReadSQL = `SELECT t.id,t.title,t.artist_credit,t.album_title,t.album_artist_credit,t.track_number,t.disc_number,t.release_year,t.genre,
	(SELECT tam.artist_id FROM track_artist_memberships tam WHERE tam.track_id=t.id AND tam.role='track_credit'),
	(SELECT m.album_id FROM track_album_memberships m WHERE m.track_id=t.id),
	(SELECT mo.format FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id WHERE mo.track_id=t.id AND ml.root_id IS NOT NULL ORDER BY mo.created_at DESC,mo.id LIMIT 1),
	EXISTS(SELECT 1 FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE mo.track_id=t.id AND ml.availability='available' AND lr.enabled),
	EXISTS(SELECT 1 FROM media_objects mo WHERE mo.track_id=t.id AND mo.artwork_sha256 IS NOT NULL AND mo.artwork_mime IN ('image/png','image/jpeg')),
	coalesce(t.catalog_title_key,'')
	FROM tracks t WHERE EXISTS(SELECT 1 FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id WHERE mo.track_id=t.id AND ml.root_id IS NOT NULL)`

func scanTrack(rows pgx.Row) (CatalogTrack, error) {
	var t CatalogTrack
	var art bool
	err := rows.Scan(&t.ID, &t.Title, &t.ArtistCredit, &t.AlbumTitle, &t.AlbumArtistCredit, &t.TrackNumber, &t.DiscNumber, &t.ReleaseYear, &t.Genre, &t.ArtistID, &t.AlbumID, &t.Format, &t.Available, &art, &t.SortKey)
	if err != nil {
		return CatalogTrack{}, err
	}
	if !validDisplay(t.Title) || !validDisplay(t.ArtistCredit) || !validDisplay(t.AlbumTitle) || !validDisplay(t.AlbumArtistCredit) || !validDisplay(t.Genre) || len(t.SortKey) > 65536 || !utf8.ValidString(t.SortKey) {
		return CatalogTrack{}, ErrCatalogInvalid
	}
	t.StreamURL = "/api/v1/tracks/" + t.ID + "/stream"
	if art && t.Available {
		url := "/api/v1/tracks/" + t.ID + "/artwork"
		t.ArtworkURL = &url
	}
	return t, nil
}

func (s *Store) GetCatalogTrack(ctx context.Context, id string) (CatalogTrack, error) {
	t, err := scanTrack(s.pool.QueryRow(ctx, trackReadSQL+" AND t.id=$1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return CatalogTrack{}, ErrCatalogNotFound
	}
	return t, err
}

func (s *Store) ListCatalogTracks(ctx context.Context, limit int, afterKey, afterID, artistID, albumID string, available *bool) ([]CatalogTrack, error) {
	rows, err := s.pool.Query(ctx, trackReadSQL+` AND (coalesce(t.catalog_title_key,''),t.id)>($1,$2) AND ($3='' OR EXISTS(SELECT 1 FROM track_artist_memberships am WHERE am.track_id=t.id AND am.artist_id=$3)) AND ($4='' OR EXISTS(SELECT 1 FROM track_album_memberships m WHERE m.track_id=t.id AND m.album_id=$4)) AND ($5::boolean IS NULL OR EXISTS(SELECT 1 FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE mo.track_id=t.id AND ml.availability='available' AND lr.enabled)=$5) ORDER BY coalesce(t.catalog_title_key,''),t.id LIMIT $6`, afterKey, afterID, artistID, albumID, available, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CatalogTrack{}
	for rows.Next() {
		t, e := scanTrack(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

const albumSortExpression = `lpad(coalesce(t.disc_number,2147483647)::text,10,'0') || ':' || lpad(coalesce(t.track_number,2147483647)::text,10,'0') || ':' || coalesce(t.catalog_title_key,'')`

func (s *Store) ListAlbumTracks(ctx context.Context, limit int, afterKey, afterID, albumID string) ([]CatalogTrack, error) {
	selectSQL := strings.Replace(trackReadSQL, "coalesce(t.catalog_title_key,'')\n\tFROM tracks t", albumSortExpression+"\n\tFROM tracks t", 1)
	if selectSQL == trackReadSQL {
		return nil, errors.New("album sort query unavailable")
	}
	query := selectSQL + ` AND EXISTS(SELECT 1 FROM track_album_memberships m WHERE m.track_id=t.id AND m.album_id=$1) AND (` + albumSortExpression + `,t.id)>($2,$3) ORDER BY ` + albumSortExpression + `,t.id LIMIT $4`
	rows, err := s.pool.Query(ctx, query, albumID, afterKey, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogTrack{}
	for rows.Next() {
		t, e := scanTrack(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetCatalogArtist(ctx context.Context, id string) (CatalogArtist, error) {
	var a CatalogArtist
	err := s.pool.QueryRow(ctx, `SELECT ca.id,ca.display_credit,ca.normalized_credit,EXISTS(SELECT 1 FROM track_artist_memberships am JOIN media_objects mo ON mo.track_id=am.track_id JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE am.artist_id=ca.id AND ml.availability='available' AND lr.enabled) FROM catalog_artists ca WHERE ca.id=$1 AND EXISTS(SELECT 1 FROM track_artist_memberships am WHERE am.artist_id=ca.id)`, id).Scan(&a.ID, &a.DisplayCredit, &a.SortKey, &a.Available)
	if errors.Is(err, pgx.ErrNoRows) {
		return CatalogArtist{}, ErrCatalogNotFound
	}
	if err == nil && (len(a.DisplayCredit) > 65536 || !utf8.ValidString(a.DisplayCredit)) {
		return CatalogArtist{}, ErrCatalogInvalid
	}
	return a, err
}

func (s *Store) ListCatalogArtists(ctx context.Context, limit int, afterKey, afterID string) ([]CatalogArtist, error) {
	rows, err := s.pool.Query(ctx, `SELECT ca.id,ca.display_credit,ca.normalized_credit,EXISTS(SELECT 1 FROM track_artist_memberships am JOIN media_objects mo ON mo.track_id=am.track_id JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE am.artist_id=ca.id AND ml.availability='available' AND lr.enabled) FROM catalog_artists ca WHERE (ca.normalized_credit,ca.id)>($1,$2) AND EXISTS(SELECT 1 FROM track_artist_memberships am WHERE am.artist_id=ca.id) ORDER BY ca.normalized_credit,ca.id LIMIT $3`, afterKey, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogArtist{}
	for rows.Next() {
		var a CatalogArtist
		if err := rows.Scan(&a.ID, &a.DisplayCredit, &a.SortKey, &a.Available); err != nil {
			return nil, err
		}
		if len(a.DisplayCredit) > 65536 || !utf8.ValidString(a.DisplayCredit) {
			return nil, ErrCatalogInvalid
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetCatalogAlbum(ctx context.Context, id string) (CatalogAlbum, error) {
	var a CatalogAlbum
	err := s.pool.QueryRow(ctx, `SELECT ca.id,ca.display_title,ca.album_artist_id,ca.release_year,ca.normalized_title,EXISTS(SELECT 1 FROM track_album_memberships m JOIN media_objects mo ON mo.track_id=m.track_id JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE m.album_id=ca.id AND ml.availability='available' AND lr.enabled) FROM catalog_albums ca WHERE ca.id=$1 AND EXISTS(SELECT 1 FROM track_album_memberships m WHERE m.album_id=ca.id)`, id).Scan(&a.ID, &a.DisplayTitle, &a.AlbumArtistID, &a.ReleaseYear, &a.SortKey, &a.Available)
	if errors.Is(err, pgx.ErrNoRows) {
		return CatalogAlbum{}, ErrCatalogNotFound
	}
	if err == nil && (len(a.DisplayTitle) > 65536 || !utf8.ValidString(a.DisplayTitle)) {
		return CatalogAlbum{}, ErrCatalogInvalid
	}
	return a, err
}

func (s *Store) ListCatalogAlbums(ctx context.Context, limit int, afterKey, afterID, artistID string) ([]CatalogAlbum, error) {
	rows, err := s.pool.Query(ctx, `SELECT ca.id,ca.display_title,ca.album_artist_id,ca.release_year,ca.normalized_title,EXISTS(SELECT 1 FROM track_album_memberships m JOIN media_objects mo ON mo.track_id=m.track_id JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE m.album_id=ca.id AND ml.availability='available' AND lr.enabled) FROM catalog_albums ca WHERE (ca.normalized_title,ca.id)>($1,$2) AND EXISTS(SELECT 1 FROM track_album_memberships m WHERE m.album_id=ca.id) AND ($3='' OR ca.album_artist_id=$3 OR EXISTS(SELECT 1 FROM track_album_memberships m JOIN track_artist_memberships am ON am.track_id=m.track_id WHERE m.album_id=ca.id AND am.artist_id=$3)) ORDER BY ca.normalized_title,ca.id LIMIT $4`, afterKey, afterID, artistID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogAlbum{}
	for rows.Next() {
		var a CatalogAlbum
		if err := rows.Scan(&a.ID, &a.DisplayTitle, &a.AlbumArtistID, &a.ReleaseYear, &a.SortKey, &a.Available); err != nil {
			return nil, err
		}
		if len(a.DisplayTitle) > 65536 || !utf8.ValidString(a.DisplayTitle) {
			return nil, ErrCatalogInvalid
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func ValidCatalogID(id, kind string) bool {
	prefix := map[string]string{"track": "trk_", "artist": "art_", "album": "alb_"}[kind]
	if prefix == "" || !strings.HasPrefix(id, prefix) || len(id) != len(prefix)+32 {
		return false
	}
	for _, c := range id[len(prefix):] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
