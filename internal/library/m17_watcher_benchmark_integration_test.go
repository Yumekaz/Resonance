//go:build integration

package library

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestM17ThirtyRealMediaWatcherChanges(t *testing.T) {
	if os.Getenv("RESONANCE_M17_MEDIA_WATCH_BENCHMARK") != "1" {
		t.Skip("explicit 30-media-event watcher campaign")
	}
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	root := addRoot(t, s, dir, "M17 real media events")
	file := filepath.Join(dir, "Artist", "Album", "media.mp3")
	writeFile(t, file, fixtureMP3WithTitle(t, "Baseline"))
	if _, err := testScanner(s).Scan(context.Background(), root.ID); err != nil {
		t.Fatal(err)
	}
	coordinator, stop, done := startIntegrationCoordinator(t, s)
	defer func() { stop(); <-done }()
	waitForCoordinator(t, 15*time.Second, func() bool {
		st := coordinator.Status()
		return st.ScansSucceeded > 0 && len(st.Roots) == 1 && !st.Roots[0].Dirty && !st.GlobalSweep
	})
	samples := []map[string]any{}
	values := []float64{}
	output := os.Getenv("RESONANCE_M17_ATTEMPT_OUTPUT")
	if output == "" {
		t.Fatal("attempt output required")
	}
	raw, err := os.OpenFile(filepath.Join(output, "media-events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	encoder := json.NewEncoder(raw)
	var previous string
	_ = pool.QueryRow(context.Background(), "SELECT last_successful_scan_id::text FROM library_roots WHERE id=$1", root.ID).Scan(&previous)
	for n := 1; n <= 30; n++ {
		title := fmt.Sprintf("M17 Real Media %02d", n)
		data := fixtureMP3WithTitle(t, title)
		started := time.Now()
		_ = encoder.Encode(map[string]any{"event": "started", "index": n, "started_utc": started.UTC()})
		writeFile(t, file, data)
		future := time.Now().Add(time.Duration(n) * time.Second)
		if err := os.Chtimes(file, future, future); err != nil {
			t.Fatal(err)
		}
		var run string
		var hashes, parses int
		var authority bool
		m17Wait(t, func() bool {
			err := pool.QueryRow(context.Background(), `SELECT r.id::text,r.files_hashed,r.metadata_extractions,r.absence_reconciled FROM library_roots lr JOIN scan_runs r ON r.id=lr.last_successful_scan_id WHERE lr.id=$1 AND r.id::text<>$2 AND EXISTS(SELECT 1 FROM tracks t JOIN media_objects mo ON mo.track_id=t.id JOIN media_locations ml ON ml.media_object_id=mo.id WHERE ml.root_id=$1 AND ml.availability='available' AND t.title=$3) ORDER BY r.started_at DESC LIMIT 1`, root.ID, previous, title).Scan(&run, &hashes, &parses, &authority)
			return err == nil && hashes == 1 && parses == 1 && authority
		})
		finished := time.Now()
		latency := float64(finished.Sub(started).Microseconds()) / 1000
		values = append(values, latency)
		samples = append(samples, map[string]any{"index": n, "started_utc": started.UTC(), "finished_utc": finished.UTC(), "latency_ms": latency, "run_id": run, "files_hashed": hashes, "metadata_extractions": parses, "absence_reconciled": authority, "expected_catalog_title_observed": true})
		_ = encoder.Encode(samples[len(samples)-1])
		previous = run
		waitForCoordinator(t, 15*time.Second, func() bool {
			st := coordinator.Status()
			return len(st.Roots) == 1 && !st.Roots[0].Dirty && !st.GlobalSweep
		})
	}
	sort.Float64s(values)
	body, _ := json.MarshalIndent(map[string]any{"dataset": "one complete tagged CC0 MP3; 30 distinct title retags; real fsnotify adapter and default coordinator; matching authoritative publication plus expected catalog state", "samples": samples, "p50_ms": (values[14] + values[15]) / 2, "p95_ms": values[28], "p99_ms": values[29], "cache": "warm/uncontrolled"}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "media-watcher.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}
