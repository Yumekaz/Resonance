package storage

import (
	"context"
	"strings"
	"unicode/utf8"
)

type SearchResults struct {
	Tracks  []CatalogTrack  `json:"tracks"`
	Artists []CatalogArtist `json:"artists"`
	Albums  []CatalogAlbum  `json:"albums"`
}

// SearchCatalog uses literal matching: SQL wildcard characters have no special
// meaning. Group identity and public Track admission remain the M1 contracts.
func (s *Store) SearchCatalog(ctx context.Context, query string, limit int) (SearchResults, error) {
	out := SearchResults{Tracks: []CatalogTrack{}, Artists: []CatalogArtist{}, Albums: []CatalogAlbum{}}
	query = strings.TrimSpace(query)
	if !utf8.ValidString(query) || strings.ContainsRune(query, 0) || len(query) > 512 || utf8.RuneCountInString(query) > 120 || limit < 1 || limit > 50 {
		return out, ErrUserInvalid
	}
	if query == "" {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, trackReadSQL+enabledTrackSQL+` AND (strpos(lower(coalesce(t.title,'')),lower($1))>0 OR strpos(lower(coalesce(t.artist_credit,'')),lower($1))>0 OR strpos(lower(coalesce(t.album_title,'')),lower($1))>0) ORDER BY coalesce(t.catalog_title_key,''),t.id LIMIT $2`, query, limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		t, e := scanTrack(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Tracks = append(out.Tracks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT ca.id,ca.display_credit,ca.normalized_credit,EXISTS(SELECT 1 FROM track_artist_memberships am JOIN media_objects mo ON mo.track_id=am.track_id JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE am.artist_id=ca.id AND ml.availability='available' AND lr.enabled)`+catalogArtistPresentationSQL+` FROM catalog_artists ca WHERE true`+enabledArtistSQL+` AND strpos(lower(ca.display_credit),lower($1))>0 AND EXISTS(SELECT 1 FROM track_artist_memberships am WHERE am.artist_id=ca.id) ORDER BY ca.normalized_credit,ca.id LIMIT $2`, query, limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a CatalogArtist
		if err = rows.Scan(&a.ID, &a.DisplayCredit, &a.SortKey, &a.Available, &a.ArtworkURL, &a.TrackCount); err != nil {
			rows.Close()
			return out, err
		}
		if !validDisplay(&a.DisplayCredit) {
			rows.Close()
			return out, ErrCatalogInvalid
		}
		out.Artists = append(out.Artists, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT ca.id,ca.display_title,ca.album_artist_id,ca.release_year,ca.normalized_title,EXISTS(SELECT 1 FROM track_album_memberships m JOIN media_objects mo ON mo.track_id=m.track_id JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE m.album_id=ca.id AND ml.availability='available' AND lr.enabled)`+catalogAlbumPresentationSQL+` FROM catalog_albums ca WHERE true`+enabledAlbumSQL+` AND strpos(lower(ca.display_title),lower($1))>0 AND EXISTS(SELECT 1 FROM track_album_memberships m WHERE m.album_id=ca.id) ORDER BY ca.normalized_title,ca.id LIMIT $2`, query, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a CatalogAlbum
		if err = rows.Scan(&a.ID, &a.DisplayTitle, &a.AlbumArtistID, &a.ReleaseYear, &a.SortKey, &a.Available, &a.ArtworkURL, &a.ArtistCredit, &a.TrackCount); err != nil {
			return out, err
		}
		if !validDisplay(&a.DisplayTitle) || !validDisplay(a.ArtistCredit) {
			return out, ErrCatalogInvalid
		}
		out.Albums = append(out.Albums, a)
	}
	return out, rows.Err()
}
