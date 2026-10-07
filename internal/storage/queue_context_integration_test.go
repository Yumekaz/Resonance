//go:build integration

package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sourceTimingTracer struct{ t *testing.T }
type sourceTimingKey struct{}
type sourceTimingValue struct {
	started time.Time
	sql     string
}

func (trace sourceTimingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, sourceTimingKey{}, sourceTimingValue{time.Now(), data.SQL})
}
func (trace sourceTimingTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	value := ctx.Value(sourceTimingKey{}).(sourceTimingValue)
	elapsed := time.Since(value.started)
	if elapsed > 10*time.Millisecond {
		label := strings.Join(strings.Fields(value.sql), " ")
		if len(label) > 140 {
			label = label[:140]
		}
		trace.t.Logf("source query %s: %s", elapsed, label)
	}
}

func TestM2TenThousandSourceSnapshotAndShuffleRemainBounded(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if os.Getenv("RESONANCE_SOURCE_PROFILE") == "1" {
		cfg := s.pool.Config()
		cfg.ConnConfig.Tracer = sourceTimingTracer{t}
		profiled, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer profiled.Close()
		s = &Store{pool: profiled}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Scale source", "C:\\scale-source")
	if err != nil {
		t.Fatal(err)
	}
	const count = 10000
	ids, titles, objects, locations := make([]string, count), make([]string, count), make([]string, count), make([]string, count)
	hashes := make([][]byte, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("trk_%032x", 100000+i)
		titles[i] = fmt.Sprintf("Source %05d", i)
		objects[i] = fmt.Sprintf("obj_%032x", 100000+i)
		locations[i] = fmt.Sprintf("loc_%032x", 100000+i)
		hash := sha256.Sum256([]byte(objects[i]))
		hashes[i] = hash[:]
	}
	// This measures the real storage projection only, not decoder/audio performance.
	if _, err = s.pool.Exec(ctx, `INSERT INTO tracks(id,title,catalog_title_key) SELECT id,title,title FROM unnest($1::text[],$2::text[]) AS data(id,title)`, ids, titles); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO media_objects(id,track_id,sha256,format,byte_length) SELECT id,track,hash,'mp3',100 FROM unnest($1::text[],$2::text[],$3::bytea[]) AS data(id,track,hash)`, objects, ids, hashes); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size,observed_mtime_ns) SELECT id,obj,'private',$3,id,100,1 FROM unnest($1::text[],$2::text[]) AS data(id,obj)`, locations, objects, root.ID); err != nil {
		t.Fatal(err)
	}
	key := func(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }
	started := time.Now()
	if _, err = s.StartQueueContext(ctx, key(1), ContextStartRequest{Source: PlaybackSource{Kind: "library", Order: "title"}, StartTrackID: ids[5000]}); err != nil {
		t.Fatal(err)
	}
	q, err := s.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("10000-reference source startup: %s; queue rows: %d", time.Since(started), len(q.Items))
	if q.Context.Total != count || len(q.Items) != contextWindow || q.Items[0].TrackID != ids[5000] {
		t.Fatal("scale source lost chosen track or bounds")
	}
	before := q
	if _, err = s.AddQueueItem(ctx, key(2), QueueAddRequest{TrackID: ids[9999], Placement: "next", ExpectedVersion: q.Revision}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	manual := q.Items[1].ID
	if _, err = s.ShuffleQueueContext(ctx, key(3), ContextShuffleRequest{Shuffle: true, ExpectedVersion: q.Revision}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if *q.CurrentItemID != *before.CurrentItemID || *q.SelectionToken != *before.SelectionToken || q.Items[1].ID != manual || !q.Context.Shuffled {
		t.Fatal("source shuffle changed current authority or manual slot")
	}
	if _, err = s.ShuffleQueueContext(ctx, key(4), ContextShuffleRequest{Shuffle: false, ExpectedVersion: q.Revision}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if q.Items[1].ID != manual || q.Items[2].TrackID != ids[0] || q.Context.Shuffled {
		t.Fatal("unshuffle failed to restore remaining source order")
	}
	// A new Store reads the same context: no browser cursor supplies durability.
	freshPool, err := pgxpool.NewWithConfig(ctx, s.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer freshPool.Close()
	fresh := &Store{pool: freshPool}
	reread, err := fresh.ReadQueue(ctx)
	if err != nil || reread.Context.ID != q.Context.ID {
		t.Fatal("source not persistent")
	}
	if os.Getenv("RESONANCE_SOURCE_BENCHMARK") == "1" {
		samples := make([]float64, 20)
		for n := range samples {
			before, _ := s.ReadQueue(ctx)
			clock := time.Now()
			if _, err = s.StartQueueContext(ctx, key(100+n), ContextStartRequest{Source: PlaybackSource{Kind: "library", Order: "title"}, StartTrackID: ids[5000], ExpectedVersion: before.Revision}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ReadQueue(ctx); err != nil {
				t.Fatal(err)
			}
			samples[n] = float64(time.Since(clock).Microseconds()) / 1000
		}
		ordered := slices.Clone(samples)
		slices.Sort(ordered)
		encoded, _ := json.Marshal(map[string]any{"samples_ms": samples, "p50_ms": ordered[9], "p95_ms": ordered[18], "p99_ms": ordered[19], "tracks": count, "queue_rows": contextWindow, "cache": "warm local database; no ANALYZE forced; projection only"})
		t.Logf("source benchmark %s", encoded)
	}
}

func TestM2SourceRefillAndNaturalReportRollBackTogether(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Report source", "C:\\report-source")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 160)
	for i := range ids {
		ids[i], _ = userFixtureTrack(t, s, root, 80000+i)
	}
	key := func(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }
	if _, err = s.StartQueueContext(ctx, key(1), ContextStartRequest{Source: PlaybackSource{Kind: "selection", Order: "original", TrackIDs: ids}}); err != nil {
		t.Fatal(err)
	}
	q, _ := s.ReadQueue(ctx)
	if _, err = s.SelectQueueItem(ctx, key(2), QueueSelectRequest{ItemID: q.Items[len(q.Items)-1].ID, ExpectedVersion: q.Revision}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	current := q.Items[currentIndex(q.Items, q.CurrentItemID)]
	session := key(3)
	if _, err = s.StartListeningSession(ctx, SessionStartRequest{ID: session, TrackID: current.TrackID, ClientInstanceID: key(4), QueueItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '60 seconds' WHERE id=$1", session); err != nil {
		t.Fatal(err)
	}
	duration := int64(40000)
	reason := "ended"
	req := QueueAdvanceRequest{Direction: "ended", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken, SessionID: &session, FinalReport: &SessionReportRequest{Sequence: 1, ListenedMS: duration, PositionMS: duration, DurationMS: &duration, TerminalReason: &reason}}
	if _, err = s.pool.Exec(ctx, `CREATE FUNCTION context_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'source refill fault';END $$;CREATE TRIGGER context_fault BEFORE INSERT ON queue_context_items FOR EACH ROW EXECUTE FUNCTION context_fault()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdvanceQueue(ctx, key(5), req); err == nil {
		t.Fatal("injected source fault committed")
	}
	after, _ := s.ReadQueue(ctx)
	if after.Revision != q.Revision || *after.SelectionToken != *q.SelectionToken || len(after.Items) != len(q.Items) {
		t.Fatal("partial source advance")
	}
	history, _ := s.ListHistory(ctx, 50, nil, "")
	if len(history) != 0 {
		t.Fatal("source failure finalized report")
	}
	if _, err = s.pool.Exec(ctx, `DROP TRIGGER context_fault ON queue_context_items;DROP FUNCTION context_fault()`); err != nil {
		t.Fatal(err)
	}
	result, err := s.AdvanceQueue(ctx, key(5), req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.AdvanceQueue(ctx, key(5), req)
	if err != nil || !replay.Replayed || string(result.Body) != string(replay.Body) {
		t.Fatal("source final-report replay changed transition")
	}
	history, _ = s.ListHistory(ctx, 50, nil, "")
	if len(history) != 1 {
		t.Fatal("source final report absent")
	}
}

func TestM2DurableSourceCrossesCatalogPagesWithBoundedQueue(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Source fixture", "C:\\source-fixture")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 350)
	for i := range ids {
		ids[i], _ = userFixtureTrack(t, s, root, 93000+i)
	}
	key := func(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }
	req := ContextStartRequest{Source: PlaybackSource{Kind: "library", Order: "title"}, StartTrackID: ids[0], ExpectedVersion: 0}
	result, err := s.StartQueueContext(ctx, key(1), req)
	if err != nil {
		t.Fatal(err)
	}
	var started QueueChange
	if err = json.Unmarshal(result.Body, &started); err != nil {
		t.Fatal(err)
	}
	q, err := s.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q.Context == nil || q.Context.Total != 350 || !q.Context.More || len(q.Items) != contextWindow || q.Items[0].TrackID != ids[0] {
		t.Fatalf("bad source start: %#v", q.Context)
	}
	replay, err := s.StartQueueContext(ctx, key(1), req)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal("source start lost replay")
	}
	seen := map[string]bool{ids[0]: true}
	for n := 0; n < 349; n++ {
		before := q
		request := QueueAdvanceRequest{Direction: "next", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}
		change, err := s.AdvanceQueue(ctx, key(10+n), request)
		if err != nil {
			t.Fatalf("advance %d: %v", n, err)
		}
		q, err = s.ReadQueue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		at := currentIndex(q.Items, q.CurrentItemID)
		if at < 0 {
			t.Fatal("stopped before end of source")
		}
		if seen[q.Items[at].TrackID] {
			t.Fatalf("repeated before full source completed at %d: %s (index %d, queue %d)", n, q.Items[at].TrackID, at, len(q.Items))
		}
		seen[q.Items[at].TrackID] = true
		if len(q.Items) > contextWindow+contextHistory+1 {
			t.Fatal("rolling queue grew unbounded")
		}
		again, err := s.AdvanceQueue(ctx, key(10+n), request)
		if err != nil || !again.Replayed || string(again.Body) != string(change.Body) {
			t.Fatal("refill replay changed selection")
		}
		if _, err = s.AdvanceQueue(ctx, key(9000+n), QueueAdvanceRequest{Direction: "next", ExpectedVersion: q.Revision, ExpectedCurrentItemID: before.CurrentItemID, SelectionToken: before.SelectionToken}); !errors.Is(err, ErrStaleSelection) {
			t.Fatal("old decoder advanced refilled source")
		}
	}
	if len(seen) != 350 {
		t.Fatal("did not traverse complete source")
	}
	if _, err = s.AdvanceQueue(ctx, key(7900), QueueAdvanceRequest{Direction: "next", Repeat: "all", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if q.Context.Round != 1 || q.Items[currentIndex(q.Items, q.CurrentItemID)].TrackID != ids[0] {
		t.Fatal("continuous playback cycled the window instead of the whole source")
	}
	// Previous crosses the retained window's first edge using source order.
	first := q.Items[0].ID
	if _, err = s.SelectQueueItem(ctx, key(8000), QueueSelectRequest{ItemID: first, ExpectedVersion: q.Revision}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if _, err = s.AdvanceQueue(ctx, key(8001), QueueAdvanceRequest{Direction: "previous", ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if currentIndex(q.Items, q.CurrentItemID) < 0 {
		t.Fatal("previous source edge stopped")
	}
}

func TestM2SourceResolverFailuresStopWithoutAnInfiniteRetryCycle(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Failed source", `C:\failed-source`)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 87001)
	b, _ := userFixtureTrack(t, s, root, 87002)
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	if _, err = s.StartQueueContext(ctx, key(1), ContextStartRequest{Source: PlaybackSource{Kind: "selection", Order: "original", TrackIDs: []string{a, b, a}}}); err != nil {
		t.Fatal(err)
	}
	failure := "resolver_failed"
	for n := 0; n < 2; n++ {
		q, _ := s.ReadQueue(ctx)
		if _, err = s.AdvanceQueue(ctx, key(2+n), QueueAdvanceRequest{Direction: "next", Repeat: "all", FailureCode: &failure, ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken}); err != nil {
			t.Fatal(err)
		}
	}
	q, _ := s.ReadQueue(ctx)
	if q.SelectionState != "stopped" || q.SelectionToken != nil || q.Context.More {
		t.Fatal("unplayable source kept retrying", q)
	}
}

func TestM2SourcePreviousAtCapacityPreservesSelectionAndReportsLimit(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Capacity source", `C:\capacity-source`)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 87501)
	b, _ := userFixtureTrack(t, s, root, 87502)
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	if _, err = s.StartQueueContext(ctx, key(1), ContextStartRequest{Source: PlaybackSource{Kind: "selection", Order: "original", TrackIDs: []string{a, b}}, StartTrackID: b}); err != nil {
		t.Fatal(err)
	}
	q, _ := s.ReadQueue(ctx)
	tracks := make([]string, 999)
	for n := range tracks {
		tracks[n] = a
	}
	if _, err = s.AddQueueCollection(ctx, key(2), QueueCollectionRequest{TrackIDs: tracks, Placement: "end", ExpectedVersion: q.Revision}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ReadQueue(ctx)
	if _, err = s.AdvanceQueue(ctx, key(3), QueueAdvanceRequest{Direction: "previous", Repeat: "all", ExpectedVersion: before.Revision, ExpectedCurrentItemID: before.CurrentItemID, SelectionToken: before.SelectionToken}); !errors.Is(err, ErrUserLimit) {
		t.Fatal("full source backfill did not report limit", err)
	}
	after, _ := s.ReadQueue(ctx)
	if before.Revision != after.Revision || *before.SelectionToken != *after.SelectionToken || len(after.Items) != 1000 {
		t.Fatal("capacity failure changed playback authority")
	}
}

func TestM2SourceLookupCancellationDoesNotBecomeNotFound(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"artist", "album"} {
		t.Run(kind, func(t *testing.T) {
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = snapshotPlaybackSource(cancelled, tx, ContextStartRequest{Source: PlaybackSource{Kind: kind, ID: "missing"}})
			if errors.Is(err, ErrUserNotFound) || !errors.Is(err, context.Canceled) {
				t.Fatal("source lookup concealed cancellation as missing collection", err)
			}
		})
	}
}
