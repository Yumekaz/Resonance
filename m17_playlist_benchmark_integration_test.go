//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestM17PlaylistSupportedCapBenchmark(t *testing.T) {
	if os.Getenv("RESONANCE_M17_PLAYLIST_CAP_BENCHMARK") != "1" {
		t.Skip("explicit 5000-entry playlist evidence run")
	}
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	dir, err := os.MkdirTemp("data/m17-closure", "playlist-cap-")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.Abs(dir)
	root, err := store.AddRoot(ctx, "Playlist benchmark", dir)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := addCatalogFixture(t, pool, root.ID, dir, "Playlist Track", "wav", testWAV(), 117)
	if err := store.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	const playlist = "pl_00000000000000000000000000000117"
	if _, err := pool.Exec(ctx, `INSERT INTO playlists(id,name) VALUES($1,'Supported cap'); INSERT INTO playlist_items(id,playlist_id,track_id,position) SELECT 'pi_'||lpad(to_hex(n),32,'0'),$1,$2,n-1 FROM generate_series(1,5000) n`, pgx.QueryExecModeSimpleProtocol, playlist, track); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newHandlerWithCatalog(filepath.Join(dir, "Artist", "Album", "Playlist Track.wav"), "Demo", io.Discard, store.Ready, store))
	defer server.Close()
	output := os.Getenv("RESONANCE_M17_ATTEMPT_OUTPUT")
	if output == "" {
		t.Fatal("attempt output required")
	}
	f, err := os.OpenFile(filepath.Join(output, "playlist-5000.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	values := []float64{}
	client := &http.Client{Timeout: 20 * time.Second}
	for n := 0; n < 30; n++ {
		detail, err := store.ReadPlaylist(ctx, playlist)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(detail.Items))
		for i, item := range detail.Items {
			ids[len(ids)-1-i] = item.ID
		}
		if len(ids) != 5000 {
			t.Fatal("wrong cap size")
		}
		body, _ := json.Marshal(map[string]any{"item_ids": ids, "expected_version": detail.Revision})
		start := time.Now().UTC()
		req, _ := http.NewRequest("PUT", server.URL+"/api/v1/playlists/"+playlist+"/order", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", fmt.Sprintf("00000000-0000-4000-8000-%012d", n+1117))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		end := time.Now().UTC()
		ms := float64(end.Sub(start).Microseconds()) / 1000
		values = append(values, ms)
		_ = json.NewEncoder(f).Encode(map[string]any{"index": n + 1, "started_utc": start, "finished_utc": end, "duration_ms": ms, "status": resp.StatusCode, "request_id": resp.Header.Get("X-Request-ID"), "request_bytes": len(body), "playlist_entries": 5000})
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("5000 reorder failed status=%d %s %v", resp.StatusCode, response, err)
		}
		after, err := store.ReadPlaylist(ctx, playlist)
		if err != nil || after.Revision != detail.Revision+1 {
			t.Fatal("version not advanced once")
		}
		for i, item := range after.Items {
			if item.ID != ids[i] || item.Position != int64(i) {
				t.Fatal("dense permutation not preserved")
			}
		}
	}
	sort.Float64s(values)
	summary, _ := json.MarshalIndent(map[string]any{"samples": 30, "playlist_entries": 5000, "dataset": "one rooted Track repeated; 5000 distinct playlist occurrence IDs", "p50_ms": (values[14] + values[15]) / 2, "p95_ms": values[28], "p99_ms": values[29], "cache": "warm/uncontrolled"}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "playlist-5000-summary.json"), summary, 0600); err != nil {
		t.Fatal(err)
	}
}
