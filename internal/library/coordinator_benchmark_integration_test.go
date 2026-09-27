//go:build integration

package library

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"
)

type watcherConvergenceSample struct {
	Index        int     `json:"index"`
	StartedUTC   string  `json:"started_utc"`
	FinishedUTC  string  `json:"finished_utc"`
	LatencyMS    float64 `json:"latency_ms"`
	ScanSequence uint64  `json:"scan_sequence"`
}

type watcherConvergenceEvidence struct {
	DateUTC    string                     `json:"date_utc"`
	Command    string                     `json:"command"`
	HostOS     string                     `json:"host_os"`
	HostArch   string                     `json:"host_arch"`
	CPUCount   int                        `json:"cpu_count"`
	PostgreSQL map[string]string          `json:"postgresql"`
	Dataset    string                     `json:"dataset"`
	CacheState string                     `json:"cache_state"`
	RootID     string                     `json:"root_id"`
	Samples    []watcherConvergenceSample `json:"samples"`
	P50MS      float64                    `json:"p50_ms"`
	P95MS      float64                    `json:"p95_ms"`
	P99MS      float64                    `json:"p99_ms"`
}

func TestM16BenchmarkWatcherConvergence(t *testing.T) {
	output := os.Getenv("RESONANCE_M16_BENCHMARK_OUTPUT")
	if output == "" {
		t.Skip("set RESONANCE_M16_BENCHMARK_OUTPUT to write watcher convergence evidence")
	}
	output, err := resolveBenchmarkOutput(output)
	if err != nil {
		t.Fatal("could not resolve benchmark output relative to repository root")
	}
	if _, err := os.Stat(output); err == nil {
		t.Fatalf("refusing to overwrite benchmark output %q", filepath.Base(output))
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	s, queryPool, _ := isolatedLibraryStore(t)
	root := addRoot(t, s, testWorkspaceDir(t), "M1.6 watcher benchmark")
	ctx, cancel := context.WithCancel(context.Background())
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: s, Scanner: testScanner(s),
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("coordinator did not stop after benchmark")
		}
	}()
	waitForCoordinator(t, 15*time.Second, func() bool {
		status := coordinator.Status()
		return status.ScansSucceeded >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && !status.GlobalSweep
	})

	const count = 30
	samples := make([]watcherConvergenceSample, 0, count)
	latencies := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		path := filepath.Join(root.CanonicalPath, "marker-"+strconv.Itoa(i)+".txt")
		before := coordinator.Status().ScansSucceeded
		started := time.Now()
		if err := os.WriteFile(path, []byte("watcher convergence"), 0600); err != nil {
			t.Fatal(err)
		}
		waitForCoordinator(t, 15*time.Second, func() bool {
			status := coordinator.Status()
			return status.ScansSucceeded > before && len(status.Roots) == 1 && status.Roots[0].AbsenceReconciled
		})
		finished := time.Now()
		sequence := coordinator.Status().ScansSucceeded
		latency := float64(finished.Sub(started).Microseconds()) / 1000
		latencies = append(latencies, latency)
		samples = append(samples, watcherConvergenceSample{Index: i + 1, StartedUTC: started.UTC().Format(time.RFC3339Nano), FinishedUTC: finished.UTC().Format(time.RFC3339Nano), LatencyMS: latency, ScanSequence: sequence})
		beforeDelete := sequence
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		waitForCoordinator(t, 15*time.Second, func() bool { return coordinator.Status().ScansSucceeded > beforeDelete })
	}

	settings := map[string]string{}
	for _, name := range []string{"server_version", "shared_buffers", "fsync", "synchronous_commit"} {
		var value string
		if err := queryPool.QueryRow(context.Background(), "SHOW "+name).Scan(&value); err != nil {
			t.Fatal(err)
		}
		settings[name] = value
	}
	sort.Float64s(latencies)
	evidence := watcherConvergenceEvidence{
		DateUTC: time.Now().UTC().Format(time.RFC3339Nano),
		Command: "go test -p 1 -tags=integration -run TestM16BenchmarkWatcherConvergence -count=1 ./internal/library",
		HostOS:  runtime.GOOS, HostArch: runtime.GOARCH, CPUCount: runtime.NumCPU(),
		PostgreSQL: settings, Dataset: "one empty enrolled root; 30 create-and-authoritative-scan samples; one short .txt marker at a time",
		CacheState: "warm process and PostgreSQL caches; not reset between samples", RootID: root.ID,
		Samples: samples, P50MS: percentile(latencies, 0.50), P95MS: percentile(latencies, 0.95), P99MS: percentile(latencies, 0.99),
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func resolveBenchmarkOutput(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return filepath.Join(directory, path), nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", os.ErrNotExist
		}
		directory = parent
	}
}

func percentile(sortedValues []float64, q float64) float64 {
	if len(sortedValues) == 0 {
		return math.NaN()
	}
	if len(sortedValues)%2 == 0 && q == 0.50 {
		middle := len(sortedValues) / 2
		return (sortedValues[middle-1] + sortedValues[middle]) / 2
	}
	rank := int(math.Ceil(q*float64(len(sortedValues)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sortedValues) {
		rank = len(sortedValues) - 1
	}
	return sortedValues[rank]
}
