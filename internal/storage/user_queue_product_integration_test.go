//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestM2AtomicCollectionAndExistingOccurrenceSelection(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "M2 queue", "C:\\m2-queue-fixture")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 501)
	b, locB := userFixtureTrack(t, s, root, 502)
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	request := QueueCollectionRequest{TrackIDs: []string{a, b, a}, Placement: "now", ExpectedVersion: 0}
	first, err := s.AddQueueCollection(ctx, key(1), request)
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q.Revision != 1 || len(q.Items) != 3 || q.Items[0].TrackID != a || q.Items[1].TrackID != b || q.Items[2].TrackID != a || q.Items[0].ID == q.Items[2].ID {
		t.Fatalf("bad atomic order %#v", q)
	}
	replay, err := s.AddQueueCollection(ctx, key(1), request)
	if err != nil || !replay.Replayed || string(first.Body) != string(replay.Body) {
		t.Fatalf("collection receipt %v %#v", err, replay)
	}
	bad := QueueCollectionRequest{TrackIDs: []string{a, "trk_" + strings.Repeat("f", 32)}, Placement: "end", ExpectedVersion: 1}
	if _, err = s.AddQueueCollection(ctx, key(2), bad); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("bad reference %v", err)
	}
	after, _ := s.ReadQueue(ctx)
	if len(after.Items) != 3 || after.Revision != 1 {
		t.Fatal("partial collection committed")
	}
	if _, err = s.AddQueueCollection(ctx, key(3), request); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale collection %v", err)
	}
	conflict := request
	conflict.Placement = "end"
	if _, err = s.AddQueueCollection(ctx, key(1), conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed intent %v", err)
	}
	selectReq := QueueSelectRequest{ItemID: q.Items[2].ID, ExpectedVersion: 1}
	selected, err := s.SelectQueueItem(ctx, key(4), selectReq)
	if err != nil {
		t.Fatal(err)
	}
	after, _ = s.ReadQueue(ctx)
	if len(after.Items) != 3 || after.Revision != 2 || *after.CurrentItemID != q.Items[2].ID || *after.SelectionToken == *q.SelectionToken {
		t.Fatal("selection duplicated or reused generation")
	}
	replay, err = s.SelectQueueItem(ctx, key(4), selectReq)
	if err != nil || !replay.Replayed || string(selected.Body) != string(replay.Body) {
		t.Fatalf("selection receipt %v", err)
	}
	if _, err = s.SelectQueueItem(ctx, key(5), selectReq); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale select %v", err)
	}
	if _, err = s.AdvanceQueue(ctx, key(6), QueueAdvanceRequest{Direction: "next", ExpectedVersion: 2, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); !errors.Is(err, ErrStaleSelection) {
		t.Fatalf("old decoder borrowed selection %v", err)
	}
	if _, err = s.pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='missing',unavailable_at=now() WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SelectQueueItem(ctx, key(7), QueueSelectRequest{ItemID: q.Items[1].ID, ExpectedVersion: 2}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unavailable select %v", err)
	}
	if _, err = s.AddQueueCollection(ctx, key(8), QueueCollectionRequest{TrackIDs: make([]string, 1001), Placement: "end", ExpectedVersion: 2}); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("oversized batch %v", err)
	}
	many := make([]string, 998)
	for i := range many {
		many[i] = a
	}
	if _, err = s.AddQueueCollection(ctx, key(9), QueueCollectionRequest{TrackIDs: many, Placement: "end", ExpectedVersion: 2}); !errors.Is(err, ErrUserLimit) {
		t.Fatalf("queue capacity %v", err)
	}
	after, _ = s.ReadQueue(ctx)
	if after.Revision != 2 || len(after.Items) != 3 {
		t.Fatal("failed batch mutated queue")
	}
	// Inserting Play next preserves input order and the selected generation.
	if _, err = s.AddQueueCollection(ctx, key(10), QueueCollectionRequest{TrackIDs: []string{a, a}, Placement: "next", ExpectedVersion: 2}); err != nil {
		t.Fatal(err)
	}
	next, _ := s.ReadQueue(ctx)
	if len(next.Items) != 5 || *next.CurrentItemID != *after.CurrentItemID || *next.SelectionToken != *after.SelectionToken {
		t.Fatal("next batch changed selection")
	}
}
