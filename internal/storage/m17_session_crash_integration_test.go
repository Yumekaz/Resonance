//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestM17NaturalEndBackendTerminationAndRetry(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "M17 session crash", `C:\m17-private-root`)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 17)
	b, _ := userFixtureTrack(t, s, root, 18)
	key := func(n int) string {
		return "00000000-0000-4000-8000-0000000000" + map[int]string{1: "01", 2: "02", 3: "03"}[n]
	}
	if _, err := s.AddQueueItem(ctx, key(1), QueueAddRequest{TrackID: a, Placement: "now", ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddQueueItem(ctx, key(2), QueueAddRequest{TrackID: b, Placement: "end", ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session := "00000000-0000-4000-8000-000000000017"
	if _, err := s.StartListeningSession(ctx, SessionStartRequest{ID: session, TrackID: a, ClientInstanceID: "00000000-0000-4000-8000-000000000018", QueueItemID: before.CurrentItemID, SelectionToken: before.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '5 seconds' WHERE id=$1", session); err != nil {
		t.Fatal(err)
	}
	duration := int64(2000)
	reason := "ended"
	request := QueueAdvanceRequest{Direction: "ended", ExpectedVersion: before.Revision, ExpectedCurrentItemID: before.CurrentItemID, SelectionToken: before.SelectionToken, SessionID: &session, FinalReport: &SessionReportRequest{Sequence: 1, ListenedMS: 2000, PositionMS: 2000, DurationMS: &duration, TerminalReason: &reason}}
	lock, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock(170911)"); err != nil {
		t.Fatal(err)
	}
	defer lock.Exec(ctx, "SELECT pg_advisory_unlock(170911)")
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION m17_receipt_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(170911); RETURN NEW; END $$; CREATE TRIGGER m17_receipt_barrier BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION m17_receipt_barrier()`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := s.AdvanceQueue(ctx, key(3), request); done <- e }()
	deadline := time.Now().Add(15 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		_ = s.pool.QueryRow(ctx, "SELECT pid FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid=170911 LIMIT 1").Scan(&pid)
		if pid != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("natural-end late receipt barrier not reached")
	}
	if _, err := s.pool.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("terminated natural end returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("terminated transaction did not return")
	}
	after, err := s.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("queue half-committed before retry")
	}
	var seq, receiptCount int
	var finalized bool
	if err := s.pool.QueryRow(ctx, "SELECT last_sequence,meaningful_at IS NOT NULL OR completed_at IS NOT NULL OR ended_at IS NOT NULL FROM playback_sessions WHERE id=$1", session).Scan(&seq, &finalized); err != nil || seq != 0 || finalized {
		t.Fatal("session half-committed", seq, finalized, err)
	}
	_ = s.pool.QueryRow(ctx, "SELECT count(*) FROM mutation_receipts WHERE idempotency_key=$1", key(3)).Scan(&receiptCount)
	if receiptCount != 0 {
		t.Fatal("receipt half-committed")
	}
	if _, err := s.pool.Exec(ctx, "DROP TRIGGER m17_receipt_barrier ON mutation_receipts; DROP FUNCTION m17_receipt_barrier()"); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_unlock(170911)"); err != nil {
		t.Fatal(err)
	}
	first, err := s.AdvanceQueue(ctx, key(3), request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.AdvanceQueue(ctx, key(3), request)
	if err != nil || !again.Replayed || string(first.Body) != string(again.Body) {
		t.Fatal("natural-end retry replay", err)
	}
	final, err := s.ReadQueue(ctx)
	if err != nil || final.Revision != before.Revision+1 || *final.CurrentItemID != before.Items[1].ID {
		t.Fatal("advance count", err)
	}
	history, err := s.ListHistory(ctx, 50, nil, "")
	if err != nil || len(history) != 1 || history[0].CompletedAt == nil {
		t.Fatal("duplicate/missing completed listen", err)
	}
	t.Log("late receipt backend killed; pre-retry queue/session/receipt rollback verified; original ended key retried and replayed once")
}
