// Command m17scanbench preserves raw repeated authoritative scan samples for
// the M1.7 scale corpus. It is evidence tooling, not application behavior.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"resonance/internal/library"
	"resonance/internal/storage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "scan benchmark failed")
		os.Exit(1)
	}
}

func run() error {
	rootID := flag.String("root", "", "verified enrolled library root ID")
	repetitions := flag.Int("repetitions", 30, "complete repeated scans")
	flag.Parse()
	if *rootID == "" || *repetitions < 1 || *repetitions > 500 {
		return errors.New("a root ID and 1..500 repetitions are required")
	}
	dsn := os.Getenv("RESONANCE_DATABASE_URL")
	if dsn == "" {
		return errors.New("database configuration is required")
	}
	attemptDir := os.Getenv("RESONANCE_M17_ATTEMPT_DIR")
	if attemptDir == "" {
		return errors.New("run through the M1.7 attempt recorder")
	}
	samplesPath := filepath.Join(attemptDir, "scan-samples.jsonl")
	if _, err := os.Stat(samplesPath); err == nil {
		return errors.New("refusing to overwrite prior raw scan samples")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	store, err := storage.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Ready(ctx); err != nil {
		return err
	}
	scanner := &library.Scanner{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	file, err := os.OpenFile(samplesPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	values := make([]float64, 0, *repetitions)
	for i := 1; i <= *repetitions; i++ {
		started := time.Now()
		result, scanErr := scanner.Scan(ctx, *rootID)
		finished := time.Now()
		if scanErr != nil || result.Status != "succeeded" || !result.TraversalComplete || !result.AbsenceReconciled {
			_ = writer.Flush()
			_ = file.Close()
			return fmt.Errorf("scan %d did not complete with authoritative absence reconciliation", i)
		}
		if result.FilesHashed != 0 || result.BytesHashed != 0 || result.MetadataExtractions != 0 {
			_ = writer.Flush()
			_ = file.Close()
			return fmt.Errorf("scan %d was not an unchanged workload", i)
		}
		sample := map[string]any{
			"index": i, "started_utc": started.UTC().Format(time.RFC3339Nano), "finished_utc": finished.UTC().Format(time.RFC3339Nano),
			"process_duration_ms": float64(finished.Sub(started).Microseconds()) / 1000, "scan_duration_ms": result.DurationMS,
			"status": result.Status, "traversal_complete": result.TraversalComplete, "observations_applied": result.ObservationsApplied,
			"absence_reconciled": result.AbsenceReconciled, "files_visited": result.FilesVisited, "files_supported": result.FilesSupported,
			"files_unchanged": result.FilesUnchanged, "files_hashed": result.FilesHashed, "bytes_hashed": result.BytesHashed,
			"metadata_extractions": result.MetadataExtractions, "grouping_ms": result.GroupingMS,
			"publish_transaction_ms": result.PublishTransactionMS, "sql_statements": result.SQLStatements, "rows_affected": result.RowsAffected,
		}
		encoded, err := json.Marshal(sample)
		if err != nil {
			_ = writer.Flush()
			_ = file.Close()
			return err
		}
		if _, err := writer.Write(append(encoded, '\n')); err != nil {
			_ = writer.Flush()
			_ = file.Close()
			return err
		}
		if err := writer.Flush(); err != nil {
			_ = file.Close()
			return err
		}
		values = append(values, result.DurationMS)
	}
	if err := file.Close(); err != nil {
		return err
	}
	sort.Float64s(values)
	middle := len(values) / 2
	p50 := values[middle]
	if len(values)%2 == 0 {
		p50 = (values[middle-1] + values[middle]) / 2
	}
	report := map[string]any{
		"date_utc": time.Now().UTC().Format(time.RFC3339Nano),
		"command":  "go run ./cmd/m17scanbench -root <root-id> -repetitions 30",
		"os":       runtime.GOOS, "architecture": runtime.GOARCH, "go_version": runtime.Version(),
		"root_id": *rootID, "repetitions": *repetitions,
		"dataset":           "10,000 short tagged distinct MP3 files; 100 Artists; 1,000 Albums",
		"cache_state":       "warm process and PostgreSQL caches; OS cache not reset",
		"percentile_method": "median p50; nearest-rank p95/p99; scanner-reported duration_ms",
		"p50_ms":            p50, "p95_ms": percentile(values, 0.95), "p99_ms": percentile(values, 0.99),
		"raw_samples": "scan-samples.jsonl",
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "scan-summary.json"), append(encoded, '\n'), 0600); err != nil {
		return err
	}
	fmt.Printf("unchanged 10k scans p50/p95/p99 %.3f/%.3f/%.3f ms (%d samples)\n", p50, report["p95_ms"], report["p99_ms"], *repetitions)
	return nil
}

func percentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	rank := int(math.Ceil(q*float64(len(values)))) - 1
	return values[max(0, min(rank, len(values)-1))]
}
