//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestM2RepeatUsesAtomicReportsAndFreshSelectionGeneration(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "repeat", "C:\\repeat")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 91001)
	b, locB := userFixtureTrack(t, s, root, 91002)
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	if _, err = s.AddQueueCollection(ctx, key(1), QueueCollectionRequest{TrackIDs: []string{a, b}, Placement: "now", ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	q, _ := s.ReadQueue(ctx)
	session := key(2)
	if _, err = s.StartListeningSession(ctx, SessionStartRequest{ID: session, TrackID: a, ClientInstanceID: key(3), QueueItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	duration := int64(40000)
	// Model a completed 40-second listening interval; production rejects
	// reports that claim more listening time than the server-observed session.
	if _, err = s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '60 seconds' WHERE id=$1", session); err != nil {
		t.Fatal(err)
	}
	reason := "ended"
	report := SessionReportRequest{Sequence: 1, ListenedMS: 40000, PositionMS: 40000, DurationMS: &duration, TerminalReason: &reason}
	req := QueueAdvanceRequest{Direction: "ended", Repeat: "one", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken, SessionID: &session, FinalReport: &report}
	result, err := s.AdvanceQueue(ctx, key(4), req)
	if err != nil {
		t.Fatal(err)
	}
	var change QueueChange
	if err = json.Unmarshal(result.Body, &change); err != nil {
		t.Fatal(err)
	}
	if *change.CurrentItemID != *q.CurrentItemID || *change.SelectionToken == *q.SelectionToken {
		t.Fatal("repeat one must retain occurrence and issue fresh authority")
	}
	replay, err := s.AdvanceQueue(ctx, key(4), req)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal("repeat lost receipt replay")
	}
	if _, err = s.AdvanceQueue(ctx, key(5), req); !errors.Is(err, ErrStaleSelection) {
		t.Fatalf("old ended event repeated: %v", err)
	}
	history, err := s.ListHistory(ctx, 50, nil, "")
	if err != nil || len(history) != 1 {
		t.Fatalf("ended report not atomic: %v %v", history, err)
	}
	q, _ = s.ReadQueue(ctx)
	// Next remains a deliberate skip when Repeat one is active.
	if _, err = s.AdvanceQueue(ctx, key(6), QueueAdvanceRequest{Direction: "next", Repeat: "one", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if *q.CurrentItemID != q.Items[1].ID {
		t.Fatal("repeat one trapped manual Next")
	}
	if _, err = s.AdvanceQueue(ctx, key(7), QueueAdvanceRequest{Direction: "next", Repeat: "all", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if *q.CurrentItemID != q.Items[0].ID {
		t.Fatal("repeat all did not wrap")
	}
	if _, err = s.pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='missing',unavailable_at=now() WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	token := *q.SelectionToken
	if _, err = s.AdvanceQueue(ctx, key(8), QueueAdvanceRequest{Direction: "next", Repeat: "all", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if *q.CurrentItemID != q.Items[0].ID || *q.SelectionToken == token {
		t.Fatal("repeat all must skip unavailable and refresh sole playable occurrence")
	}
	if err = s.DisableRoot(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdvanceQueue(ctx, key(9), QueueAdvanceRequest{Direction: "next", Repeat: "all", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if q.SelectionState != "stopped" || q.SelectionToken != nil {
		t.Fatal("all-unavailable repeat did not stop")
	}
	if _, err = s.AdvanceQueue(ctx, key(10), QueueAdvanceRequest{Direction: "next", Repeat: "invalid"}); !errors.Is(err, ErrUserInvalid) {
		t.Fatal("invalid repeat accepted")
	}
}
