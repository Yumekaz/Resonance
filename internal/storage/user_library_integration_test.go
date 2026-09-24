//go:build integration

package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func userFixtureTrack(t *testing.T, s *Store, root LibraryRoot, n int) (string, string) {
	t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("trk_%032x", n)
	obj := fmt.Sprintf("obj_%032x", n)
	loc := fmt.Sprintf("loc_%032x", n)
	title := fmt.Sprintf("Track %d", n)
	hash := sha256.Sum256([]byte(obj))
	if err := s.InsertTrack(ctx, Track{ID: id, Title: &title}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMediaObject(ctx, MediaObject{ID: obj, TrackID: id, SHA256: hash, Format: "mp3", ByteLength: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size,observed_mtime_ns) VALUES($1,$2,'private',$3,$4,100,1)`, loc, obj, root.ID, fmt.Sprintf("Album/track-%d.mp3", n)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE tracks SET metadata_source_location_id=$2 WHERE id=$1", id, loc); err != nil {
		t.Fatal(err)
	}
	return id, loc
}

func TestM15PopulatedUpgradeAndExactContract(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "M15 fixture", `C:\m15-fixture`)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := userFixtureTrack(t, s, root, 901)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 7 {
		t.Fatalf("version count %d %v", count, err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM tracks WHERE id=$1", track).Scan(&count); err != nil || count != 1 {
		t.Fatal("populated Track lost")
	}
	var revision int64
	var state string
	if err := s.pool.QueryRow(ctx, "SELECT revision,selection_state FROM active_queue WHERE singleton=true").Scan(&revision, &state); err != nil || revision != 0 || state != "stopped" {
		t.Fatalf("queue singleton %d %s %v", revision, state, err)
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM active_queue WHERE singleton=true"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Ready(ctx), ErrSchemaMismatch) {
		t.Fatal("readiness accepted a missing active queue singleton")
	}
	if _, err := s.pool.Exec(ctx, "INSERT INTO active_queue(singleton) VALUES(true)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatalf("readiness did not recover after restoring singleton: %v", err)
	}
	if _, err := s.pool.Exec(ctx, "DROP INDEX mutation_receipts_expiry_idx"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.ValidateSchema(ctx), ErrSchemaMismatch) {
		t.Fatal("0007 index drift accepted")
	}
}

func TestM15MigrationFailureRetryAndConstraintDrift(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "CREATE TABLE queue_items(blocker integer)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("conflicting 0007 DDL was accepted")
	}
	var versions int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&versions); err != nil || versions != 6 {
		t.Fatalf("migration rollback ledger=%d %v", versions, err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT to_regclass('active_queue') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatal("partial 0007 active queue DDL committed")
	}
	if _, err := s.pool.Exec(ctx, "DROP TABLE queue_items"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "ALTER TABLE active_queue DROP CONSTRAINT active_queue_selection_check, ADD CONSTRAINT active_queue_selection_check CHECK (selection_state IN ('stopped','selected'))"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.ValidateSchema(ctx), ErrSchemaMismatch) {
		t.Fatal("weakened queue selection constraint passed contract")
	}
}

func TestM15QueueReceiptsAtomicEndedAndPlaylist(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "M15", `C:\m15-tests`)
	if err != nil {
		t.Fatal(err)
	}
	a, locA := userFixtureTrack(t, s, root, 1)
	b, locB := userFixtureTrack(t, s, root, 2)
	c, _ := userFixtureTrack(t, s, root, 3)
	key := func(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }
	first, err := s.AddQueueItem(ctx, key(1), QueueAddRequest{TrackID: a, Placement: "end", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.AddQueueItem(ctx, key(1), QueueAddRequest{TrackID: a, Placement: "end", ExpectedVersion: 0})
	if err != nil || !replay.Replayed || string(replay.Body) != string(first.Body) {
		t.Fatalf("receipt replay %v %#v", err, replay)
	}
	if _, err = s.AddQueueItem(ctx, key(1), QueueAddRequest{TrackID: b, Placement: "end", ExpectedVersion: 0}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("receipt conflict %v", err)
	}
	if _, err = s.AddQueueItem(ctx, key(2), QueueAddRequest{TrackID: a, Placement: "end", ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	now, err := s.AddQueueItem(ctx, key(3), QueueAddRequest{TrackID: b, Placement: "now", ExpectedVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	var selected QueueChange
	if err = json.Unmarshal(now.Body, &selected); err != nil || selected.CurrentItemID == nil || selected.SelectionToken == nil {
		t.Fatalf("selection %#v %v", selected, err)
	}
	q, err := s.ReadQueue(ctx)
	if err != nil || len(q.Items) != 3 || q.Items[0].TrackID != b || q.Items[1].TrackID != a || q.Items[2].TrackID != a || q.Items[1].ID == q.Items[2].ID {
		t.Fatalf("queue order %#v %v", q, err)
	}
	if _, err = s.AddQueueItem(ctx, key(4), QueueAddRequest{TrackID: c, Placement: "next", ExpectedVersion: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddQueueItem(ctx, key(91), QueueAddRequest{TrackID: a, Placement: "next", ExpectedVersion: 4}); err != nil {
		t.Fatal(err)
	}
	q, err = s.ReadQueue(ctx)
	if err != nil || len(q.Items) != 5 || q.Items[1].TrackID != a || q.Items[2].TrackID != c {
		t.Fatalf("play next order %#v %v", q, err)
	}
	if _, err = s.ReorderQueue(ctx, key(5), QueueOrderRequest{ExpectedVersion: 5, ItemIDs: []string{q.Items[0].ID, q.Items[2].ID, q.Items[1].ID, q.Items[3].ID, q.Items[4].ID}}); err != nil {
		t.Fatal(err)
	}
	q, err = s.ReadQueue(ctx)
	if err != nil || *q.CurrentItemID != *selected.CurrentItemID || *q.SelectionToken != *selected.SelectionToken {
		t.Fatalf("reorder changed selection %#v %v", q, err)
	}
	resolverFailure := "resolver_failed"
	if _, err = s.AdvanceQueue(ctx, key(90), QueueAdvanceRequest{Direction: "previous", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken, FailureCode: &resolverFailure}); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("resolver failure was accepted for previous: %v", err)
	}
	if _, err = s.ReorderQueue(ctx, key(6), QueueOrderRequest{ExpectedVersion: 6, ItemIDs: []string{q.Items[0].ID, q.Items[0].ID, q.Items[2].ID, q.Items[3].ID, q.Items[4].ID}}); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("duplicate permutation %v", err)
	}
	if _, err = s.ReorderQueue(ctx, key(92), QueueOrderRequest{ExpectedVersion: 6, ItemIDs: []string{q.Items[0].ID, q.Items[1].ID, q.Items[2].ID, q.Items[3].ID}}); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("incomplete queue permutation %v", err)
	}
	if _, err = s.ReorderQueue(ctx, key(93), QueueOrderRequest{ExpectedVersion: 6, ItemIDs: []string{q.Items[4].ID, q.Items[3].ID, q.Items[2].ID, q.Items[1].ID, "qi_ffffffffffffffffffffffffffffffff"}}); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("foreign queue item in permutation %v", err)
	}
	unchanged, err := s.ReadQueue(ctx)
	if err != nil || unchanged.Revision != q.Revision || unchanged.Items[1].ID != q.Items[1].ID {
		t.Fatalf("failed queue reorder changed state: %#v %v", unchanged, err)
	}
	badItemID := "not-a-queue-item"
	if _, err = s.AdvanceQueue(ctx, key(94), QueueAdvanceRequest{Direction: "next", ExpectedVersion: q.Revision, ExpectedCurrentItemID: &badItemID, SelectionToken: q.SelectionToken}); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("malformed expected current item ID was not rejected: %v", err)
	}
	badToken := "not-a-uuid"
	if _, err = s.AdvanceQueue(ctx, key(95), QueueAdvanceRequest{Direction: "next", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: &badToken}); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("malformed selection token was not rejected: %v", err)
	}
	client := key(700)
	session := key(701)
	started, err := s.StartListeningSession(ctx, SessionStartRequest{ID: session, TrackID: b, ClientInstanceID: client, QueueItemID: q.CurrentItemID, SelectionToken: q.SelectionToken})
	if err != nil || started.EndedAt != nil {
		t.Fatalf("start %#v %v", started, err)
	}
	ended := "ended"
	duration := int64(1000)
	final := SessionReportRequest{Sequence: 1, ListenedMS: 1000, PositionMS: 1000, DurationMS: &duration, TerminalReason: &ended}
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION fail_queue_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.revision>OLD.revision THEN RAISE EXCEPTION 'injected queue failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE TRIGGER fail_queue_update BEFORE UPDATE ON active_queue FOR EACH ROW EXECUTE FUNCTION fail_queue_update()`); err != nil {
		t.Fatal(err)
	}
	req := QueueAdvanceRequest{Direction: "ended", ExpectedVersion: 6, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken, SessionID: &session, FinalReport: &final}
	if _, err := s.AdvanceQueue(ctx, key(7), req); err == nil {
		t.Fatal("injected ended advance failure succeeded")
	}
	var sequence int64
	var completed bool
	if err := s.pool.QueryRow(ctx, "SELECT last_sequence,completed_at IS NOT NULL FROM playback_sessions WHERE id=$1", session).Scan(&sequence, &completed); err != nil || sequence != 0 || completed {
		t.Fatalf("ended report committed alone: %d %v %v", sequence, completed, err)
	}
	if _, err := s.pool.Exec(ctx, "DROP TRIGGER fail_queue_update ON active_queue"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "DROP FUNCTION fail_queue_update()"); err != nil {
		t.Fatal(err)
	}
	advanced, err := s.AdvanceQueue(ctx, key(7), req)
	if err != nil {
		t.Fatal(err)
	}
	var result QueueChange
	if err = json.Unmarshal(advanced.Body, &result); err != nil || result.Revision != 7 || result.CurrentItemID == nil || *result.CurrentItemID != q.Items[1].ID {
		t.Fatalf("ended advance %#v %v", result, err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT last_sequence,completed_at IS NOT NULL FROM playback_sessions WHERE id=$1", session).Scan(&sequence, &completed); err != nil || sequence != 1 || !completed {
		t.Fatalf("ended not atomic %d %v %v", sequence, completed, err)
	}
	if again, err := s.AdvanceQueue(ctx, key(7), req); err != nil || !again.Replayed || string(again.Body) != string(advanced.Body) {
		t.Fatalf("ended replay %#v %v", again, err)
	}
	if _, err := s.AdvanceQueue(ctx, key(8), req); !errors.Is(err, ErrStaleSelection) {
		t.Fatalf("stale ended advanced twice: %v", err)
	}
	history, err := s.ListHistory(ctx, 50, nil, "")
	if err != nil || len(history) != 1 || history[0].ID != session {
		t.Fatalf("history %#v %v", history, err)
	}
	q, err = s.ReadQueue(ctx)
	if err != nil || q.CurrentItemID == nil || q.SelectionToken == nil {
		t.Fatalf("queue after known-duration end %#v %v", q, err)
	}
	unknownEndSession := key(702)
	if _, err := s.StartListeningSession(ctx, SessionStartRequest{ID: unknownEndSession, TrackID: c, ClientInstanceID: client, QueueItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '40 seconds' WHERE id=$1", unknownEndSession); err != nil {
		t.Fatal(err)
	}
	unknownEnded := "ended"
	unknownFinal := SessionReportRequest{Sequence: 1, ListenedMS: 30000, PositionMS: 45000, TerminalReason: &unknownEnded}
	unknownAdvance, err := s.AdvanceQueue(ctx, key(17), QueueAdvanceRequest{Direction: "ended", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken, SessionID: &unknownEndSession, FinalReport: &unknownFinal})
	if err != nil {
		t.Fatalf("unknown-duration natural end did not advance: %v", err)
	}
	var unknownAdvanceChange QueueChange
	if err = json.Unmarshal(unknownAdvance.Body, &unknownAdvanceChange); err != nil || unknownAdvanceChange.Revision != q.Revision+1 || unknownAdvanceChange.CurrentItemID == nil {
		t.Fatalf("unknown-duration queue transition %#v %v", unknownAdvanceChange, err)
	}
	var unknownCompleted bool
	var unknownMeaningful bool
	if err := s.pool.QueryRow(ctx, "SELECT completed_at IS NOT NULL,meaningful_at IS NOT NULL FROM playback_sessions WHERE id=$1", unknownEndSession).Scan(&unknownCompleted, &unknownMeaningful); err != nil || unknownCompleted || !unknownMeaningful {
		t.Fatalf("unknown-duration end history completion=%v meaningful=%v error=%v", unknownCompleted, unknownMeaningful, err)
	}
	if err := s.SetFavorite(ctx, a, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFavorite(ctx, a, true); err != nil {
		t.Fatal(err)
	}
	favorites, err := s.ListFavorites(ctx, 50, nil, "")
	if err != nil || len(favorites) != 1 {
		t.Fatalf("favorites %#v %v", favorites, err)
	}
	if err := s.SetFavorite(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFavorite(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreatePlaylist(ctx, key(10), PlaylistCreateRequest{Name: " Mix ", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var pl PlaylistChange
	if err = json.Unmarshal(created.Body, &pl); err != nil {
		t.Fatal(err)
	}
	add1, err := s.AddPlaylistItem(ctx, key(11), PlaylistAddRequest{PlaylistID: pl.ID, TrackID: a, ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var pi1 PlaylistChange
	_ = json.Unmarshal(add1.Body, &pi1)
	add2, err := s.AddPlaylistItem(ctx, key(12), PlaylistAddRequest{PlaylistID: pl.ID, TrackID: a, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	var pi2 PlaylistChange
	_ = json.Unmarshal(add2.Body, &pi2)
	detail, err := s.ReadPlaylist(ctx, pl.ID)
	if err != nil || len(detail.Items) != 2 || detail.Items[0].TrackID != a || detail.Items[1].TrackID != a {
		t.Fatalf("duplicate playlist entries %#v %v", detail, err)
	}
	if _, err = s.ReorderPlaylist(ctx, key(13), PlaylistOrderRequest{PlaylistID: pl.ID, ItemIDs: []string{*pi2.ItemID, *pi1.ItemID}, ExpectedVersion: 2}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]string{{*pi2.ItemID, *pi2.ItemID}, {*pi2.ItemID}, {*pi2.ItemID, "pi_ffffffffffffffffffffffffffffffff"}} {
		if _, err = s.ReorderPlaylist(ctx, key(15), PlaylistOrderRequest{PlaylistID: pl.ID, ItemIDs: invalid, ExpectedVersion: 3}); !errors.Is(err, ErrUserInvalid) {
			t.Fatalf("invalid playlist permutation %v: %v", invalid, err)
		}
	}
	afterInvalidOrder, err := s.ReadPlaylist(ctx, pl.ID)
	if err != nil || afterInvalidOrder.Revision != 3 || afterInvalidOrder.Items[0].ID != *pi2.ItemID || afterInvalidOrder.Items[1].ID != *pi1.ItemID {
		t.Fatalf("failed playlist reorder changed positions: %#v %v", afterInvalidOrder, err)
	}
	if _, err = s.RenamePlaylist(ctx, key(14), PlaylistRenameRequest{ID: pl.ID, Name: "Renamed", ExpectedVersion: 2}); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale playlist rename %v", err)
	}
	if err := s.DeletePlaylist(ctx, pl.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlaylist(ctx, pl.ID, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadPlaylist(ctx, pl.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatal("deleted playlist remained readable")
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM tracks WHERE id=$1", a).Scan(&sequence); err != nil || sequence != 1 {
		t.Fatal("playlist deletion removed Track")
	}
	if _, err := s.pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='not_found',unavailable_at=now() WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	q, err = s.ReadQueue(ctx)
	if err != nil || q.Items[0].Available {
		t.Fatalf("unavailable queue item missing %#v %v", q, err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE media_locations SET availability='available',unavailable_reason=NULL,unavailable_at=NULL WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	_ = locA
}

func TestM15ConcurrentReceiptsAndCompareAndSwap(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Concurrent M15", `C:\m15-concurrent`)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := userFixtureTrack(t, s, root, 991)
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION pause_playlist_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.2); RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE TRIGGER pause_playlist_insert BEFORE INSERT ON playlists FOR EACH ROW EXECUTE FUNCTION pause_playlist_insert()`); err != nil {
		t.Fatal(err)
	}
	type createResult struct {
		result MutationResult
		err    error
	}
	key := "abc00000-0000-4000-8000-000000000001"
	request := PlaylistCreateRequest{Name: "Concurrent receipt", ExpectedVersion: 0}
	start := make(chan struct{})
	results := make(chan createResult, 2)
	for _, requestKey := range []string{key, strings.ToUpper(key)} {
		go func(requestKey string) {
			<-start
			result, callErr := s.CreatePlaylist(ctx, requestKey, request)
			results <- createResult{result, callErr}
		}(requestKey)
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.result.Replayed == second.result.Replayed || string(first.result.Body) != string(second.result.Body) {
		t.Fatalf("concurrent receipt results %#v %#v", first, second)
	}
	conflictStart := make(chan struct{})
	conflictResults := make(chan createResult, 2)
	for _, conflictRequest := range []PlaylistCreateRequest{{Name: "Canonical A", ExpectedVersion: 0}, {Name: "Canonical B", ExpectedVersion: 0}} {
		go func(conflictRequest PlaylistCreateRequest) {
			<-conflictStart
			result, callErr := s.CreatePlaylist(ctx, "abc00000-0000-4000-8000-000000000002", conflictRequest)
			conflictResults <- createResult{result, callErr}
		}(conflictRequest)
	}
	close(conflictStart)
	conflictA, conflictB := <-conflictResults, <-conflictResults
	if !(conflictA.err == nil && errors.Is(conflictB.err, ErrIdempotencyConflict) || conflictB.err == nil && errors.Is(conflictA.err, ErrIdempotencyConflict)) {
		t.Fatalf("concurrent canonical conflict results %#v %#v", conflictA, conflictB)
	}
	if _, err := s.pool.Exec(ctx, "DROP TRIGGER pause_playlist_insert ON playlists"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "DROP FUNCTION pause_playlist_insert()"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM playlists").Scan(&count); err != nil || count != 2 {
		t.Fatalf("same-key concurrent create produced %d playlists: %v", count, err)
	}
	clientID := "ab000000-0000-4000-8000-000000000001"
	startBarrier := make(chan struct{})
	startResults := make(chan error, 2)
	for _, sessionID := range []string{"bc000000-0000-4000-8000-000000000001", "cd000000-0000-4000-8000-000000000001"} {
		go func(sessionID string) {
			<-startBarrier
			_, callErr := s.StartListeningSession(ctx, SessionStartRequest{ID: sessionID, TrackID: track, ClientInstanceID: strings.ToUpper(clientID)})
			startResults <- callErr
		}(sessionID)
	}
	close(startBarrier)
	if startA, startB := <-startResults, <-startResults; startA != nil || startB != nil {
		t.Fatalf("concurrent same-client session starts: %v %v", startA, startB)
	}
	var openSessions, interruptedSessions int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE ended_at IS NULL),count(*) FILTER (WHERE terminal_reason='interrupted') FROM playback_sessions WHERE client_instance_id=$1`, clientID).Scan(&openSessions, &interruptedSessions); err != nil || openSessions != 1 || interruptedSessions != 1 {
		t.Fatalf("same-client concurrent starts left %d open/%d interrupted sessions: %v", openSessions, interruptedSessions, err)
	}

	addStart := make(chan struct{})
	addResults := make(chan error, 2)
	for i := 1; i <= 2; i++ {
		requestKey := fmt.Sprintf("80000000-0000-4000-8000-%012d", i)
		go func(requestKey string) {
			<-addStart
			_, callErr := s.AddQueueItem(ctx, requestKey, QueueAddRequest{TrackID: track, Placement: "end", ExpectedVersion: 0})
			addResults <- callErr
		}(requestKey)
	}
	close(addStart)
	addA, addB := <-addResults, <-addResults
	if (addA == nil && !errors.Is(addB, ErrStaleVersion)) || (addB == nil && !errors.Is(addA, ErrStaleVersion)) {
		t.Fatalf("queue CAS results %v %v", addA, addB)
	}
	queue, err := s.ReadQueue(ctx)
	if err != nil || queue.Revision != 1 || len(queue.Items) != 1 {
		t.Fatalf("concurrent queue writes were not serialized: %#v %v", queue, err)
	}

	create, err := s.CreatePlaylist(ctx, "81000000-0000-4000-8000-000000000001", PlaylistCreateRequest{Name: "Edit delete race", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var playlist PlaylistChange
	if err = json.Unmarshal(create.Body, &playlist); err != nil {
		t.Fatal(err)
	}
	editStart := make(chan struct{})
	edited := make(chan error, 1)
	go func() {
		<-editStart
		_, editErr := s.RenamePlaylist(ctx, "81000000-0000-4000-8000-000000000002", PlaylistRenameRequest{ID: playlist.ID, Name: "Edited", ExpectedVersion: 0})
		edited <- editErr
	}()
	deleted := make(chan error, 1)
	go func() {
		<-editStart
		deleted <- s.DeletePlaylist(ctx, playlist.ID, 0)
	}()
	close(editStart)
	editErr := <-edited
	deleteErr := <-deleted
	editWon := editErr == nil && errors.Is(deleteErr, ErrStaleVersion)
	deleteWon := errors.Is(editErr, ErrUserNotFound) && deleteErr == nil
	if !editWon && !deleteWon {
		t.Fatalf("playlist edit/delete race edit=%v delete=%v", editErr, deleteErr)
	}

	secondTrack, _ := userFixtureTrack(t, s, root, 992)
	if _, err := s.AddQueueItem(ctx, "82000000-0000-4000-8000-000000000001", QueueAddRequest{TrackID: secondTrack, Placement: "end", ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadQueue(ctx)
	if err != nil || len(before.Items) != 2 {
		t.Fatalf("queue setup: %#v %v", before, err)
	}
	raceStart := make(chan struct{})
	type queueRaceResult struct{ removeErr, reorderErr error }
	raceResult := make(chan queueRaceResult, 1)
	go func() {
		<-raceStart
		_, removeErr := s.RemoveQueueItem(ctx, "82000000-0000-4000-8000-000000000002", QueueRemoveRequest{ItemID: before.Items[0].ID, ExpectedVersion: before.Revision})
		raceResult <- queueRaceResult{removeErr: removeErr}
	}()
	reordered := make(chan error, 1)
	go func() {
		<-raceStart
		_, reorderErr := s.ReorderQueue(ctx, "82000000-0000-4000-8000-000000000003", QueueOrderRequest{ItemIDs: []string{before.Items[1].ID, before.Items[0].ID}, ExpectedVersion: before.Revision})
		reordered <- reorderErr
	}()
	close(raceStart)
	removeResult := <-raceResult
	reorderErr := <-reordered
	removeWon := removeResult.removeErr == nil && errors.Is(reorderErr, ErrStaleVersion)
	reorderWon := errors.Is(removeResult.removeErr, ErrStaleVersion) && reorderErr == nil
	if !removeWon && !reorderWon {
		t.Fatalf("queue remove/reorder race remove=%v reorder=%v", removeResult.removeErr, reorderErr)
	}
	after, err := s.ReadQueue(ctx)
	if err != nil || after.Revision != before.Revision+1 || removeWon && len(after.Items) != 1 || reorderWon && (len(after.Items) != 2 || after.Items[0].ID != before.Items[1].ID) {
		t.Fatalf("queue race left an invalid order: %#v %v", after, err)
	}
}

func TestM15HistoryThresholdsReportsAndReceiptExpiry(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "History", `C:\m15-history`)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := userFixtureTrack(t, s, root, 51)
	key := func(i int) string { return fmt.Sprintf("20000000-0000-4000-8000-%012d", i) }
	client := key(1)
	shortID := key(2)
	shortStart, err := s.StartListeningSession(ctx, SessionStartRequest{ID: shortID, TrackID: track, ClientInstanceID: client})
	if err != nil {
		t.Fatal(err)
	}
	retriedStart, err := s.StartListeningSession(ctx, SessionStartRequest{ID: shortID, TrackID: track, ClientInstanceID: client})
	if err != nil || retriedStart.ID != shortStart.ID || !retriedStart.StartedAt.Equal(shortStart.StartedAt) {
		t.Fatalf("same session start was not idempotent: %#v %v", retriedStart, err)
	}
	ended := "ended"
	duration := int64(120000)
	short, err := s.ReportListeningSession(ctx, shortID, SessionReportRequest{Sequence: 1, ListenedMS: 1000, PositionMS: 120000, DurationMS: &duration, SeekCount: 1, TerminalReason: &ended})
	if err != nil || short.CompletedAt != nil || short.MeaningfulAt != nil {
		t.Fatalf("seek-to-end fabricated completion %#v %v", short, err)
	}
	replayedEndedStart, err := s.StartListeningSession(ctx, SessionStartRequest{ID: shortID, TrackID: track, ClientInstanceID: client})
	if err != nil || replayedEndedStart.EndedAt == nil || replayedEndedStart.TerminalReason == nil || *replayedEndedStart.TerminalReason != "ended" {
		t.Fatalf("replay of ended session was not stable: %#v %v", replayedEndedStart, err)
	}
	if items, err := s.ListHistory(ctx, 50, nil, ""); err != nil || len(items) != 0 {
		t.Fatalf("short history %#v %v", items, err)
	}
	fullID := key(3)
	_, err = s.StartListeningSession(ctx, SessionStartRequest{ID: fullID, TrackID: track, ClientInstanceID: client})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '40 seconds' WHERE id=$1", fullID); err != nil {
		t.Fatal(err)
	}
	progress := SessionReportRequest{Sequence: 1, ListenedMS: 30000, PositionMS: 119000, DurationMS: &duration, SeekCount: 2}
	if listening, reportErr := s.ReportListeningSession(ctx, fullID, progress); reportErr != nil || listening.MeaningfulAt == nil || listening.CompletedAt != nil {
		t.Fatalf("meaningful progress report %#v %v", listening, reportErr)
	}
	final := SessionReportRequest{Sequence: 2, ListenedMS: 31000, PositionMS: 120000, DurationMS: &duration, SeekCount: 2, TerminalReason: &ended}
	full, err := s.ReportListeningSession(ctx, fullID, final)
	if err != nil || full.CompletedAt == nil || full.MeaningfulAt == nil {
		t.Fatalf("qualifying completion %#v %v", full, err)
	}
	old, err := s.ReportListeningSession(ctx, fullID, SessionReportRequest{Sequence: 1, ListenedMS: 1, PositionMS: 1})
	if err != nil || old.Sequence != 2 || old.ListenedMS != 31000 {
		t.Fatalf("old report changed cumulative history %#v %v", old, err)
	}
	if _, err := s.ReportListeningSession(ctx, fullID, SessionReportRequest{Sequence: 3, ListenedMS: 31000, PositionMS: 120000, DurationMS: &duration}); !errors.Is(err, ErrStaleSelection) {
		t.Fatalf("terminal session accepted new report: %v", err)
	}
	unknownID := key(4)
	_, err = s.StartListeningSession(ctx, SessionStartRequest{ID: unknownID, TrackID: track, ClientInstanceID: client})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '40 seconds' WHERE id=$1", unknownID); err != nil {
		t.Fatal(err)
	}
	unknown, err := s.ReportListeningSession(ctx, unknownID, SessionReportRequest{Sequence: 1, ListenedMS: 30000, PositionMS: 45000})
	if err != nil || unknown.MeaningfulAt == nil || unknown.CompletedAt != nil {
		t.Fatalf("unknown-duration meaningful rule %#v %v", unknown, err)
	}
	items, err := s.ListHistory(ctx, 50, nil, "")
	if err != nil || len(items) != 2 {
		t.Fatalf("qualifying history %#v %v", items, err)
	}
	seekID := key(5)
	if _, err := s.StartListeningSession(ctx, SessionStartRequest{ID: seekID, TrackID: track, ClientInstanceID: client}); err != nil {
		t.Fatal(err)
	}
	var interrupted string
	if err := s.pool.QueryRow(ctx, "SELECT terminal_reason FROM playback_sessions WHERE id=$1", unknownID).Scan(&interrupted); err != nil || interrupted != "interrupted" {
		t.Fatalf("new playback did not mark the prior open session interrupted: %q %v", interrupted, err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '40 seconds' WHERE id=$1", seekID); err != nil {
		t.Fatal(err)
	}
	seekCount := int64(1)
	seekedEnd, err := s.ReportListeningSession(ctx, seekID, SessionReportRequest{Sequence: 1, ListenedMS: 30000, PositionMS: 120000, DurationMS: &duration, SeekCount: seekCount, TerminalReason: &ended})
	if err != nil || seekedEnd.MeaningfulAt == nil || seekedEnd.CompletedAt != nil {
		t.Fatalf("seek-to-end fabricated completion %#v %v", seekedEnd, err)
	}
	items, err = s.ListHistory(ctx, 50, nil, "")
	if err != nil || len(items) != 3 {
		t.Fatalf("seek-to-end history %#v %v", items, err)
	}
	keyReceipt := key(20)
	if _, err := s.AddQueueItem(ctx, keyReceipt, QueueAddRequest{TrackID: track, Placement: "end", ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE mutation_receipts SET created_at=now()-interval '8 days',expires_at=now()-interval '1 day' WHERE operation_scope='queue.add' AND idempotency_key=$1", keyReceipt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddQueueItem(ctx, keyReceipt, QueueAddRequest{TrackID: track, Placement: "end", ExpectedVersion: 1}); err != nil {
		t.Fatalf("expired receipt blocked new action: %v", err)
	}
	var receipts int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM mutation_receipts WHERE operation_scope='queue.add' AND idempotency_key=$1 AND expires_at>now()", keyReceipt).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipt cleanup failed %d %v", receipts, err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE mutation_receipts SET created_at=now()-interval '8 days',expires_at=now()-interval '1 day' WHERE operation_scope='queue.add' AND idempotency_key=$1", keyReceipt); err != nil {
		t.Fatal(err)
	}
	pruned, err := s.PruneExpiredReceipts(ctx, 1)
	if err != nil || pruned != 1 {
		t.Fatalf("scheduled receipt cleanup %d %v", pruned, err)
	}
}

func TestM15UnavailableSkipAndRestartPersistence(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Queue", `C:\m15-queue`)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 61)
	b, locB := userFixtureTrack(t, s, root, 62)
	c, _ := userFixtureTrack(t, s, root, 63)
	key := func(i int) string { return fmt.Sprintf("30000000-0000-4000-8000-%012d", i) }
	if _, err := s.AddQueueItem(ctx, key(1), QueueAddRequest{TrackID: a, Placement: "now", ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddQueueItem(ctx, key(2), QueueAddRequest{TrackID: b, Placement: "end", ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddQueueItem(ctx, key(3), QueueAddRequest{TrackID: c, Placement: "end", ExpectedVersion: 2}); err != nil {
		t.Fatal(err)
	}
	q, err := s.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='not_found',unavailable_at=now() WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	advance := QueueAdvanceRequest{Direction: "next", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}
	result, err := s.AdvanceQueue(ctx, key(4), advance)
	if err != nil {
		t.Fatal(err)
	}
	var change QueueChange
	_ = json.Unmarshal(result.Body, &change)
	if change.CurrentItemID == nil || *change.CurrentItemID != q.Items[2].ID || len(change.SkippedItemIDs) != 1 || change.SkippedItemIDs[0] != q.Items[1].ID {
		t.Fatalf("unavailable skip %#v", change)
	}
	q, err = s.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := s.AdvanceQueue(ctx, key(9), QueueAdvanceRequest{Direction: "previous", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken})
	if err != nil {
		t.Fatal(err)
	}
	var previousChange QueueChange
	_ = json.Unmarshal(previous.Body, &previousChange)
	if previousChange.CurrentItemID == nil || *previousChange.CurrentItemID != q.Items[0].ID || len(previousChange.SkippedItemIDs) != 1 || previousChange.SkippedItemIDs[0] != q.Items[1].ID {
		t.Fatalf("Previous did not skip unavailable item: %#v", previousChange)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE media_locations SET availability='available',unavailable_reason=NULL,unavailable_at=NULL WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	q, err = s.ReadQueue(ctx)
	if err != nil || !q.Items[1].Available {
		t.Fatal("reappearing Track did not regain availability")
	}
	forward, err := s.AdvanceQueue(ctx, key(10), QueueAdvanceRequest{Direction: "next", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken})
	if err != nil {
		t.Fatal(err)
	}
	var forwardChange QueueChange
	_ = json.Unmarshal(forward.Body, &forwardChange)
	if forwardChange.CurrentItemID == nil || *forwardChange.CurrentItemID != q.Items[1].ID || len(forwardChange.SkippedItemIDs) != 0 {
		t.Fatalf("reappearing Track was not selectable: %#v", forwardChange)
	}
	if err := s.SetFavorite(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreatePlaylist(ctx, key(5), PlaylistCreateRequest{Name: "Persistent", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var pl PlaylistChange
	_ = json.Unmarshal(created.Body, &pl)
	if _, err := s.AddPlaylistItem(ctx, key(6), PlaylistAddRequest{PlaylistID: pl.ID, TrackID: b, ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	sessionID := key(7)
	if _, err := s.StartListeningSession(ctx, SessionStartRequest{ID: sessionID, TrackID: b, ClientInstanceID: key(8)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '40 seconds' WHERE id=$1", sessionID); err != nil {
		t.Fatal(err)
	}
	duration := int64(60000)
	if _, err := s.ReportListeningSession(ctx, sessionID, SessionReportRequest{Sequence: 1, ListenedMS: 30000, PositionMS: 30000, DurationMS: &duration}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='not_found',unavailable_at=now() WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	cfg := s.pool.Config()
	newPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	reopened := &Store{pool: newPool}
	defer reopened.Close()
	if err := reopened.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	q, err = reopened.ReadQueue(ctx)
	if err != nil || len(q.Items) != 3 || q.Revision != 6 || q.Items[1].Available {
		t.Fatalf("queue not durable %#v %v", q, err)
	}
	detail, err := reopened.ReadPlaylist(ctx, pl.ID)
	if err != nil || len(detail.Items) != 1 || detail.Items[0].Available {
		t.Fatalf("playlist not durable %#v %v", detail, err)
	}
	favorites, err := reopened.ListFavorites(ctx, 50, nil, "")
	if err != nil || len(favorites) != 1 || favorites[0].Available {
		t.Fatalf("favorite not durable %#v %v", favorites, err)
	}
	history, err := reopened.ListHistory(ctx, 50, nil, "")
	if err != nil || len(history) != 1 || history[0].Available {
		t.Fatalf("history not durable for unavailable Track %#v %v", history, err)
	}
	if _, err := reopened.pool.Exec(ctx, "UPDATE media_locations SET availability='available',unavailable_reason=NULL,unavailable_at=NULL WHERE id=$1", locB); err != nil {
		t.Fatal(err)
	}
	q, err = reopened.ReadQueue(ctx)
	if err != nil || !q.Items[1].Available {
		t.Fatal("queue reference did not recover with Track")
	}
	detail, err = reopened.ReadPlaylist(ctx, pl.ID)
	if err != nil || !detail.Items[0].Available {
		t.Fatal("playlist reference did not recover with Track")
	}
	favorites, err = reopened.ListFavorites(ctx, 50, nil, "")
	if err != nil || !favorites[0].Available {
		t.Fatal("favorite reference did not recover with Track")
	}
	history, err = reopened.ListHistory(ctx, 50, nil, "")
	if err != nil || !history[0].Available {
		t.Fatal("history reference did not recover with Track")
	}
	current := *q.CurrentItemID
	removed, err := reopened.RemoveQueueItem(ctx, key(11), QueueRemoveRequest{ItemID: current, ExpectedVersion: q.Revision})
	if err != nil {
		t.Fatal(err)
	}
	var removedChange QueueChange
	_ = json.Unmarshal(removed.Body, &removedChange)
	if removedChange.CurrentItemID == nil || *removedChange.CurrentItemID != q.Items[2].ID || removedChange.SelectionToken == nil || *removedChange.SelectionToken == *q.SelectionToken {
		t.Fatalf("removing current item did not select its playable successor: %#v", removedChange)
	}
	afterRemove, err := reopened.ReadQueue(ctx)
	if err != nil || len(afterRemove.Items) != 2 || afterRemove.Revision != q.Revision+1 {
		t.Fatalf("queue after current removal %#v %v", afterRemove, err)
	}
	if _, err := reopened.RemoveQueueItem(ctx, key(12), QueueRemoveRequest{ItemID: afterRemove.Items[1].ID, ExpectedVersion: afterRemove.Revision}); err != nil {
		t.Fatal(err)
	}
	stopped, err := reopened.ReadQueue(ctx)
	if err != nil || stopped.SelectionState != "stopped" || stopped.CurrentItemID != nil || stopped.SelectionToken != nil {
		t.Fatalf("removing last current item did not stop cleanly: %#v %v", stopped, err)
	}
	if _, err := reopened.ClearQueue(ctx, key(13), QueueClearRequest{ExpectedVersion: stopped.Revision}); err != nil {
		t.Fatal(err)
	}
	cleared, err := reopened.ReadQueue(ctx)
	if err != nil || len(cleared.Items) != 0 || cleared.SelectionState != "stopped" {
		t.Fatalf("queue clear did not persist: %#v %v", cleared, err)
	}
}

func TestM15BoundedQueueAndPlaylist(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Bounds", `C:\m15-bounds`)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := userFixtureTrack(t, s, root, 88)
	if _, err := s.pool.Exec(ctx, `INSERT INTO queue_items(id,track_id,position) SELECT 'qi_'||lpad(g::text,32,'0'),$1,g-1 FROM generate_series(1,1000) g`, track); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddQueueItem(ctx, "90000000-0000-4000-8000-000000000001", QueueAddRequest{TrackID: track, Placement: "end", ExpectedVersion: 0}); !errors.Is(err, ErrUserLimit) {
		t.Fatalf("queue cap not enforced: %v", err)
	}
	created, err := s.CreatePlaylist(ctx, "90000000-0000-4000-8000-000000000002", PlaylistCreateRequest{Name: "Bounded", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var playlist PlaylistChange
	_ = json.Unmarshal(created.Body, &playlist)
	if _, err := s.pool.Exec(ctx, `INSERT INTO playlist_items(id,playlist_id,track_id,position) SELECT 'pi_'||lpad(g::text,32,'0'),$1,$2,g-1 FROM generate_series(1,5000) g`, playlist.ID, track); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlaylistItem(ctx, "90000000-0000-4000-8000-000000000003", PlaylistAddRequest{PlaylistID: playlist.ID, TrackID: track, ExpectedVersion: 0}); !errors.Is(err, ErrUserLimit) {
		t.Fatalf("playlist cap not enforced: %v", err)
	}
}
