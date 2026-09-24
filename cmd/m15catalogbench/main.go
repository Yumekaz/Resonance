package main

import (
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
)

type sample struct {
	StartedUTC  string  `json:"started_utc"`
	FinishedUTC string  `json:"finished_utc"`
	DurationMS  float64 `json:"duration_ms"`
	Status      int     `json:"status"`
	RequestID   string  `json:"request_id"`
}

func percentile(v []float64, p float64) float64 {
	sort.Float64s(v)
	if p == .5 && len(v)%2 == 0 {
		return (v[len(v)/2-1] + v[len(v)/2]) / 2
	}
	return v[max(0, int(math.Ceil(float64(len(v))*p))-1)]
}
func main() {
	url := flag.String("url", "http://127.0.0.1:8080/api/v1/tracks?limit=50", "catalog page URL")
	count := flag.Int("count", 100, "requests")
	ready := flag.String("ready-file", "", "ready marker")
	start := flag.String("start-file", "", "start marker")
	out := flag.String("out", "docs/benchmarks/M1-5-catalog-concurrent.json", "output")
	flag.Parse()
	if *count < 100 {
		panic("at least 100 requests required")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	request := func() (sample, error) {
		begin := time.Now()
		response, err := client.Get(*url)
		if err != nil {
			return sample{}, err
		}
		defer response.Body.Close()
		_, err = io.Copy(io.Discard, response.Body)
		if err != nil {
			return sample{}, err
		}
		end := time.Now()
		if response.StatusCode != 200 {
			return sample{}, fmt.Errorf("status %d", response.StatusCode)
		}
		return sample{begin.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), float64(end.Sub(begin).Microseconds()) / 1000, response.StatusCode, response.Header.Get("X-Request-ID")}, nil
	}
	if _, err := request(); err != nil {
		panic(err)
	}
	if *ready != "" {
		if err := os.WriteFile(*ready, []byte("ready"), 0600); err != nil {
			panic(err)
		}
	}
	if *start != "" {
		deadline := time.Now().Add(60 * time.Second)
		for {
			if _, err := os.Stat(*start); err == nil {
				break
			}
			if time.Now().After(deadline) {
				panic("start marker timeout")
			}
			time.Sleep(time.Millisecond)
		}
	}
	samples := []sample{}
	values := []float64{}
	for i := 0; i < *count; i++ {
		s, err := request()
		if err != nil {
			panic(err)
		}
		samples = append(samples, s)
		values = append(values, s.DurationMS)
	}
	report := map[string]any{"date_utc": time.Now().UTC().Format(time.RFC3339Nano), "url": *url, "request_count": *count, "concurrency": 1, "cache": "warm/uncontrolled", "go_version": runtime.Version(), "p50_ms": percentile(append([]float64{}, values...), .5), "p95_ms": percentile(append([]float64{}, values...), .95), "p99_ms": percentile(append([]float64{}, values...), .99), "samples": samples}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(*out, append(body, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("catalog page p50/p95/p99 %.3f/%.3f/%.3f ms\n", report["p50_ms"], report["p95_ms"], report["p99_ms"])
}
