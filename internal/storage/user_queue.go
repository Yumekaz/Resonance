package storage

import (
	"context"
	"math/rand/v2"
	"slices"

	"github.com/jackc/pgx/v5"
)

type QueueItem struct {
	ID           string  `json:"id"`
	TrackID      string  `json:"track_id"`
	Position     int64   `json:"position"`
	Title        *string `json:"title"`
	ArtistCredit *string `json:"artist_credit"`
	Available    bool    `json:"available"`
	LastSkipCode *string `json:"last_skip_code"`
}
type QueueSnapshot struct {
	Context        *QueueContext `json:"context,omitempty"`
	Revision       int64         `json:"revision"`
	CurrentItemID  *string       `json:"current_item_id"`
	SelectionToken *string       `json:"selection_token"`
	SelectionState string        `json:"selection_state"`
	Items          []QueueItem   `json:"items"`
}
type QueueChange struct {
	Context        *QueueContext `json:"context,omitempty"`
	Revision       int64         `json:"revision"`
	CurrentItemID  *string       `json:"current_item_id"`
	SelectionToken *string       `json:"selection_token"`
	SelectionState string        `json:"selection_state"`
	ItemID         *string       `json:"item_id,omitempty"`
	SkippedItemIDs []string      `json:"skipped_item_ids,omitempty"`
	SessionID      *string       `json:"session_id,omitempty"`
}

type queueQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readQueue(ctx context.Context, q queueQueryer) (QueueSnapshot, error) {
	var out QueueSnapshot
	if err := q.QueryRow(ctx, "SELECT revision,current_item_id,selection_token::text,selection_state FROM active_queue WHERE singleton=true").Scan(&out.Revision, &out.CurrentItemID, &out.SelectionToken, &out.SelectionState); err != nil {
		return out, err
	}
	rows, err := q.Query(ctx, `SELECT qi.id,qi.track_id,qi.position,t.title,t.artist_credit,qi.last_skip_code,EXISTS(SELECT 1 FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE mo.track_id=qi.track_id AND ml.availability='available' AND lr.enabled) AND NOT EXISTS(SELECT 1 FROM queue_context_items ci JOIN queue_context_tracks cm ON cm.ordinal=ci.ordinal WHERE ci.queue_item_id=qi.id AND cm.failed) FROM queue_items qi JOIN tracks t ON t.id=qi.track_id ORDER BY qi.position,qi.id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Items = []QueueItem{}
	for rows.Next() {
		var item QueueItem
		if err := rows.Scan(&item.ID, &item.TrackID, &item.Position, &item.Title, &item.ArtistCredit, &item.LastSkipCode, &item.Available); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Context, err = readQueueContext(ctx, q, out.CurrentItemID)
	return out, err
}
func (s *Store) ReadQueue(ctx context.Context) (QueueSnapshot, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return QueueSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	out, err := readQueue(ctx, tx)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return QueueSnapshot{}, err
	}
	return out, nil
}

func lockQueue(ctx context.Context, tx pgx.Tx) (QueueSnapshot, error) {
	var q QueueSnapshot
	err := tx.QueryRow(ctx, "SELECT revision,current_item_id,selection_token::text,selection_state FROM active_queue WHERE singleton=true FOR UPDATE").Scan(&q.Revision, &q.CurrentItemID, &q.SelectionToken, &q.SelectionState)
	return q, err
}
func queueChange(q QueueSnapshot) QueueChange {
	return QueueChange{Revision: q.Revision, CurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken, SelectionState: q.SelectionState, Context: q.Context}
}
func currentIndex(items []QueueItem, id *string) int {
	if id == nil {
		return -1
	}
	for i, item := range items {
		if item.ID == *id {
			return i
		}
	}
	return -1
}
func persistQueueOrder(ctx context.Context, tx pgx.Tx, items []QueueItem) error {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	_, err := tx.Exec(ctx, `UPDATE queue_items qi SET position=ordered.ordinality-1 FROM unnest($1::text[]) WITH ORDINALITY AS ordered(id,ordinality) WHERE qi.id=ordered.id`, ids)
	return err
}
func setQueueSelection(ctx context.Context, tx pgx.Tx, revision int64, current, token *string, state string) error {
	_, err := tx.Exec(ctx, "UPDATE active_queue SET revision=$1,current_item_id=$2,selection_token=$3,selection_state=$4,updated_at=now() WHERE singleton=true", revision, current, token, state)
	return err
}

type QueueAddRequest struct {
	TrackID         string `json:"track_id"`
	Placement       string `json:"placement"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Store) AddQueueItem(ctx context.Context, key string, req QueueAddRequest) (MutationResult, error) {
	if !ValidCatalogID(req.TrackID, "track") || req.ExpectedVersion < 0 || !(req.Placement == "end" || req.Placement == "next" || req.Placement == "now") {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "queue.add", key, req, func(tx pgx.Tx) (int, any, error) {
		q, err := lockQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if q.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		exists, err := publicTrackExists(ctx, tx, req.TrackID)
		if err != nil {
			return 0, nil, err
		}
		if !exists {
			return 0, nil, ErrUserNotFound
		}
		items, err := readQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if len(items.Items) >= 1000 {
			return 0, nil, ErrUserLimit
		}
		id, err := newLogicalID("qi_")
		if err != nil {
			return 0, nil, err
		}
		at := len(items.Items)
		if req.Placement != "end" {
			at = currentIndex(items.Items, q.CurrentItemID) + 1
			if at < 0 {
				at = 0
			}
		}
		if _, err = tx.Exec(ctx, "INSERT INTO queue_items(id,track_id,position) VALUES($1,$2,$3)", id, req.TrackID, len(items.Items)); err != nil {
			return 0, nil, err
		}
		item := QueueItem{ID: id, TrackID: req.TrackID}
		items.Items = slices.Insert(items.Items, at, item)
		if err = persistQueueOrder(ctx, tx, items.Items); err != nil {
			return 0, nil, err
		}
		q.Revision++
		if req.Placement == "now" {
			token, e := newUUID()
			if e != nil {
				return 0, nil, e
			}
			q.CurrentItemID = &id
			q.SelectionToken = &token
			q.SelectionState = "selected"
		}
		if err = setQueueSelection(ctx, tx, q.Revision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
			return 0, nil, err
		}
		change := queueChange(q)
		change.ItemID = &id
		return 201, change, nil
	})
}

type QueueRemoveRequest struct {
	ItemID          string `json:"item_id"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Store) RemoveQueueItem(ctx context.Context, key string, req QueueRemoveRequest) (MutationResult, error) {
	if !validLogicalID(req.ItemID, "qi_") || req.ExpectedVersion < 0 {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "queue.remove", key, req, func(tx pgx.Tx) (int, any, error) {
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
		if err = excludeContextItems(ctx, tx, []string{req.ItemID}); err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM queue_items WHERE id=$1", req.ItemID); err != nil {
			return 0, nil, err
		}
		full.Items = slices.Delete(full.Items, at, at+1)
		if err = persistQueueOrder(ctx, tx, full.Items); err != nil {
			return 0, nil, err
		}
		if q.CurrentItemID != nil && *q.CurrentItemID == req.ItemID {
			q.CurrentItemID = nil
			q.SelectionToken = nil
			q.SelectionState = "stopped"
			for i := at; i < len(full.Items); i++ {
				if full.Items[i].Available {
					token, e := newUUID()
					if e != nil {
						return 0, nil, e
					}
					id := full.Items[i].ID
					q.CurrentItemID = &id
					q.SelectionToken = &token
					q.SelectionState = "selected"
					break
				}
			}
			if q.CurrentItemID == nil && full.Context != nil {
				if err = fillQueueContext(ctx, tx, min(contextWindow, 1000-len(full.Items)), -1, false); err != nil {
					return 0, nil, err
				}
				refreshed, e := readQueue(ctx, tx)
				if e != nil {
					return 0, nil, e
				}
				for i := at; i < len(refreshed.Items); i++ {
					if refreshed.Items[i].Available {
						token, e := newUUID()
						if e != nil {
							return 0, nil, e
						}
						q.CurrentItemID = &refreshed.Items[i].ID
						q.SelectionToken = &token
						q.SelectionState = "selected"
						break
					}
				}
			}
		}
		q.Revision++
		if err = setQueueSelection(ctx, tx, q.Revision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
			return 0, nil, err
		}
		change := queueChange(q)
		change.ItemID = &req.ItemID
		return 200, change, nil
	})
}

type QueueOrderRequest struct {
	ItemIDs         []string `json:"item_ids"`
	ExpectedVersion int64    `json:"expected_version"`
}

func (s *Store) ReorderQueue(ctx context.Context, key string, req QueueOrderRequest) (MutationResult, error) {
	if req.ExpectedVersion < 0 || len(req.ItemIDs) > 1000 {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "queue.order", key, req, func(tx pgx.Tx) (int, any, error) {
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
		if len(req.ItemIDs) != len(full.Items) {
			return 0, nil, ErrUserInvalid
		}
		found := map[string]QueueItem{}
		for _, item := range full.Items {
			found[item.ID] = item
		}
		ordered := make([]QueueItem, 0, len(req.ItemIDs))
		for _, id := range req.ItemIDs {
			item, ok := found[id]
			if !ok {
				return 0, nil, ErrUserInvalid
			}
			delete(found, id)
			ordered = append(ordered, item)
		}
		if len(found) != 0 {
			return 0, nil, ErrUserInvalid
		}
		if err = persistQueueOrder(ctx, tx, ordered); err != nil {
			return 0, nil, err
		}
		q.Revision++
		if err = setQueueSelection(ctx, tx, q.Revision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
			return 0, nil, err
		}
		return 200, queueChange(q), nil
	})
}

type QueueClearRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

func (s *Store) ClearQueue(ctx context.Context, key string, req QueueClearRequest) (MutationResult, error) {
	if req.ExpectedVersion < 0 {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "queue.clear", key, req, func(tx pgx.Tx) (int, any, error) {
		q, err := lockQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if q.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		if err = clearQueueContext(ctx, tx); err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec(ctx, "UPDATE active_queue SET current_item_id=NULL,selection_token=NULL,selection_state='stopped' WHERE singleton=true"); err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM queue_items"); err != nil {
			return 0, nil, err
		}
		q.Revision++
		q.CurrentItemID = nil
		q.SelectionToken = nil
		q.SelectionState = "stopped"
		if err = setQueueSelection(ctx, tx, q.Revision, nil, nil, "stopped"); err != nil {
			return 0, nil, err
		}
		return 200, queueChange(q), nil
	})
}

type QueueAdvanceRequest struct {
	Reshuffle             bool                  `json:"reshuffle,omitempty"`
	Repeat                string                `json:"repeat,omitempty"`
	Direction             string                `json:"direction"`
	ExpectedVersion       int64                 `json:"expected_version"`
	ExpectedCurrentItemID *string               `json:"expected_current_item_id"`
	SelectionToken        *string               `json:"selection_token"`
	FailureCode           *string               `json:"failure_code,omitempty"`
	SessionID             *string               `json:"session_id,omitempty"`
	FinalReport           *SessionReportRequest `json:"final_report,omitempty"`
}

func sameNullableString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func (s *Store) AdvanceQueue(ctx context.Context, key string, req QueueAdvanceRequest) (MutationResult, error) {
	if req.Reshuffle && (req.Repeat != "all" || req.Direction == "previous") {
		return MutationResult{}, ErrUserInvalid
	}
	if req.Repeat != "" && req.Repeat != "off" && req.Repeat != "one" && req.Repeat != "all" {
		return MutationResult{}, ErrUserInvalid
	}
	if req.ExpectedVersion < 0 || !(req.Direction == "next" || req.Direction == "previous" || req.Direction == "ended") || req.FailureCode != nil && (*req.FailureCode != "resolver_failed" || req.Direction != "next") {
		return MutationResult{}, ErrUserInvalid
	}
	if (req.ExpectedCurrentItemID != nil && !validLogicalID(*req.ExpectedCurrentItemID, "qi_")) || (req.SelectionToken != nil && !validUUID(*req.SelectionToken)) {
		return MutationResult{}, ErrUserInvalid
	}
	if req.Direction == "ended" && (req.SessionID == nil || req.FinalReport == nil || !validUUID(*req.SessionID)) {
		return MutationResult{}, ErrUserInvalid
	}
	if req.Direction == "ended" && (req.FinalReport.TerminalReason == nil || *req.FinalReport.TerminalReason != "ended") {
		return MutationResult{}, ErrUserInvalid
	}
	if req.Direction != "ended" && (req.SessionID != nil || req.FinalReport != nil) {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "queue.advance", key, req, func(tx pgx.Tx) (int, any, error) {
		q, err := lockQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if !sameNullableString(q.CurrentItemID, req.ExpectedCurrentItemID) || !sameNullableString(q.SelectionToken, req.SelectionToken) {
			return 0, nil, ErrStaleSelection
		}
		if q.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		full, err := readQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		at := currentIndex(full.Items, q.CurrentItemID)
		if req.Direction == "ended" {
			if q.SelectionState != "selected" || at < 0 {
				return 0, nil, ErrStaleSelection
			}
			if _, err = applySessionReportTx(ctx, tx, *req.SessionID, *req.FinalReport, true, full.Items[at].ID, *q.SelectionToken); err != nil {
				return 0, nil, err
			}
		}
		if req.FailureCode != nil && at >= 0 {
			if _, err = tx.Exec(ctx, "UPDATE queue_items SET last_skip_code='resolver_failed',last_skipped_at=now() WHERE id=$1", full.Items[at].ID); err != nil {
				return 0, nil, err
			}
		}
		full, err = prepareContextAdvance(ctx, tx, full, req)
		if err != nil {
			return 0, nil, err
		}
		at = currentIndex(full.Items, q.CurrentItemID)
		skipped := []string{}
		selected := -1
		if req.Direction == "ended" && req.Repeat == "one" && full.Items[at].Available {
			selected = at
		} else if req.Direction == "previous" {
			for i := at - 1; i >= 0; i-- {
				if full.Items[i].Available {
					selected = i
					break
				}
				skipped = append(skipped, full.Items[i].ID)
			}
		} else {
			for i := at + 1; i < len(full.Items); i++ {
				if full.Items[i].Available {
					selected = i
					break
				}
				skipped = append(skipped, full.Items[i].ID)
			}
		}
		// Loop at most once. Unavailable items never create an endless repeat
		// cycle, and a resolver-failed current item is not immediately retried.
		if selected < 0 && req.Repeat == "all" && full.Context == nil {
			if req.Reshuffle {
				oldID, oldTrack := "", ""
				if at >= 0 {
					oldID, oldTrack = full.Items[at].ID, full.Items[at].TrackID
				}
				before := make([]string, len(full.Items))
				for i, item := range full.Items {
					before[i] = item.ID
				}
				rand.Shuffle(len(full.Items), func(i, j int) { full.Items[i], full.Items[j] = full.Items[j], full.Items[i] })
				first := -1
				seenSkip := make(map[string]bool, len(skipped))
				for _, id := range skipped {
					seenSkip[id] = true
				}
				for i, item := range full.Items {
					if !item.Available && !seenSkip[item.ID] {
						skipped = append(skipped, item.ID)
						seenSkip[item.ID] = true
					}
					if first < 0 && item.Available && item.TrackID != oldTrack {
						first = i
					}
				}
				if first < 0 {
					for i, item := range full.Items {
						if item.Available && (req.FailureCode == nil || item.ID != oldID) {
							first = i
							break
						}
					}
				}
				if first >= 0 {
					// Keep duplicate occurrences, but avoid immediately replaying
					// the same musical identity when another one is available.
					full.Items[0], full.Items[first] = full.Items[first], full.Items[0]
					same := true
					for i, item := range full.Items {
						if item.ID != before[i] {
							same = false
							break
						}
					}
					if same && len(full.Items) > 2 {
						last := full.Items[len(full.Items)-1]
						copy(full.Items[2:], full.Items[1:len(full.Items)-1])
						full.Items[1] = last
					}
					if err = persistQueueOrder(ctx, tx, full.Items); err != nil {
						return 0, nil, err
					}
					selected = 0
				}
			} else if req.Direction == "previous" {
				for i := len(full.Items) - 1; i >= at && i >= 0; i-- {
					if full.Items[i].Available {
						selected = i
						break
					}
					skipped = append(skipped, full.Items[i].ID)
				}
			} else {
				for i := 0; i <= at && i < len(full.Items); i++ {
					if req.FailureCode != nil && i == at {
						continue
					}
					if full.Items[i].Available {
						selected = i
						break
					}
					skipped = append(skipped, full.Items[i].ID)
				}
			}
		}
		for _, id := range skipped {
			if _, err = tx.Exec(ctx, "UPDATE queue_items SET last_skip_code='track_unavailable',last_skipped_at=now() WHERE id=$1", id); err != nil {
				return 0, nil, err
			}
		}
		if selected >= 0 {
			token, e := newUUID()
			if e != nil {
				return 0, nil, e
			}
			id := full.Items[selected].ID
			q.CurrentItemID = &id
			q.SelectionToken = &token
			q.SelectionState = "selected"
		} else if req.Direction != "previous" {
			q.SelectionToken = nil
			q.SelectionState = "stopped"
		}
		q.Revision++
		if err = setQueueSelection(ctx, tx, q.Revision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
			return 0, nil, err
		}
		change := queueChange(q)
		change.Context = full.Context
		change.SkippedItemIDs = skipped
		change.SessionID = req.SessionID
		return 200, change, nil
	})
}

func (s *Store) QueueRevision(ctx context.Context) (int64, error) {
	var version int64
	err := s.pool.QueryRow(ctx, "SELECT revision FROM active_queue WHERE singleton=true").Scan(&version)
	return version, err
}
