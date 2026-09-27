//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"resonance/internal/storage"
)

type m16HTTPBenchmarkSample struct {
	Index              int     `json:"index"`
	RangeStartedUTC    string  `json:"range_started_utc"`
	RangeFinishedUTC   string  `json:"range_finished_utc"`
	RangeLatencyMS     float64 `json:"range_latency_ms"`
	RangeStatus        int     `json:"range_status"`
	WriteStartedUTC    string  `json:"write_started_utc"`
	WriteFinishedUTC   string  `json:"write_finished_utc"`
	WriteLatencyMS     float64 `json:"write_latency_ms"`
	WriteStatus        int     `json:"write_status"`
	RequestsOverlapped bool    `json:"requests_overlapped"`
}

func TestM16BenchmarkIndexedRangeDuringUserLibraryWrite(t *testing.T) {
	output := os.Getenv("RESONANCE_M16_RANGE_BENCHMARK_OUTPUT")
	if output == "" {
		t.Skip("set RESONANCE_M16_RANGE_BENCHMARK_OUTPUT to write Range/write overlap evidence")
	}
	if !filepath.IsAbs(output) {
		t.Fatal("range benchmark output path must be absolute")
	}
	if _, err := os.Stat(output); err == nil {
		t.Fatalf("refusing to overwrite benchmark output %q", filepath.Base(output))
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	rootPath := catalogWorkspaceTempDir(t)
	root, err := store.AddRoot(ctx, "M1.6 Range benchmark", rootPath)
	if err != nil {
		t.Fatal(err)
	}
	trackID, _ := addCatalogFixture(t, pool, root.ID, rootPath, "Range Benchmark", "wav", testWAV(), 9901)
	if err := store.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	queue, err := store.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const queueSize = 1000
	for i := 0; i < queueSize; i++ {
		key := fmt.Sprintf("90000000-0000-4000-8000-%012d", i+1)
		if _, err := store.AddQueueItem(ctx, key, storage.QueueAddRequest{TrackID: trackID, Placement: "end", ExpectedVersion: queue.Revision}); err != nil {
			t.Fatal(err)
		}
		queue.Revision++
	}
	queue, err = store.ReadQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(queue.Items))
	for i, item := range queue.Items {
		ids[i] = item.ID
	}
	server := httptest.NewServer(newHandlerWithCatalog(filepath.Join(rootPath, "Artist", "Album", "Range Benchmark.wav"), "Demo", io.Discard, store.Ready, store))
	defer server.Close()
	client := &http.Client{Timeout: 15 * time.Second}
	const sampleCount = 30
	samples := make([]m16HTTPBenchmarkSample, 0, sampleCount)
	rangeValues := make([]float64, 0, sampleCount)
	writeValues := make([]float64, 0, sampleCount)
	for i := 0; i < sampleCount; i++ {
		for left, right := 0, len(ids)-1; left < right; left, right = left+1, right-1 {
			ids[left], ids[right] = ids[right], ids[left]
		}
		requestBody, err := json.Marshal(storage.QueueOrderRequest{ItemIDs: ids, ExpectedVersion: queue.Revision})
		if err != nil {
			t.Fatal(err)
		}
		writeRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/queue/order", bytes.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		writeRequest.Header.Set("Content-Type", "application/json")
		writeRequest.Header.Set("Idempotency-Key", fmt.Sprintf("a0000000-0000-4000-8000-%012d", i+1))
		rangeRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/tracks/"+trackID+"/stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		rangeRequest.Header.Set("Range", "bytes=0-4095")
		gate := make(chan struct{})
		type responseTiming struct {
			started, finished time.Time
			status            int
			err               error
		}
		writeDone := make(chan responseTiming, 1)
		rangeDone := make(chan responseTiming, 1)
		go func() {
			<-gate
			started := time.Now()
			response, err := client.Do(writeRequest)
			if err != nil {
				writeDone <- responseTiming{started: started, finished: time.Now(), err: err}
				return
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			writeDone <- responseTiming{started: started, finished: time.Now(), status: response.StatusCode, err: readErr}
		}()
		go func() {
			<-gate
			started := time.Now()
			response, err := client.Do(rangeRequest)
			if err != nil {
				rangeDone <- responseTiming{started: started, finished: time.Now(), err: err}
				return
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			rangeDone <- responseTiming{started: started, finished: time.Now(), status: response.StatusCode, err: readErr}
		}()
		close(gate)
		writeTiming := <-writeDone
		rangeTiming := <-rangeDone
		if writeTiming.err != nil || rangeTiming.err != nil || writeTiming.status != http.StatusOK || rangeTiming.status != http.StatusPartialContent {
			t.Fatalf("concurrent request failed: write=%d/%v range=%d/%v", writeTiming.status, writeTiming.err, rangeTiming.status, rangeTiming.err)
		}
		overlapStart := writeTiming.started
		if rangeTiming.started.After(overlapStart) {
			overlapStart = rangeTiming.started
		}
		overlapEnd := minTime(writeTiming.finished, rangeTiming.finished)
		overlapped := overlapEnd.After(overlapStart)
		if !overlapped {
			t.Fatalf("request pair %d did not overlap at request level", i+1)
		}
		writeMS := float64(writeTiming.finished.Sub(writeTiming.started).Microseconds()) / 1000
		rangeMS := float64(rangeTiming.finished.Sub(rangeTiming.started).Microseconds()) / 1000
		writeValues = append(writeValues, writeMS)
		rangeValues = append(rangeValues, rangeMS)
		samples = append(samples, m16HTTPBenchmarkSample{
			Index: i + 1, RangeStartedUTC: rangeTiming.started.UTC().Format(time.RFC3339Nano), RangeFinishedUTC: rangeTiming.finished.UTC().Format(time.RFC3339Nano),
			RangeLatencyMS: rangeMS, RangeStatus: rangeTiming.status, WriteStartedUTC: writeTiming.started.UTC().Format(time.RFC3339Nano),
			WriteFinishedUTC: writeTiming.finished.UTC().Format(time.RFC3339Nano), WriteLatencyMS: writeMS, WriteStatus: writeTiming.status, RequestsOverlapped: overlapped,
		})
		queue.Revision++
	}
	settings := map[string]string{}
	for _, name := range []string{"server_version", "shared_buffers", "fsync", "synchronous_commit"} {
		var value string
		if err := pool.QueryRow(ctx, "SHOW "+name).Scan(&value); err != nil {
			t.Fatal(err)
		}
		settings[name] = value
	}
	sort.Float64s(rangeValues)
	sort.Float64s(writeValues)
	report := map[string]any{
		"date_utc": time.Now().UTC().Format(time.RFC3339Nano), "command": "go test -p 1 -tags=integration -run TestM16BenchmarkIndexedRangeDuringUserLibraryWrite -count=1 .",
		"host_os": runtime.GOOS, "host_arch": runtime.GOARCH, "cpu_count": runtime.NumCPU(), "postgresql": settings,
		"dataset":     "one indexed 4 KiB WAV Track; 1,000 queue occurrences; 30 paired local HTTP Range and dense queue reorder requests",
		"cache_state": "warm process and PostgreSQL caches; not reset between samples", "queue_items": queueSize,
		"request_pairs": sampleCount, "all_pairs_overlapped": len(samples) == sampleCount,
		"range_p50_ms": percentileFloat(rangeValues, 0.50), "range_p95_ms": percentileFloat(rangeValues, 0.95), "range_p99_ms": percentileFloat(rangeValues, 0.99),
		"write_p50_ms": percentileFloat(writeValues, 0.50), "write_p95_ms": percentileFloat(writeValues, 0.95), "write_p99_ms": percentileFloat(writeValues, 0.99),
		"samples": samples,
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func percentileFloat(sortedValues []float64, q float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	if q == 0.5 && len(sortedValues)%2 == 0 {
		middle := len(sortedValues) / 2
		return (sortedValues[middle-1] + sortedValues[middle]) / 2
	}
	rank := int(math.Ceil(q*float64(len(sortedValues)))) - 1
	return sortedValues[max(0, min(rank, len(sortedValues)-1))]
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
