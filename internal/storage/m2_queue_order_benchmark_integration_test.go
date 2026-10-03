//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

func TestM2QueueOrderBenchmark(t *testing.T) {
	if os.Getenv("RESONANCE_M2_QUEUE_BENCH") != "1" {
		t.Skip("explicit bounded queue measurement only")
	}
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "queue measurement", "C:\\m2-queue-measurement")
	if err != nil {
		t.Fatal(err)
	}
	track, _ := userFixtureTrack(t, s, root, 9101)
	ids := make([]string, 999)
	for i := range ids {
		ids[i] = track
	}
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	if _, err = s.AddQueueCollection(ctx, key(1), QueueCollectionRequest{TrackIDs: ids, Placement: "now", ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	times := []float64{}
	for i := 0; i < 20; i++ {
		start := time.Now()
		added, e := s.AddQueueItem(ctx, key(i*2+2), QueueAddRequest{TrackID: track, Placement: "now", ExpectedVersion: revision})
		if e != nil {
			t.Fatal(e)
		}
		times = append(times, float64(time.Since(start).Microseconds())/1000)
		revision++
		var change QueueChange
		if e = json.Unmarshal(added.Body, &change); e != nil {
			t.Fatal(e)
		}
		if _, e = s.RemoveQueueItem(ctx, key(i*2+3), QueueRemoveRequest{ItemID: *change.ItemID, ExpectedVersion: revision}); e != nil {
			t.Fatal(e)
		}
		revision++
	}
	raw := append([]float64(nil), times...)
	sort.Float64s(times)
	t.Logf("queue_999_add_now_ms samples=%v p50=%.3f p95=%.3f p99=%.3f environment=local_PostgreSQL_17.11_Go_1.25_warm", raw, times[9], times[18], times[19])
}
