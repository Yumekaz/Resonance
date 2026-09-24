package storage

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type Playlist struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type PlaylistItem struct {
	ID           string  `json:"id"`
	TrackID      string  `json:"track_id"`
	Position     int64   `json:"position"`
	Title        *string `json:"title"`
	ArtistCredit *string `json:"artist_credit"`
	Available    bool    `json:"available"`
}
type PlaylistDetail struct {
	Playlist
	Items []PlaylistItem `json:"items"`
}
type PlaylistChange struct {
	ID       string  `json:"id"`
	Revision int64   `json:"revision"`
	ItemID   *string `json:"item_id,omitempty"`
}

func validPlaylistName(name string) bool {
	return utf8.ValidString(name) && len(name) <= 1024 && len([]rune(strings.TrimSpace(name))) >= 1 && len([]rune(strings.TrimSpace(name))) <= 256
}
func (s *Store) ListPlaylists(ctx context.Context, limit int, beforeTime *time.Time, beforeID string) ([]Playlist, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,revision,created_at,updated_at FROM playlists WHERE ($1::timestamptz IS NULL OR (created_at,id)<($1,$2)) ORDER BY created_at DESC,id DESC LIMIT $3`, beforeTime, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Playlist{}
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.Name, &p.Revision, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func readPlaylist(ctx context.Context, q queueQueryer, id string) (PlaylistDetail, error) {
	var out PlaylistDetail
	err := q.QueryRow(ctx, "SELECT id,name,revision,created_at,updated_at FROM playlists WHERE id=$1", id).Scan(&out.ID, &out.Name, &out.Revision, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrUserNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := q.Query(ctx, `SELECT pi.id,pi.track_id,pi.position,t.title,t.artist_credit,EXISTS(SELECT 1 FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE mo.track_id=pi.track_id AND ml.availability='available' AND lr.enabled) FROM playlist_items pi JOIN tracks t ON t.id=pi.track_id WHERE pi.playlist_id=$1 ORDER BY pi.position,pi.id`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Items = []PlaylistItem{}
	for rows.Next() {
		var item PlaylistItem
		if err := rows.Scan(&item.ID, &item.TrackID, &item.Position, &item.Title, &item.ArtistCredit, &item.Available); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}
func (s *Store) ReadPlaylist(ctx context.Context, id string) (PlaylistDetail, error) {
	if !validLogicalID(id, "pl_") {
		return PlaylistDetail{}, ErrUserInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PlaylistDetail{}, err
	}
	defer tx.Rollback(ctx)
	out, err := readPlaylist(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlaylistDetail{}, err
	}
	return out, nil
}

type PlaylistCreateRequest struct {
	Name            string `json:"name"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Store) CreatePlaylist(ctx context.Context, key string, req PlaylistCreateRequest) (MutationResult, error) {
	if !validPlaylistName(req.Name) || req.ExpectedVersion != 0 {
		return MutationResult{}, ErrUserInvalid
	}
	req.Name = strings.TrimSpace(req.Name)
	return s.mutateWithReceipt(ctx, "playlist.create", key, req, func(tx pgx.Tx) (int, any, error) {
		id, err := newLogicalID("pl_")
		if err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO playlists(id,name) VALUES($1,$2)", id, req.Name); err != nil {
			return 0, nil, err
		}
		return 201, PlaylistChange{ID: id, Revision: 0}, nil
	})
}
func lockPlaylist(ctx context.Context, tx pgx.Tx, id string) (Playlist, error) {
	var p Playlist
	err := tx.QueryRow(ctx, "SELECT id,name,revision,created_at,updated_at FROM playlists WHERE id=$1 FOR UPDATE", id).Scan(&p.ID, &p.Name, &p.Revision, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrUserNotFound
	}
	return p, err
}

type PlaylistRenameRequest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Store) RenamePlaylist(ctx context.Context, key string, req PlaylistRenameRequest) (MutationResult, error) {
	if !validLogicalID(req.ID, "pl_") || !validPlaylistName(req.Name) || req.ExpectedVersion < 0 {
		return MutationResult{}, ErrUserInvalid
	}
	req.Name = strings.TrimSpace(req.Name)
	return s.mutateWithReceipt(ctx, "playlist.rename", key, req, func(tx pgx.Tx) (int, any, error) {
		p, err := lockPlaylist(ctx, tx, req.ID)
		if err != nil {
			return 0, nil, err
		}
		if p.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		p.Revision++
		if _, err = tx.Exec(ctx, "UPDATE playlists SET name=$2,revision=$3,updated_at=now() WHERE id=$1", p.ID, req.Name, p.Revision); err != nil {
			return 0, nil, err
		}
		return 200, PlaylistChange{ID: p.ID, Revision: p.Revision}, nil
	})
}

