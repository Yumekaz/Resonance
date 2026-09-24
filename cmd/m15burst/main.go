package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"time"

	"resonance/internal/storage"
)

type sample struct {
	StartedUTC  string  `json:"started_utc"`
	FinishedUTC string  `json:"finished_utc"`
	DurationMS  float64 `json:"duration_ms"`
	Revision    int64   `json:"revision"`
}

func percentile(v []float64, p float64) float64 {
	sort.Float64s(v)
	if p == .5 && len(v)%2 == 0 {
		return (v[len(v)/2-1] + v[len(v)/2]) / 2
	}
	return v[max(0, int(math.Ceil(float64(len(v))*p))-1)]
}
func main() {
	track := flag.String("track", "", "rooted Track ID")
	count := flag.Int("count", 100, "reorder count")
	items := flag.Int("items", 100, "queue size")
	ready := flag.String("ready-file", "", "ready marker")
	start := flag.String("start-file", "", "start marker")
	out := flag.String("out", "docs/benchmarks/M1-5-write-overlap.json", "output")
	flag.Parse()
	if !storage.ValidCatalogID(*track, "track") || *count < 20 || *items < 2 || *items > 1000 {
		panic("invalid benchmark parameters")
	}
	dsn := os.Getenv("RESONANCE_DATABASE_URL")
	if dsn == "" {
		panic("RESONANCE_DATABASE_URL required")
	}
	ctx := context.Background()
	s, err := storage.Open(ctx, dsn)
	if err != nil {
		panic("database configuration invalid")
	}
	defer s.Close()
	if err = s.Ready(ctx); err != nil {
		panic("catalog unavailable")
	}
	var salt [4]byte
	if _, err = rand.Read(salt[:]); err != nil {
		panic(err)
	}
	prefix := binary.BigEndian.Uint32(salt[:])
	seq := 0
	key := func() string { seq++; return fmt.Sprintf("80000000-0000-4000-8000-%08x%04x", prefix, seq) }
	q, err := s.ReadQueue(ctx)
	if err != nil {
		panic(err)
	}
	if _, err = s.ClearQueue(ctx, key(), storage.QueueClearRequest{ExpectedVersion: q.Revision}); err != nil {
		panic(err)
	}
	q, err = s.ReadQueue(ctx)
	if err != nil {
		panic(err)
	}
	for i := 0; i < *items; i++ {
		if _, err = s.AddQueueItem(ctx, key(), storage.QueueAddRequest{TrackID: *track, Placement: "end", ExpectedVersion: q.Revision}); err != nil {
			panic(err)
		}
		q.Revision++
	}
	q, err = s.ReadQueue(ctx)
	if err != nil {
		panic(err)
	}
	ids := make([]string, len(q.Items))
	for i, item := range q.Items {
		ids[i] = item.ID
	}
	if *ready != "" {
		if err = os.WriteFile(*ready, []byte("ready"), 0600); err != nil {
			panic(err)
		}
	}
	if *start != "" {
		deadline := time.Now().Add(60 * time.Second)
		for {
			if _, e := os.Stat(*start); e == nil {
				break
			}
			if time.Now().After(deadline) {
				panic("start marker timeout")
			}
			time.Sleep(time.Millisecond)
		}
	}
	samples := []sample{}
	for i := 0; i < *count; i++ {
		for a, b := 0, len(ids)-1; a < b; a, b = a+1, b-1 {
			ids[a], ids[b] = ids[b], ids[a]
		}
		begin := time.Now()
		_, e := s.ReorderQueue(ctx, key(), storage.QueueOrderRequest{ItemIDs: ids, ExpectedVersion: q.Revision})
		end := time.Now()
		if e != nil {
			panic(e)
		}
		q.Revision++
		samples = append(samples, sample{begin.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), float64(end.Sub(begin).Microseconds()) / 1000, q.Revision})
	}
	values := []float64{}
	for _, sample := range samples {
		values = append(values, sample.DurationMS)
	}
	report := map[string]any{"date_utc": time.Now().UTC().Format(time.RFC3339Nano), "operation": "queue reorder of bounded duplicate Track occurrences", "samples": samples, "queue_items": *items, "count": *count, "p50_ms": percentile(append([]float64{}, values...), .5), "p95_ms": percentile(append([]float64{}, values...), .95), "p99_ms": percentile(append([]float64{}, values...), .99), "go_version": runtime.Version(), "cache": "warm/uncontrolled", "command": "go run ./cmd/m15burst -track <track-id> -items 100 -count 100 -ready-file <path> -start-file <path> -out <path>"}
	body, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		panic(e)
	}
	if e = os.WriteFile(*out, append(body, '\n'), 0644); e != nil {
		panic(e)
	}
	fmt.Printf("queue reorder p50/p95/p99 %.3f/%.3f/%.3f ms\n", report["p50_ms"], report["p95_ms"], report["p99_ms"])
}
