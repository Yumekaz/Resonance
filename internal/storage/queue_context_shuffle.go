package storage

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type ContextShuffleRequest struct {
	Shuffle         bool  `json:"shuffle"`
	ExpectedVersion int64 `json:"expected_version"`
}

func (s *Store) ShuffleQueueContext(ctx context.Context, key string, req ContextShuffleRequest) (MutationResult, error) {
	if req.ExpectedVersion < 0 {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "queue.context.shuffle", key, req, func(tx pgx.Tx) (int, any, error) {
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
		if full.Context == nil {
			return 0, nil, ErrUserNotFound
		}
		at := currentIndex(full.Items, q.CurrentItemID)
		if at >= 0 {
			if _, err = tx.Exec(ctx, `UPDATE queue_context_tracks m SET seen=true FROM queue_context_items ci WHERE ci.queue_item_id=$1 AND ci.ordinal=m.ordinal`, q.CurrentItemID); err != nil {
				return 0, nil, err
			}
		}
		// Explicit queued additions and prior/current occurrences keep their IDs,
		// relative order and selection authority. Only future generated rows refresh.
		if _, err = tx.Exec(ctx, `DELETE FROM queue_items qi USING queue_context_items ci WHERE ci.queue_item_id=qi.id AND qi.position>$1`, at); err != nil {
			return 0, nil, err
		}
		order := "ordinal"
		if req.Shuffle {
			order = "random(),ordinal"
		}
		if _, err = tx.Exec(ctx, `UPDATE queue_context_tracks m SET play_rank=r.rank FROM (SELECT ordinal,row_number() OVER(ORDER BY CASE WHEN seen OR excluded OR failed THEN 1 ELSE 0 END,`+order+`)-1 rank FROM queue_context_tracks) r WHERE m.ordinal=r.ordinal`); err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE queue_playback_context SET shuffled=$1,next_rank=0,exhausted=false`, req.Shuffle); err != nil {
			return 0, nil, err
		}
		full, err = readQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if err = persistQueueOrder(ctx, tx, full.Items); err != nil {
			return 0, nil, err
		}
		if err = fillQueueContext(ctx, tx, min(contextWindow, 1000-len(full.Items)), -1, false); err != nil {
			return 0, nil, err
		}
		q.Revision++
		if err = setQueueSelection(ctx, tx, q.Revision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
			return 0, nil, err
		}
		q.Context, err = readQueueContext(ctx, tx, q.CurrentItemID)
		if err != nil {
			return 0, nil, err
		}
		return 200, queueChange(q), nil
	})
}
