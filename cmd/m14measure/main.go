package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"runtime"
	"sort"
	"time"

	"resonance/internal/storage"
)

type sample struct {
	Kind        string  `json:"kind"`
	StartedUTC  string  `json:"started_utc"`
	FinishedUTC string  `json:"finished_utc"`
	DurationMS  float64 `json:"duration_ms"`
	Status      int     `json:"status,omitempty"`
	RequestID   string  `json:"request_id,omitempty"`
}

func percentile(values []float64, p float64) float64 {
	sort.Float64s(values)
	if len(values) == 0 {
		return 0
	}
	if p == .5 && len(values)%2 == 0 {
		return (values[len(values)/2-1] + values[len(values)/2]) / 2
	}
	return values[max(0, int(math.Ceil(float64(len(values))*p))-1)]
}

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "server base URL")
	trackID := flag.String("track", "", "catalog Track ID")
	count := flag.Int("count", 100, "measurements per operation")
	out := flag.String("out", "docs/benchmarks/M1-4-queries.json", "raw output")
	flag.Parse()
	if !storage.ValidCatalogID(*trackID, "track") || *count < 20 {
		panic("valid Track ID and at least 20 measurements required")
	}
	dsn := os.Getenv("RESONANCE_DATABASE_URL")
	if dsn == "" {
		panic("RESONANCE_DATABASE_URL required")
	}
	ctx := context.Background()
	store, err := storage.Open(ctx, dsn)
	if err != nil {
		panic("database configuration invalid")
	}
	defer store.Close()
	if err := store.Ready(ctx); err != nil {
		panic("catalog unavailable")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	operations := []struct{ kind, url string }{{"tracks_page", *base + "/api/v1/tracks?limit=50"}, {"artists_page", *base + "/api/v1/artists?limit=50"}, {"albums_page", *base + "/api/v1/albums?limit=50"}, {"track_detail", *base + "/api/v1/tracks/" + *trackID}}
	samples := []sample{}
	measure := func(kind string, run func() (int, string, error)) {
		for i := 0; i < *count; i++ {
			start := time.Now()
			status, id, err := run()
			end := time.Now()
			if err != nil {
				panic(fmt.Sprintf("%s failed: %v", kind, err))
			}
			samples = append(samples, sample{kind, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), float64(end.Sub(start).Microseconds()) / 1000, status, id})
		}
	}
	for _, op := range operations {
		op := op
		measure(op.kind, func() (int, string, error) {
			response, err := client.Get(op.url)
			if err != nil {
				return 0, "", err
			}
			defer response.Body.Close()
			_, err = io.Copy(io.Discard, response.Body)
			if err != nil {
				return 0, "", err
			}
			if response.StatusCode != 200 {
				return response.StatusCode, "", fmt.Errorf("HTTP %d", response.StatusCode)
			}
			return response.StatusCode, response.Header.Get("X-Request-ID"), nil
		})
	}
	measure("track_resolution", func() (int, string, error) {
		candidates, err := store.PlaybackCandidates(ctx, *trackID)
		if err != nil {
			return 0, "", err
		}
		if len(candidates) == 0 {
			return 0, "", fmt.Errorf("no available location")
		}
		return 0, "", nil
	})
	summary := map[string]map[string]float64{}
	for _, op := range append(operations, struct{ kind, url string }{"track_resolution", ""}) {
		values := []float64{}
		for _, s := range samples {
			if s.Kind == op.kind {
				values = append(values, s.DurationMS)
			}
		}
		summary[op.kind] = map[string]float64{"p50_ms": percentile(append([]float64{}, values...), .5), "p95_ms": percentile(append([]float64{}, values...), .95), "p99_ms": percentile(append([]float64{}, values...), .99)}
	}
	report := map[string]any{"date_utc": time.Now().UTC().Format(time.RFC3339Nano), "go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "count_per_operation": *count, "concurrency": 1, "cache_state": "uncontrolled OS and PostgreSQL cache; sequential warm requests", "summary": summary, "samples": samples}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(*out, append(body, '\n'), 0644); err != nil {
		panic(err)
	}
	for kind, stats := range summary {
		fmt.Printf("%s p50/p95/p99 %.3f/%.3f/%.3f ms\n", kind, stats["p50_ms"], stats["p95_ms"], stats["p99_ms"])
	}
}
