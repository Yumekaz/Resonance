//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestM2ShuffledCycleKeepsBoundedOccurrencesAndAtomicReceipt(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "cycle", "C:\\cycle-fixture")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 93001)
	b, locB := userFixtureTrack(t, s, root, 93002)
	c, _ := userFixtureTrack(t, s, root, 93003)
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	if _, err = s.AddQueueCollection(ctx, key(1), QueueCollectionRequest{TrackIDs: []string{a, b, a, c}, Placement: "replace", StartIndex: 3, ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ReadQueue(ctx)
	session := key(2)
	if _, err = s.StartListeningSession(ctx, SessionStartRequest{ID: session, TrackID: c, ClientInstanceID: key(3), QueueItemID: before.CurrentItemID, SelectionToken: before.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '60 seconds' WHERE id=$1", session); err != nil {
		t.Fatal(err)
	}
	duration := int64(40000)
	reason := "ended"
	report := SessionReportRequest{Sequence: 1, ListenedMS: 40000, PositionMS: 40000, DurationMS: &duration, TerminalReason: &reason}
	req := QueueAdvanceRequest{Direction: "ended", Repeat: "all", Reshuffle: true, ExpectedVersion: before.Revision, ExpectedCurrentItemID: before.CurrentItemID, SelectionToken: before.SelectionToken, SessionID: &session, FinalReport: &report}
	// Inject a late selection-write failure: order and report must roll back too.
	if _, err = s.pool.Exec(ctx, `CREATE FUNCTION fail_cycle_selection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'cycle rollback'; END $$; CREATE TRIGGER fail_cycle_selection BEFORE UPDATE ON active_queue FOR EACH ROW EXECUTE FUNCTION fail_cycle_selection()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdvanceQueue(ctx, key(4), req); err == nil {
		t.Fatal("injected cycle failure succeeded")
	}
	failed, _ := s.ReadQueue(ctx)
	if failed.Revision != before.Revision || *failed.SelectionToken != *before.SelectionToken {
		t.Fatal("failed cycle changed selection")
	}
	for i, item := range failed.Items {
		if item.ID != before.Items[i].ID {
			t.Fatal("failed cycle reordered queue")
		}
	}
	history, _ := s.ListHistory(ctx, 50, nil, "")
	if len(history) != 0 {
		t.Fatal("failed cycle finalized history")
	}
	if _, err = s.pool.Exec(ctx, `DROP TRIGGER fail_cycle_selection ON active_queue; DROP FUNCTION fail_cycle_selection()`); err != nil {
		t.Fatal(err)
	}
	result, err := s.AdvanceQueue(ctx, key(4), req)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := s.ReadQueue(ctx)
	if len(q.Items) != 4 || q.CurrentItemID == nil || *q.CurrentItemID != q.Items[0].ID || q.Items[0].TrackID == c || *q.SelectionToken == *before.SelectionToken {
		t.Fatalf("bad new cycle %#v", q)
	}
	oldIDs, newIDs := []string{}, []string{}
	same := true
	for i, item := range q.Items {
		oldIDs = append(oldIDs, before.Items[i].ID)
		newIDs = append(newIDs, item.ID)
		if item.Position != int64(i) {
			t.Fatal("cycle order not dense")
		}
		if item.ID != before.Items[i].ID {
			same = false
		}
	}
	slices.Sort(oldIDs)
	slices.Sort(newIDs)
	if same || !slices.Equal(oldIDs, newIDs) {
		t.Fatal("cycle did not permute existing occurrences")
	}
	replay, err := s.AdvanceQueue(ctx, key(4), req)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal("cycle receipt replay changed order/token")
	}
	if _, err = s.AdvanceQueue(ctx, key(5), req); !errors.Is(err, ErrStaleSelection) {
		t.Fatalf("old decoder cycled again: %v", err)
	}
	var change QueueChange
	if err = json.Unmarshal(result.Body, &change); err != nil || change.Revision != before.Revision+1 {
		t.Fatal("cycle revision")
	}
	history, _ = s.ListHistory(ctx, 50, nil, "")
	if len(history) != 1 {
		t.Fatal("cycle final report not committed atomically")
	}
	// Subsequent rounds skip unavailable songs and never grow the queue.
	if _, err = s.pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='missing',unavailable_at=now() WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 4; round++ {
		q, _ = s.ReadQueue(ctx)
		last := q.Items[len(q.Items)-1]
		if !last.Available {
			for i := len(q.Items) - 1; i >= 0; i-- {
				if q.Items[i].Available {
					last = q.Items[i]
					break
				}
			}
		}
		if _, err = s.SelectQueueItem(ctx, key(10+round*2), QueueSelectRequest{ItemID: last.ID, ExpectedVersion: q.Revision}); err != nil {
			t.Fatal(err)
		}
		q, _ = s.ReadQueue(ctx)
		if _, err = s.AdvanceQueue(ctx, key(11+round*2), QueueAdvanceRequest{Direction: "next", Repeat: "all", Reshuffle: true, ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
			t.Fatal(err)
		}
		q, _ = s.ReadQueue(ctx)
		if len(q.Items) != 4 || q.SelectionState != "selected" {
			t.Fatal("cycle grew or stopped despite playable music")
		}
	}
	if err = s.DisableRoot(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if _, err = s.AdvanceQueue(ctx, key(30), QueueAdvanceRequest{Direction: "next", Repeat: "all", Reshuffle: true, ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if q.SelectionState != "stopped" || q.SelectionToken != nil {
		t.Fatal("all-unavailable cycle did not stop")
	}
}