func (s *Store) DeletePlaylist(ctx context.Context, id string, expected int64) error {
	if !validLogicalID(id, "pl_") || expected < 0 {
		return ErrUserInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	p, err := lockPlaylist(ctx, tx, id)
	if errors.Is(err, ErrUserNotFound) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if p.Revision != expected {
		return ErrStaleVersion
	}
	if _, err = tx.Exec(ctx, "DELETE FROM playlists WHERE id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type PlaylistAddRequest struct {
	PlaylistID      string `json:"playlist_id"`
	TrackID         string `json:"track_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Store) AddPlaylistItem(ctx context.Context, key string, req PlaylistAddRequest) (MutationResult, error) {
	if !validLogicalID(req.PlaylistID, "pl_") || !ValidCatalogID(req.TrackID, "track") || req.ExpectedVersion < 0 {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "playlist.add", key, req, func(tx pgx.Tx) (int, any, error) {
		p, err := lockPlaylist(ctx, tx, req.PlaylistID)
		if err != nil {
			return 0, nil, err
		}
		if p.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		exists, err := publicTrackExists(ctx, tx, req.TrackID)
		if err != nil {
			return 0, nil, err
		}
		if !exists {
			return 0, nil, ErrUserNotFound
		}
		var count int64
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM playlist_items WHERE playlist_id=$1", p.ID).Scan(&count); err != nil {
			return 0, nil, err
		}
		if count >= 5000 {
			return 0, nil, ErrUserLimit
		}
		id, err := newLogicalID("pi_")
		if err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO playlist_items(id,playlist_id,track_id,position) VALUES($1,$2,$3,$4)", id, p.ID, req.TrackID, count); err != nil {
			return 0, nil, err
		}
		p.Revision++
		if _, err = tx.Exec(ctx, "UPDATE playlists SET revision=$2,updated_at=now() WHERE id=$1", p.ID, p.Revision); err != nil {
			return 0, nil, err
		}
		return 201, PlaylistChange{ID: p.ID, Revision: p.Revision, ItemID: &id}, nil
	})
}

type PlaylistRemoveRequest struct {
	PlaylistID      string `json:"playlist_id"`
	ItemID          string `json:"item_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Store) RemovePlaylistItem(ctx context.Context, key string, req PlaylistRemoveRequest) (MutationResult, error) {
	if !validLogicalID(req.PlaylistID, "pl_") || !validLogicalID(req.ItemID, "pi_") || req.ExpectedVersion < 0 {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "playlist.remove", key, req, func(tx pgx.Tx) (int, any, error) {
		p, err := lockPlaylist(ctx, tx, req.PlaylistID)
		if err != nil {
			return 0, nil, err
		}
		if p.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		full, err := readPlaylist(ctx, tx, p.ID)
		if err != nil {
			return 0, nil, err
		}
		at := -1
		for i, item := range full.Items {
			if item.ID == req.ItemID {
				at = i
				break
			}
		}
		if at < 0 {
			return 0, nil, ErrUserNotFound
		}
		if _, err = tx.Exec(ctx, "DELETE FROM playlist_items WHERE id=$1", req.ItemID); err != nil {
			return 0, nil, err
		}
		full.Items = slices.Delete(full.Items, at, at+1)
		for i, item := range full.Items {
			if _, err = tx.Exec(ctx, "UPDATE playlist_items SET position=$2 WHERE id=$1", item.ID, i); err != nil {
				return 0, nil, err
			}
		}
		p.Revision++
		if _, err = tx.Exec(ctx, "UPDATE playlists SET revision=$2,updated_at=now() WHERE id=$1", p.ID, p.Revision); err != nil {
			return 0, nil, err
		}
		return 200, PlaylistChange{ID: p.ID, Revision: p.Revision, ItemID: &req.ItemID}, nil
	})
}

type PlaylistOrderRequest struct {
	PlaylistID      string   `json:"playlist_id"`
	ItemIDs         []string `json:"item_ids"`
	ExpectedVersion int64    `json:"expected_version"`
}

func (s *Store) ReorderPlaylist(ctx context.Context, key string, req PlaylistOrderRequest) (MutationResult, error) {
	if !validLogicalID(req.PlaylistID, "pl_") || req.ExpectedVersion < 0 || len(req.ItemIDs) > 5000 {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "playlist.order", key, req, func(tx pgx.Tx) (int, any, error) {
		p, err := lockPlaylist(ctx, tx, req.PlaylistID)
		if err != nil {
			return 0, nil, err
		}
		if p.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		full, err := readPlaylist(ctx, tx, p.ID)
		if err != nil {
			return 0, nil, err
		}
		if len(full.Items) != len(req.ItemIDs) {
			return 0, nil, ErrUserInvalid
		}
		found := map[string]bool{}
		for _, item := range full.Items {
			found[item.ID] = true
		}
		for i, id := range req.ItemIDs {
			if !found[id] {
				return 0, nil, ErrUserInvalid
			}
			delete(found, id)
			if _, err = tx.Exec(ctx, "UPDATE playlist_items SET position=$2 WHERE id=$1", id, i); err != nil {
				return 0, nil, err
			}
		}
		if len(found) != 0 {
			return 0, nil, ErrUserInvalid
		}
		p.Revision++
		if _, err = tx.Exec(ctx, "UPDATE playlists SET revision=$2,updated_at=now() WHERE id=$1", p.ID, p.Revision); err != nil {
			return 0, nil, err
		}
		return 200, PlaylistChange{ID: p.ID, Revision: p.Revision}, nil
	})
}
