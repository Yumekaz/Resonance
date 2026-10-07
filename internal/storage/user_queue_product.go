package storage

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
)

type QueueSelectRequest struct {
	ItemID          string `json:"item_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

// SelectQueueItem selects an existing occurrence. It never inserts a duplicate
// and always issues a fresh generation, so old decoders cannot advance it.
func (s *Store) SelectQueueItem(ctx context.Context, key string, req QueueSelectRequest) (MutationResult, error) {
	if !validLogicalID(req.ItemID, "qi_") || req.ExpectedVersion < 0 {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "queue.select", key, req, func(tx pgx.Tx) (int, any, error) {
		q, err := lockQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if q.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		full, err := readQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		index := currentIndex(full.Items, &req.ItemID)
		if index < 0 || !full.Items[index].Available {
			return 0, nil, ErrUserNotFound
		}
		token, err := newUUID()
		if err != nil {
			return 0, nil, err
		}
		q.Revision++
		q.CurrentItemID = &req.ItemID
		q.SelectionToken = &token
		q.SelectionState = "selected"
		if err = setQueueSelection(ctx, tx, q.Revision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
			return 0, nil, err
		}
		return 200, queueChange(q), nil
	})
}

type QueueCollectionRequest struct {
	StartIndex      int      `json:"start_index,omitempty"`
	TrackIDs        []string `json:"track_ids"`
	Placement       string   `json:"placement"`
	ExpectedVersion int64    `json:"expected_version"`
}

// AddQueueCollection commits all occurrences, their order, selection and retry
// receipt together. A bad reference, limit or stale revision commits none.
func (s *Store) AddQueueCollection(ctx context.Context, key string, req QueueCollectionRequest) (MutationResult, error) {
	if req.ExpectedVersion < 0 || len(req.TrackIDs) < 1 || len(req.TrackIDs) > 1000 || !(req.Placement == "now" || req.Placement == "next" || req.Placement == "end" || req.Placement == "replace") || req.StartIndex < 0 || req.StartIndex >= len(req.TrackIDs) || req.Placement != "replace" && req.StartIndex != 0 {
		return MutationResult{}, ErrUserInvalid
	}
	unique := make(map[string]bool)
	for _, id := range req.TrackIDs {
		if !ValidCatalogID(id, "track") {
			return MutationResult{}, ErrUserInvalid
		}
		unique[id] = true
	}
	return s.mutateWithReceipt(ctx, "queue.collection", key, req, func(tx pgx.Tx) (int, any, error) {
		q, err := lockQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if q.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		full, err := readQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if req.Placement != "replace" && len(full.Items)+len(req.TrackIDs) > 1000 {
			return 0, nil, ErrUserLimit
		}
		var count int
		err = tx.QueryRow(ctx, `SELECT count(DISTINCT t.id) FROM tracks t JOIN media_objects mo ON mo.track_id=t.id JOIN media_locations ml ON ml.media_object_id=mo.id WHERE t.id=ANY($1::text[]) AND ml.root_id IS NOT NULL`, req.TrackIDs).Scan(&count)
		if err != nil {
			return 0, nil, err
		}
		if count != len(unique) {
			return 0, nil, ErrUserNotFound
		}
		if req.Placement == "replace" {
			if err = clearQueueContext(ctx, tx); err != nil {
				return 0, nil, err
			}
			var playable bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE mo.track_id=$1 AND ml.availability='available' AND lr.enabled)`, req.TrackIDs[req.StartIndex]).Scan(&playable); err != nil {
				return 0, nil, err
			}
			if !playable {
				return 0, nil, ErrUserNotFound
			}
			if _, err = tx.Exec(ctx, "DELETE FROM queue_items"); err != nil {
				return 0, nil, err
			}
			full.Items = nil
		}
		ids := make([]string, len(req.TrackIDs))
		additions := make([]QueueItem, len(req.TrackIDs))
		for i, trackID := range req.TrackIDs {
			id, e := newLogicalID("qi_")
			if e != nil {
				return 0, nil, e
			}
			ids[i] = id
			additions[i] = QueueItem{ID: id, TrackID: trackID}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO queue_items(id,track_id,position) SELECT id,track_id,$3+ordinality-1 FROM unnest($1::text[],$2::text[]) WITH ORDINALITY AS entries(id,track_id,ordinality)`, ids, req.TrackIDs, len(full.Items)); err != nil {
			return 0, nil, err
		}
		at := len(full.Items)
		if req.Placement != "end" && req.Placement != "replace" {
			at = currentIndex(full.Items, q.CurrentItemID) + 1
		}
		full.Items = slices.Insert(full.Items, at, additions...)
		order := make([]string, len(full.Items))
		for i, item := range full.Items {
			order[i] = item.ID
		}
		if _, err = tx.Exec(ctx, `UPDATE queue_items qi SET position=ordered.ordinality-1 FROM unnest($1::text[]) WITH ORDINALITY AS ordered(id,ordinality) WHERE qi.id=ordered.id`, order); err != nil {
			return 0, nil, err
		}
		q.Revision++
		if req.Placement == "now" || req.Placement == "replace" {
			token, e := newUUID()
			if e != nil {
				return 0, nil, e
			}
			q.CurrentItemID = &ids[req.StartIndex]
			q.SelectionToken = &token
			q.SelectionState = "selected"
		}
		if err = setQueueSelection(ctx, tx, q.Revision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
			return 0, nil, err
		}
		change := queueChange(q)
		change.ItemID = &ids[req.StartIndex]
		return 201, change, nil
	})
}
