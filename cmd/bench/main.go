package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"sort"
	"time"
)

type sample struct {
	Kind         string  `json:"kind"`
	StartedUTC   string  `json:"started_utc"`
	FinishedUTC  string  `json:"finished_utc"`
	Offset       int64   `json:"offset"`
	TTFBMs       float64 `json:"ttfb_ms"`
	TotalMs      float64 `json:"total_ms"`
	Bytes        int64   `json:"bytes"`
	Status       int     `json:"status"`
	ContentRange string  `json:"content_range"`
	RequestID    string  `json:"request_id"`
}

func percentile(values []float64, p float64) float64 {
	sort.Float64s(values)
	if p == .5 && len(values)%2 == 0 {
		return (values[len(values)/2-1] + values[len(values)/2]) / 2
	}
	index := max(0, int(math.Ceil(float64(len(values))*p))-1)
	return values[index]
}

func validateResponse(resp *http.Response, offset, expected, size int64, partial bool) error {
	status := 200
	if partial {
		status = 206
	}
	if resp.StatusCode != status || resp.ContentLength != expected {
		return fmt.Errorf("unexpected status/length: %d/%d; wanted %d/%d", resp.StatusCode, resp.ContentLength, status, expected)
	}
	if partial && resp.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, offset+expected-1, size) {
		return fmt.Errorf("incorrect Content-Range: %q", resp.Header.Get("Content-Range"))
	}
	return nil
}

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/media/demo-track", "media URL")
	count := flag.Int("count", 100, "number of random range requests")
	out := flag.String("out", "docs/benchmarks/M0-raw.json", "machine-readable results")
	readyFile := flag.String("ready-file", "", "write this marker after warm-up requests")
	startFile := flag.String("start-file", "", "wait for this marker before random ranges")
	flag.Parse()
	if *count < 100 {
		fmt.Fprintln(os.Stderr, "count must be at least 100")
		os.Exit(2)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	probe, err := client.Head(*url)
	if err != nil {
		panic(err)
	}
	probe.Body.Close()
	if probe.StatusCode != 200 {
		panic(fmt.Sprintf("HEAD returned %d", probe.StatusCode))
	}
	var size int64
	if _, err := fmt.Sscan(probe.Header.Get("Content-Length"), &size); err != nil || size < 2 {
		panic("media must contain at least two bytes")
	}
	var samples []sample
	measure := func(kind string, offset int64, header string) {
		req, err := http.NewRequest("GET", *url, nil)
		if err != nil {
			panic(err)
		}
		if header != "" {
			req.Header.Set("Range", header)
		}
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		expected := size
		if header != "" {
			expected = min(int64(65536), size-offset)
		}
		if err := validateResponse(resp, offset, expected, size, header != ""); err != nil {
			panic(err)
		}
		var first [1]byte
		if _, err := io.ReadFull(resp.Body, first[:]); err != nil {
			panic(err)
		}
		ttfb := time.Since(start).Seconds() * 1000
		copied, err := io.Copy(io.Discard, resp.Body)
		if err != nil {
			panic(err)
		}
		if copied+1 != expected {
			panic(fmt.Sprintf("short response: %d, wanted %d", copied+1, expected))
		}
		finished := time.Now()
		samples = append(samples, sample{Kind: kind, StartedUTC: start.UTC().Format(time.RFC3339Nano), FinishedUTC: finished.UTC().Format(time.RFC3339Nano), Offset: offset, TTFBMs: ttfb, TotalMs: finished.Sub(start).Seconds() * 1000, Bytes: copied + 1, Status: resp.StatusCode, ContentRange: resp.Header.Get("Content-Range"), RequestID: resp.Header.Get("X-Request-ID")})
	}
	measure("full", 0, "")
	measure("range_first", 0, "bytes=0-65535")
	if *readyFile != "" {
		if err := os.WriteFile(*readyFile, []byte("ready"), 0600); err != nil {
			panic(err)
		}
	}
	if *startFile != "" {
		deadline := time.Now().Add(60 * time.Second)
		for {
			if _, err := os.Stat(*startFile); err == nil {
				break
			} else if !os.IsNotExist(err) {
				panic(err)
			}
			if time.Now().After(deadline) {
				panic("timed out waiting for Range start marker")
			}
			time.Sleep(time.Millisecond)
		}
	}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < *count; i++ {
		offset := rng.Int63n(size)
		end := offset + 65535
		if end >= size {
			end = size - 1
		}
		measure("random_range", offset, fmt.Sprintf("bytes=%d-%d", offset, end))
	}
	values := make([]float64, 0, *count)
	ttfbValues := make([]float64, 0, *count)
	for _, s := range samples {
		if s.Kind == "random_range" {
			values = append(values, s.TotalMs)
			ttfbValues = append(ttfbValues, s.TTFBMs)
		}
	}
	report := map[string]any{
		"url": *url, "request_count": *count, "concurrency": 1, "cache_state": "uncontrolled OS cache; full transfer precedes ranges", "connection_policy": "Go default transport, sequential reuse", "median_method": "mean of middle pair for even sample counts", "p95_method": "nearest rank ceil(0.95*N)",
		"timestamp_utc": time.Now().UTC().Format(time.RFC3339),
		"go_version":    runtime.Version(), "go_os": runtime.GOOS, "go_arch": runtime.GOARCH,
		"media_bytes": size, "range_bytes": 65536, "random_seed": 42,
		"full_ttfb_ms": samples[0].TTFBMs, "first_range_ttfb_ms": samples[1].TTFBMs,
		"random_range_total_median_ms": percentile(append([]float64(nil), values...), .5),
		"random_range_total_p95_ms":    percentile(append([]float64(nil), values...), .95),
		"random_range_total_p99_ms":    percentile(append([]float64(nil), values...), .99),
		"random_range_ttfb_median_ms":  percentile(append([]float64(nil), ttfbValues...), .5),
		"random_range_ttfb_p95_ms":     percentile(append([]float64(nil), ttfbValues...), .95),
		"random_range_ttfb_p99_ms":     percentile(append([]float64(nil), ttfbValues...), .99),
		"samples":                      samples,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("full TTFB %.3f ms; range TTFB %.3f ms; %d random ranges total p50/p95/p99 %.3f/%.3f/%.3f ms\n", samples[0].TTFBMs, samples[1].TTFBMs, *count, report["random_range_total_median_ms"], report["random_range_total_p95_ms"], report["random_range_total_p99_ms"])
}
