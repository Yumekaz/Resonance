//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestM2LibraryContextReplacementIsAtomicAndStartsAtChosenOccurrence(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "context", "C:\\context-fixture")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 92001)
	b, locB := userFixtureTrack(t, s, root, 92002)
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	if _, err = s.AddQueueCollection(ctx, key(1), QueueCollectionRequest{TrackIDs: []string{a, b}, Placement: "now", ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	old, _ := s.ReadQueue(ctx)
	bad := QueueCollectionRequest{TrackIDs: []string{a, "trk_00000000000000000000000000000000"}, Placement: "replace", StartIndex: 1, ExpectedVersion: old.Revision}
	if _, err = s.AddQueueCollection(ctx, key(2), bad); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("bad replacement %v", err)
	}
	unchanged, _ := s.ReadQueue(ctx)
	if unchanged.Revision != old.Revision || *unchanged.SelectionToken != *old.SelectionToken || len(unchanged.Items) != 2 {
		t.Fatal("failed replacement lost prior queue")
	}
	req := QueueCollectionRequest{TrackIDs: []string{a, b, a}, Placement: "replace", StartIndex: 1, ExpectedVersion: old.Revision}
	result, err := s.AddQueueCollection(ctx, key(3), req)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := s.ReadQueue(ctx)
	if len(q.Items) != 3 || q.Items[0].TrackID != a || q.Items[1].TrackID != b || q.Items[2].TrackID != a || *q.CurrentItemID != q.Items[1].ID || *q.SelectionToken == *old.SelectionToken || q.Items[0].ID == q.Items[2].ID {
		t.Fatalf("context order/selection %#v", q)
	}
	replay, err := s.AddQueueCollection(ctx, key(3), req)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal("replacement lost receipt replay")
	}
	changed := req
	changed.StartIndex = 2
	if _, err = s.AddQueueCollection(ctx, key(3), changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed start intent %v", err)
	}
	if _, err = s.AdvanceQueue(ctx, key(4), QueueAdvanceRequest{Direction: "next", ExpectedVersion: old.Revision, ExpectedCurrentItemID: old.CurrentItemID, SelectionToken: old.SelectionToken}); !errors.Is(err, ErrStaleSelection) {
		t.Fatalf("old decoder borrowed new context: %v", err)
	}
	if _, err = s.pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='missing',unavailable_at=now() WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	unavailable := req
	unavailable.ExpectedVersion = q.Revision
	if _, err = s.AddQueueCollection(ctx, key(5), unavailable); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unavailable selected song replaced queue: %v", err)
	}
	after, _ := s.ReadQueue(ctx)
	if after.Revision != q.Revision || *after.CurrentItemID != *q.CurrentItemID {
		t.Fatal("unavailable replacement committed")
	}
	invalid := req
	invalid.StartIndex = 3
	if _, err = s.AddQueueCollection(ctx, key(6), invalid); !errors.Is(err, ErrUserInvalid) {
		t.Fatal("out of range start accepted")
	}
	invalid = req
	invalid.Placement = "end"
	if _, err = s.AddQueueCollection(ctx, key(7), invalid); !errors.Is(err, ErrUserInvalid) {
		t.Fatal("non-playing placement accepted start index")
	}
}
