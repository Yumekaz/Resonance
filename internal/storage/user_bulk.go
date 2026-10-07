package storage

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
)

type CollectionRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}
type BulkCollectionRequest struct {
	Source          CollectionRef  `json:"source"`
	ItemIDs         []string       `json:"item_ids"`
	Action          string         `json:"action"`
	ExpectedVersion int64          `json:"expected_version"`
	Target          *CollectionRef `json:"target,omitempty"`
	TargetVersion   *int64         `json:"target_version,omitempty"`
	Placement       string         `json:"placement,omitempty"`
}
type BulkCollectionChange struct {
	Revision       int64  `json:"revision"`
	TargetRevision *int64 `json:"target_revision,omitempty"`
	Count          int    `json:"count"`
}
type collectionRow struct{ id, track string }

func validCollectionRef(ref CollectionRef) bool {
	return ref.Kind == "queue" && ref.ID == "" || ref.Kind == "playlist" && validLogicalID(ref.ID, "pl_")
}
func collectionRows(ctx context.Context, tx pgx.Tx, ref CollectionRef) ([]collectionRow, error) {
	out := []collectionRow{}
	if ref.Kind == "queue" {
		q, err := readQueue(ctx, tx)
		if err != nil {
			return nil, err
		}
		for _, v := range q.Items {
			out = append(out, collectionRow{v.ID, v.TrackID})
		}
	} else {
		p, err := readPlaylist(ctx, tx, ref.ID)
		if err != nil {
			return nil, err
		}
		for _, v := range p.Items {
			out = append(out, collectionRow{v.ID, v.TrackID})
		}
	}
	return out, nil
}
func collectionOrder(ctx context.Context, tx pgx.Tx, ref CollectionRef, rows []collectionRow) error {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.id
	}
	table := "queue_items"
	if ref.Kind == "playlist" {
		table = "playlist_items"
	}
	_, err := tx.Exec(ctx, `UPDATE `+table+` qi SET position=ordered.ordinality-1 FROM unnest($1::text[]) WITH ORDINALITY AS ordered(id,ordinality) WHERE qi.id=ordered.id`, ids)
	return err
}
func excludeContextItems(ctx context.Context, tx pgx.Tx, ids []string) error {
	_, err := tx.Exec(ctx, `UPDATE queue_context_tracks m SET excluded=true FROM queue_context_items ci,queue_playback_context c WHERE ci.queue_item_id=ANY($1::text[]) AND ci.ordinal=m.ordinal AND ci.round=c.round`, ids)
	return err
}
func deleteCollectionRows(ctx context.Context, tx pgx.Tx, ref CollectionRef, ids []string) error {
	if ref.Kind == "queue" {
		if err := excludeContextItems(ctx, tx, ids); err != nil {
			return err
		}
	}
	table := "queue_items"
	if ref.Kind == "playlist" {
		table = "playlist_items"
	}
	_, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE id=ANY($1::text[])", ids)
	return err
}
func persistBulkQueueSelection(ctx context.Context, tx pgx.Tx, q QueueSnapshot, removed map[string]bool, old []collectionRow) error {
	if q.CurrentItemID != nil && removed[*q.CurrentItemID] {
		at := 0
		for _, row := range old {
			if row.id == *q.CurrentItemID {
				break
			}
			if !removed[row.id] {
				at++
			}
		}
		q.CurrentItemID = nil
		q.SelectionToken = nil
		q.SelectionState = "stopped"
		full, err := readQueue(ctx, tx)
		if err != nil {
			return err
		}
		if full.Context != nil && at >= len(full.Items) {
			if err = fillQueueContext(ctx, tx, min(contextWindow, 1000-len(full.Items)), -1, false); err != nil {
				return err
			}
			full, err = readQueue(ctx, tx)
			if err != nil {
				return err
			}
		}
		for i := at; i < len(full.Items); i++ {
			if full.Items[i].Available {
				token, e := newUUID()
				if e != nil {
					return e
				}
				q.CurrentItemID = &full.Items[i].ID
				q.SelectionToken = &token
				q.SelectionState = "selected"
				break
			}
		}
	}
	return setQueueSelection(ctx, tx, q.Revision+1, q.CurrentItemID, q.SelectionToken, q.SelectionState)
}
func (s *Store) EditCollection(ctx context.Context, key string, req BulkCollectionRequest) (MutationResult, error) {
	if !validCollectionRef(req.Source) || req.ExpectedVersion < 0 || len(req.ItemIDs) < 1 || len(req.ItemIDs) > 5000 {
		return MutationResult{}, ErrUserInvalid
	}
	transfer := req.Action == "copy" || req.Action == "move"
	if !transfer && req.Action != "remove" && req.Action != "next" && req.Action != "top" && req.Action != "end" {
		return MutationResult{}, ErrUserInvalid
	}
	if transfer && (req.Target == nil || !validCollectionRef(*req.Target) || req.TargetVersion == nil || *req.TargetVersion < 0 || *req.Target == req.Source) {
		return MutationResult{}, ErrUserInvalid
	}
	if !transfer && (req.Target != nil || req.TargetVersion != nil || req.Placement != "") {
		return MutationResult{}, ErrUserInvalid
	}
	if req.Source.Kind == "queue" && (len(req.ItemIDs) > 1000 || req.Action == "top") || req.Source.Kind == "playlist" && req.Action == "next" {
		return MutationResult{}, ErrUserInvalid
	}
	if transfer && req.Target.Kind == "queue" && req.Placement != "next" && req.Placement != "end" || transfer && req.Target.Kind == "playlist" && req.Placement != "" {
		return MutationResult{}, ErrUserInvalid
	}
	prefix := "qi_"
	if req.Source.Kind == "playlist" {
		prefix = "pi_"
	}
	chosen := map[string]bool{}
	for _, id := range req.ItemIDs {
		if !validLogicalID(id, prefix) || chosen[id] {
			return MutationResult{}, ErrUserInvalid
		}
		chosen[id] = true
	}
	return s.mutateWithReceipt(ctx, "collection.edit", key, req, func(tx pgx.Tx) (int, any, error) {
		// Cross-collection writes lock queue first, then playlist IDs in lexical order.
		q, err := lockQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		playlists := map[string]Playlist{}
		playlistIDs := []string{}
		if req.Source.Kind == "playlist" {
			playlistIDs = append(playlistIDs, req.Source.ID)
		}
		if transfer && req.Target.Kind == "playlist" {
			playlistIDs = append(playlistIDs, req.Target.ID)
		}
		slices.Sort(playlistIDs)
		for _, id := range playlistIDs {
			p, e := lockPlaylist(ctx, tx, id)
			if e != nil {
				return 0, nil, e
			}
			playlists[id] = p
		}
		revision := q.Revision
		if req.Source.Kind == "playlist" {
			revision = playlists[req.Source.ID].Revision
		}
		if revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		source, err := collectionRows(ctx, tx, req.Source)
		if err != nil {
			return 0, nil, err
		}
		byID := map[string]collectionRow{}
		remaining := []collectionRow{}
		for _, row := range source {
			byID[row.id] = row
			if !chosen[row.id] {
				remaining = append(remaining, row)
			}
		}
		picked := make([]collectionRow, len(req.ItemIDs))
		for i, id := range req.ItemIDs {
			row, ok := byID[id]
			if !ok {
				return 0, nil, ErrUserNotFound
			}
			picked[i] = row
		}
		result := BulkCollectionChange{Revision: revision, Count: len(picked)}
		if transfer {
			targetRevision := q.Revision
			if req.Target.Kind == "playlist" {
				targetRevision = playlists[req.Target.ID].Revision
			}
			if targetRevision != *req.TargetVersion {
				return 0, nil, ErrStaleVersion
			}
			destination, e := collectionRows(ctx, tx, *req.Target)
			if e != nil {
				return 0, nil, e
			}
			capacity := 1000
			if req.Target.Kind == "playlist" {
				capacity = 5000
			}
			if len(destination)+len(picked) > capacity {
				return 0, nil, ErrUserLimit
			}
			ids, tracks := make([]string, len(picked)), make([]string, len(picked))
			targetPrefix := "qi_"
			if req.Target.Kind == "playlist" {
				targetPrefix = "pi_"
			}
			additions := make([]collectionRow, len(picked))
			for i, row := range picked {
				id, e := newLogicalID(targetPrefix)
				if e != nil {
					return 0, nil, e
				}
				ids[i] = id
				tracks[i] = row.track
				additions[i] = collectionRow{id, row.track}
			}
			if req.Target.Kind == "queue" {
				_, err = tx.Exec(ctx, `INSERT INTO queue_items(id,track_id,position) SELECT id,track,$3+ordinality-1 FROM unnest($1::text[],$2::text[]) WITH ORDINALITY AS rows(id,track,ordinality)`, ids, tracks, len(destination))
				at := len(destination)
				if req.Placement == "next" {
					at = 0
					for i, row := range destination {
						if q.CurrentItemID != nil && row.id == *q.CurrentItemID {
							at = i + 1
							break
						}
					}
				}
				destination = slices.Insert(destination, at, additions...)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO playlist_items(id,playlist_id,track_id,position) SELECT id,$3,track,$4+ordinality-1 FROM unnest($1::text[],$2::text[]) WITH ORDINALITY AS rows(id,track,ordinality)`, ids, tracks, req.Target.ID, len(destination))
				destination = append(destination, additions...)
			}
			if err != nil {
				return 0, nil, err
			}
			if err = collectionOrder(ctx, tx, *req.Target, destination); err != nil {
				return 0, nil, err
			}
			targetRevision++
			if req.Target.Kind == "queue" {
				if err = setQueueSelection(ctx, tx, targetRevision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
					return 0, nil, err
				}
			} else {
				if _, err = tx.Exec(ctx, "UPDATE playlists SET revision=$2,updated_at=now() WHERE id=$1", req.Target.ID, targetRevision); err != nil {
					return 0, nil, err
				}
			}
			result.TargetRevision = &targetRevision
			if req.Action == "copy" {
				return 200, result, nil
			}
		}
		removed := map[string]bool{}
		if req.Action == "remove" || req.Action == "move" {
			removed = chosen
			if err = deleteCollectionRows(ctx, tx, req.Source, req.ItemIDs); err != nil {
				return 0, nil, err
			}
		} else {
			if req.Source.Kind == "queue" && q.CurrentItemID != nil && chosen[*q.CurrentItemID] {
				picked = slices.DeleteFunc(picked, func(row collectionRow) bool { return row.id == *q.CurrentItemID })
				remaining = []collectionRow{}
				for _, row := range source {
					if !chosen[row.id] || row.id == *q.CurrentItemID {
						remaining = append(remaining, row)
					}
				}
			}
			at := len(remaining)
			if req.Action == "top" {
				at = 0
			}
			if req.Action == "next" {
				at = 0
				for i, row := range remaining {
					if q.CurrentItemID != nil && row.id == *q.CurrentItemID {
						at = i + 1
						break
					}
				}
			}
			remaining = slices.Insert(remaining, at, picked...)
		}
		if err = collectionOrder(ctx, tx, req.Source, remaining); err != nil {
			return 0, nil, err
		}
		if req.Source.Kind == "queue" {
			if err = persistBulkQueueSelection(ctx, tx, q, removed, source); err != nil {
				return 0, nil, err
			}
		} else {
			if _, err = tx.Exec(ctx, "UPDATE playlists SET revision=$2,updated_at=now() WHERE id=$1", req.Source.ID, revision+1); err != nil {
				return 0, nil, err
			}
		}
		result.Revision = revision + 1
		return 200, result, nil
	})
}
