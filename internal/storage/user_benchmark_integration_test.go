//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
)

type m15Sample struct {
	Kind        string  `json:"kind"`
	StartedUTC  string  `json:"started_utc"`
	FinishedUTC string  `json:"finished_utc"`
	DurationMS  float64 `json:"duration_ms"`
}

func m15Percentile(values []float64, p float64) float64 {
	sort.Float64s(values)
	if len(values) == 0 {
		return 0
	}
	if p == .5 && len(values)%2 == 0 {
		return (values[len(values)/2-1] + values[len(values)/2]) / 2
	}
	return values[max(0, int(math.Ceil(float64(len(values))*p))-1)]
}

func TestM15BenchmarkEvidence(t *testing.T) {
	output := os.Getenv("RESONANCE_M15_BENCHMARK_OUTPUT")
	if output == "" {
		t.Skip("set RESONANCE_M15_BENCHMARK_OUTPUT for explicit evidence run")
	}
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "M15 benchmark", `C:\m15-benchmark`)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := userFixtureTrack(t, s, root, 777)
	samples := []m15Sample{}
	measure := func(kind string, fn func() error) {
		t.Helper()
		started := time.Now()
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		finished := time.Now()
		samples = append(samples, m15Sample{kind, started.UTC().Format(time.RFC3339Nano), finished.UTC().Format(time.RFC3339Nano), float64(finished.Sub(started).Microseconds()) / 1000})
	}
	key := 0
	nextKey := func() string { key++; return fmt.Sprintf("50000000-0000-4000-8000-%012d", key) }
	// Explicit 1,000-item queue workload. Duplicated Track references are legal.
	queueIDs := make([]string, 1000)
	for i := range queueIDs {
		id := fmt.Sprintf("qi_%032x", i+1)
		queueIDs[i] = id
		if _, err := s.pool.Exec(ctx, "INSERT INTO queue_items(id,track_id,position) VALUES($1,$2,$3)", id, track, i); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		measure("queue_read_1000", func() error {
			q, e := s.ReadQueue(ctx)
			if e == nil && len(q.Items) != 1000 {
				return ErrUserInvalid
			}
			return e
		})
	}
	var queueRevision int64
	for i := 0; i < 30; i++ {
		reversed := append([]string{}, queueIDs...)
		for a, b := 0, len(reversed)-1; a < b; a, b = a+1, b-1 {
			reversed[a], reversed[b] = reversed[b], reversed[a]
		}
		measure("queue_reorder_1000", func() error {
			_, e := s.ReorderQueue(ctx, nextKey(), QueueOrderRequest{ItemIDs: reversed, ExpectedVersion: queueRevision})
			return e
		})
		queueRevision++
		queueIDs = reversed
	}
	if _, err := s.ClearQueue(ctx, nextKey(), QueueClearRequest{ExpectedVersion: queueRevision}); err != nil {
		t.Fatal(err)
	}
	queueRevision++
	for i := 0; i < 100; i++ {
		var itemID string
		measure("queue_add", func() error {
			result, e := s.AddQueueItem(ctx, nextKey(), QueueAddRequest{TrackID: track, Placement: "end", ExpectedVersion: queueRevision})
			if e != nil {
				return e
			}
			var body QueueChange
			if e = json.Unmarshal(result.Body, &body); e != nil {
				return e
			}
			itemID = *body.ItemID
			return nil
		})
		queueRevision++
		measure("queue_remove", func() error {
			_, e := s.RemoveQueueItem(ctx, nextKey(), QueueRemoveRequest{ItemID: itemID, ExpectedVersion: queueRevision})
			return e
		})
		queueRevision++
	}
	if _, err := s.AddQueueItem(ctx, nextKey(), QueueAddRequest{TrackID: track, Placement: "now", ExpectedVersion: queueRevision}); err != nil {
		t.Fatal(err)
	}
	queueRevision++
	if _, err := s.AddQueueItem(ctx, nextKey(), QueueAddRequest{TrackID: track, Placement: "end", ExpectedVersion: queueRevision}); err != nil {
		t.Fatal(err)
	}
	queueRevision++
	for i := 0; i < 30; i++ {
		q, e := s.ReadQueue(ctx)
		if e != nil {
			t.Fatal(e)
		}
		direction := "next"
		if q.CurrentItemID != nil && *q.CurrentItemID == q.Items[1].ID {
			direction = "previous"
		}
		measure("queue_advance", func() error {
			_, err := s.AdvanceQueue(ctx, nextKey(), QueueAdvanceRequest{Direction: direction, ExpectedVersion: q.Revision, ExpectedCurrentItemID: q.CurrentItemID, SelectionToken: q.SelectionToken})
			return err
		})
	}
	created, err := s.CreatePlaylist(ctx, nextKey(), PlaylistCreateRequest{Name: "Benchmark", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var playlist PlaylistChange
	if err = json.Unmarshal(created.Body, &playlist); err != nil {
		t.Fatal(err)
	}
	playlistIDs := make([]string, 500)
	for i := range playlistIDs {
		id := fmt.Sprintf("pi_%032x", i+1)
		playlistIDs[i] = id
		if _, err := s.pool.Exec(ctx, "INSERT INTO playlist_items(id,playlist_id,track_id,position) VALUES($1,$2,$3,$4)", id, playlist.ID, track, i); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 30; i++ {
		reversed := append([]string{}, playlistIDs...)
		for a, b := 0, len(reversed)-1; a < b; a, b = a+1, b-1 {
			reversed[a], reversed[b] = reversed[b], reversed[a]
		}
		measure("playlist_reorder_500", func() error {
			_, e := s.ReorderPlaylist(ctx, nextKey(), PlaylistOrderRequest{PlaylistID: playlist.ID, ItemIDs: reversed, ExpectedVersion: int64(i)})
			return e
		})
		playlistIDs = reversed
	}
	playlistRevision := int64(30)
	for i := 0; i < 100; i++ {
		var itemID string
		measure("playlist_add_500", func() error {
			result, e := s.AddPlaylistItem(ctx, nextKey(), PlaylistAddRequest{PlaylistID: playlist.ID, TrackID: track, ExpectedVersion: playlistRevision})
			if e != nil {
				return e
			}
			var body PlaylistChange
			if e = json.Unmarshal(result.Body, &body); e != nil {
				return e
			}
			itemID = *body.ItemID
			return nil
		})
		playlistRevision++
		measure("playlist_remove_501", func() error {
			_, e := s.RemovePlaylistItem(ctx, nextKey(), PlaylistRemoveRequest{PlaylistID: playlist.ID, ItemID: itemID, ExpectedVersion: playlistRevision})
			return e
		})
		playlistRevision++
	}
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("Benchmark %d", i)
		measure("playlist_rename_500", func() error {
			_, e := s.RenamePlaylist(ctx, nextKey(), PlaylistRenameRequest{ID: playlist.ID, Name: name, ExpectedVersion: playlistRevision})
			return e
		})
		playlistRevision++
	}
	for i := 0; i < 30; i++ {
		var createdID string
		measure("playlist_create", func() error {
			result, e := s.CreatePlaylist(ctx, nextKey(), PlaylistCreateRequest{Name: fmt.Sprintf("Temporary %d", i), ExpectedVersion: 0})
			if e != nil {
				return e
			}
			var body PlaylistChange
			if e = json.Unmarshal(result.Body, &body); e != nil {
				return e
			}
			createdID = body.ID
			return nil
		})
		measure("playlist_delete", func() error { return s.DeletePlaylist(ctx, createdID, 0) })
	}
	for i := 0; i < 100; i++ {
		measure("favorite_put", func() error { return s.SetFavorite(ctx, track, true) })
		measure("favorite_delete", func() error { return s.SetFavorite(ctx, track, false) })
	}
	for i := 0; i < 30; i++ {
		sessionID := fmt.Sprintf("60000000-0000-4000-8000-%012d", i+1)
		clientID := fmt.Sprintf("70000000-0000-4000-8000-%012d", i+1)
		measure("history_start", func() error {
			_, e := s.StartListeningSession(ctx, SessionStartRequest{ID: sessionID, TrackID: track, ClientInstanceID: clientID})
			return e
		})
		duration := int64(1000)
		measure("history_report", func() error {
			_, e := s.ReportListeningSession(ctx, sessionID, SessionReportRequest{Sequence: 1, ListenedMS: 500, PositionMS: 500, DurationMS: &duration})
			return e
		})
		ended := "ended"
		measure("history_finalize", func() error {
			_, e := s.ReportListeningSession(ctx, sessionID, SessionReportRequest{Sequence: 2, ListenedMS: 1000, PositionMS: 1000, DurationMS: &duration, TerminalReason: &ended})
			return e
		})
	}
	kinds := map[string][]float64{}
	for _, sample := range samples {
		kinds[sample.Kind] = append(kinds[sample.Kind], sample.DurationMS)
	}
	summary := map[string]map[string]any{}
	for kind, values := range kinds {
		summary[kind] = map[string]any{"count": len(values), "p50_ms": m15Percentile(append([]float64{}, values...), .5), "p95_ms": m15Percentile(append([]float64{}, values...), .95), "p99_ms": m15Percentile(append([]float64{}, values...), .99)}
	}
	report := map[string]any{"date_utc": time.Now().UTC().Format(time.RFC3339Nano), "machine_os": runtime.GOOS, "machine_arch": runtime.GOARCH, "go_version": runtime.Version(), "database": "PostgreSQL 17.11 local test cluster, 128 MiB shared_buffers, fsync on", "dataset": "one rooted Track; 1,000 queue occurrences; 500 playlist entries; 30 listening sessions", "command": "go test -p 1 -tags=integration -run TestM15BenchmarkEvidence -count=1 ./internal/storage", "cache": "warm, not reset", "concurrency": 1, "summary": summary, "samples": samples}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, append(body, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
