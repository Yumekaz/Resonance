//go:build integration && benchmark

package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"resonance/internal/storage"
)

type reconciliationBenchmarkSample struct {
	Workload   string `json:"workload"`
	Iteration  int    `json:"iteration"`
	CacheState string `json:"cache_state"`
	ScanResult
}

type reconciliationPercentiles struct {
	Samples int     `json:"samples"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
}

func TestM13BenchmarkSuite(t *testing.T) {
	output := os.Getenv("RESONANCE_BENCHMARK_OUTPUT")
	if output == "" {
		t.Skip("set RESONANCE_BENCHMARK_OUTPUT to run the M1.3A benchmark suite")
	}
	repetitions := 10
	if value := os.Getenv("RESONANCE_BENCHMARK_REPETITIONS"); value != "" {
		if _, err := fmt.Sscan(value, &repetitions); err != nil || repetitions < 3 || repetitions > 10 {
			t.Fatalf("RESONANCE_BENCHMARK_REPETITIONS must be between 3 and 10")
		}
	}
	s, pool, _ := isolatedLibraryStore(t)
	ctx := context.Background()
	rootDir := testWorkspaceDir(t)
	root := addRoot(t, s, rootDir, "M1.3A benchmark")
	const wavCount = 1000
	const mp3Count = 20
	const partialCount = 100
	files := make([]benchmarkFile, 0, wavCount+mp3Count)
	manifest := sha256.New()
	var totalBytes int64
	for i := 0; i < wavCount; i++ {
		relative := fmt.Sprintf("track-%04d.wav", i)
		content := benchmarkWAV(i)
		files = append(files, writeBenchmarkFile(t, rootDir, relative, content))
		addManifestHash(manifest, relative, content)
		totalBytes += int64(len(content))
	}
	for i := 0; i < mp3Count; i++ {
		relative := fmt.Sprintf("retag-%02d.mp3", i)
		content := fixtureMP3WithTitle(t, fmt.Sprintf("Benchmark Tag %02d", i))
		files = append(files, writeBenchmarkFile(t, rootDir, relative, content))
		addManifestHash(manifest, relative, content)
		totalBytes += int64(len(content))
	}
	for i := 0; i < partialCount; i++ {
		relative := filepath.ToSlash(filepath.Join("partial", fmt.Sprintf("track-%03d.wav", i)))
		content := benchmarkWAV(2000 + i)
		files = append(files, writeBenchmarkFile(t, rootDir, relative, content))
		addManifestHash(manifest, relative, content)
		totalBytes += int64(len(content))
	}
	if err := seedM12Catalog(ctx, pool, root, rootDir, files); err != nil {
		t.Fatal(err)
	}
	initialDBBytes, initialWAL := benchmarkDatabaseState(t, pool)
	config := benchmarkDatabaseConfig(t, pool)
	logPath := os.Getenv("RESONANCE_BENCHMARK_LOG")
	var logFile *os.File
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
			t.Fatal(err)
		}
		var err error
		logFile, err = os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		defer logFile.Close()
	}
	scanner := testScanner(s)
	if logFile != nil {
		scanner.Log = slog.New(slog.NewJSONHandler(logFile, nil))
	}

	var concurrentRange map[string]any
	var rangeCmd *exec.Cmd
	rangeStarted := time.Time{}
	var readyMarker, startMarker string
	if benchExe, url, rangeOut := os.Getenv("RESONANCE_BENCHMARK_RANGE_BENCH"), os.Getenv("RESONANCE_BENCHMARK_RANGE_URL"), os.Getenv("RESONANCE_BENCHMARK_RANGE_OUTPUT"); benchExe != "" && url != "" && rangeOut != "" {
		if err := os.MkdirAll(filepath.Dir(rangeOut), 0755); err != nil {
			t.Fatal(err)
		}
		markers := t.TempDir()
		readyMarker, startMarker = filepath.Join(markers, "range-ready"), filepath.Join(markers, "range-start")
		rangeCmd = exec.Command(benchExe, "-url", url, "-count", "100", "-out", rangeOut, "-ready-file", readyMarker, "-start-file", startMarker)
		if err := rangeCmd.Start(); err != nil {
			t.Fatalf("start concurrent Range benchmark: %v", err)
		}
		t.Cleanup(func() {
			if rangeCmd.ProcessState == nil || !rangeCmd.ProcessState.Exited() {
				_ = rangeCmd.Process.Kill()
				_ = rangeCmd.Wait()
			}
		})
		rangeStarted = time.Now().UTC()
		deadline := time.Now().Add(60 * time.Second)
		for {
			if _, err := os.Stat(readyMarker); err == nil {
				break
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatal("Range benchmark did not finish warm-up before scan")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	var samples []reconciliationBenchmarkSample
	run := func(workload string, iteration int, cacheState string) ScanResult {
		result, err := scanner.Scan(ctx, root.ID)
		if err != nil {
			t.Fatalf("benchmark scan %s/%d failed: %#v: %v", workload, iteration, result, err)
		}
		samples = append(samples, reconciliationBenchmarkSample{Workload: workload, Iteration: iteration, CacheState: cacheState, ScanResult: result})
		return result
	}
	if startMarker != "" {
		if err := os.WriteFile(startMarker, []byte("start"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	baseline := run("m12_baseline_migrated", 0, "first scanner pass; OS filesystem cache unverified")
	if baseline.Status != "succeeded" || baseline.FilesHashed != wavCount+mp3Count+partialCount || baseline.TracksCreated != 0 || baseline.MediaObjectsCreated != 0 || baseline.LocationsAdded != 0 {
		t.Fatalf("M1.2 baseline did not retain identities: %#v", baseline)
	}
	for i := 1; i < repetitions; i++ {
		resetM12ObservationState(t, pool, root.ID)
		result := run("m12_baseline_migrated", i, "warm cache; mtime observation reset to migrated M1.2 state")
		if result.FilesHashed != wavCount+mp3Count+partialCount || result.TracksCreated != 0 || result.MediaObjectsCreated != 0 || result.LocationsAdded != 0 {
			t.Fatalf("repeated M1.2 baseline did not retain identities: %#v", result)
		}
	}
	if rangeCmd != nil {
		if err := rangeCmd.Wait(); err != nil {
			t.Fatalf("concurrent Range workload failed: %v", err)
		}
		rangeFinished := time.Now().UTC()
		scanStarted, scanFinished := benchmarkScanEventTimes(logPath, baseline.RunID, rangeStarted, baseline.DurationMS)
		concurrentRange = map[string]any{"range_started_utc": rangeStarted.Format(time.RFC3339Nano), "range_finished_utc": rangeFinished.Format(time.RFC3339Nano), "scan_started_utc": scanStarted.Format(time.RFC3339Nano), "scan_finished_utc": scanFinished.Format(time.RFC3339Nano), "overlap_ms": overlapMilliseconds(rangeStarted, rangeFinished, scanStarted, scanFinished), "path": filepath.Base(os.Getenv("RESONANCE_BENCHMARK_RANGE_OUTPUT"))}
		if data, err := os.ReadFile(os.Getenv("RESONANCE_BENCHMARK_RANGE_OUTPUT")); err == nil {
			var report map[string]any
			if json.Unmarshal(data, &report) == nil {
				concurrentRange["summary"] = report
				var overlapping []float64
				for _, rawSample := range report["samples"].([]any) {
					sample := rawSample.(map[string]any)
					if sample["kind"] != "random_range" {
						continue
					}
					started, startErr := time.Parse(time.RFC3339Nano, sample["started_utc"].(string))
					finished, finishErr := time.Parse(time.RFC3339Nano, sample["finished_utc"].(string))
					if startErr != nil || finishErr != nil {
						t.Fatal("Range sample lacks valid request timestamps")
					}
					if started.Before(scanFinished) && finished.After(scanStarted) {
						overlapping = append(overlapping, sample["total_ms"].(float64))
					}
				}
				if len(overlapping) < 90 {
					t.Fatalf("only %d of 100 Range requests overlapped reconciliation", len(overlapping))
				}
				sort.Float64s(overlapping)
				concurrentRange["overlapping_requests"] = len(overlapping)
				concurrentRange["overlapping_range_p50_ms"] = percentileBenchmark(overlapping, .5)
				concurrentRange["overlapping_range_p95_ms"] = percentileBenchmark(overlapping, .95)
				concurrentRange["overlapping_range_p99_ms"] = percentileBenchmark(overlapping, .99)
				concurrentRange["overlapping_range_max_ms"] = overlapping[len(overlapping)-1]
			}
		}
	}
	for i := 0; i < 20; i++ {
		result := run("unchanged_warm", i, "warm cache")
		if result.FilesHashed != 0 || result.BytesHashed != 0 || result.MetadataExtractions != 0 || result.LocationsAdded != 0 || result.TracksCreated != 0 {
			t.Fatalf("unchanged scan did work: %#v", result)
		}
	}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 10; j++ {
			file := files[(i*10+j)%wavCount]
			setBenchmarkMTime(t, file, i, j)
		}
		result := run("one_percent_mtime_only", i, "warm cache")
		if result.FilesHashed != 10 || result.MetadataExtractions != 0 || result.StatChangedSameBytes != 10 {
			t.Fatalf("mtime-only sample: %#v", result)
		}
	}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 10; j++ {
			index := wavCount - 1 - (i*10 + j)
			file := files[index]
			writeBenchmarkFileContents(t, file, benchmarkWAV(3000+i*10+j))
		}
		result := run("one_percent_new_bytes", i, "warm cache")
		if result.FilesHashed != 10 || result.ChangedBytes != 10 || result.MediaObjectsCreated != 10 || result.MetadataExtractions != 0 {
			t.Fatalf("new-byte sample: %#v", result)
		}
	}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 2; j++ {
			index := (i*2 + j) % mp3Count
			file := files[wavCount+index]
			writeBenchmarkFileContents(t, file, fixtureMP3WithTitle(t, fmt.Sprintf("Retag %02d.%d", i, j)))
		}
		result := run("mixed_retags", i, "warm cache")
		if result.FilesHashed != 2 || result.ChangedBytes != 2 || result.MetadataExtractions != 2 {
			t.Fatalf("retag sample: %#v", result)
		}
	}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 8; j++ {
			file := files[200+i*8+j]
			writeBenchmarkFileContents(t, file, benchmarkWAV(5000+i*8+j))
		}
		for j := 0; j < 2; j++ {
			index := (i*2 + j) % mp3Count
			file := files[wavCount+index]
			writeBenchmarkFileContents(t, file, fixtureMP3WithTitle(t, fmt.Sprintf("Mixed Retag %02d.%d", i, j)))
		}
		result := run("mixed_retags_and_replacements", i, "warm cache")
		if result.FilesHashed != 10 || result.ChangedBytes != 10 || result.MetadataExtractions != 2 {
			t.Fatalf("mixed retag/replacement sample: %#v", result)
		}
	}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 10; j++ {
			file := files[300+i*10+j]
			moveBenchmarkFile(t, rootDir, file, filepath.ToSlash(filepath.Join("native-moves", filepath.Base(file.relative))))
		}
		result := run("rename_native_provider", i, "warm cache")
		if result.FilesHashed != 10 || result.LocationsMoved != 10 || result.LocationsAdded != 0 || result.LocationsUnavailable != 0 {
			t.Fatalf("native rename sample: %#v", result)
		}
	}
	scanner.nativeIdentity = noNativeIdentityProvider{}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 10; j++ {
			file := files[500+i*10+j]
			moveBenchmarkFile(t, rootDir, file, filepath.ToSlash(filepath.Join("nonative-moves", filepath.Base(file.relative))))
		}
		result := run("rename_native_unavailable", i, "warm cache")
		if result.FilesHashed != 10 || result.LocationsMoved != 0 || result.LocationsAdded != 10 || result.LocationsUnavailable != 10 {
			t.Fatalf("no-native rename sample: %#v", result)
		}
	}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 10; j++ {
			source := files[400+i*10+j]
			target := filepath.ToSlash(filepath.Join("duplicates", fmt.Sprintf("copy-%02d-%02d.wav", i, j)))
			writeBenchmarkFile(t, rootDir, target, mustReadBenchmarkFile(t, source.path))
		}
		result := run("exact_duplicate_additions", i, "warm cache")
		if result.FilesHashed != 10 || result.LocationsAdded != 10 || result.TracksCreated != 0 || result.MediaObjectsCreated != 0 || result.MetadataExtractions != 0 {
			t.Fatalf("duplicate sample: %#v", result)
		}
	}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 10; j++ {
			file := files[700+i*10+j]
			if err := os.Remove(file.path); err != nil {
				t.Fatal(err)
			}
		}
		result := run("disappearance", i, "warm cache")
		if result.LocationsUnavailable != 10 || !result.AbsenceReconciled {
			t.Fatalf("disappearance sample: %#v", result)
		}
	}
	for i := 0; i < repetitions; i++ {
		for j := 0; j < 10; j++ {
			file := files[wavCount+mp3Count+i*10+j]
			if err := os.Remove(file.path); err != nil {
				t.Fatal(err)
			}
		}
		scanner.openDir = func(root *os.Root, name string) (*os.File, error) {
			if name == "partial" {
				return nil, os.ErrPermission
			}
			return root.Open(name)
		}
		partial := run("partial_incomplete_absence_suppressed", i, "warm cache")
		if partial.TraversalComplete || partial.AbsenceReconciled {
			t.Fatalf("partial scan reconciled absence: %#v", partial)
		}
		scanner.openDir = nil
		complete := run("partial_complete_followup", i, "warm cache")
		if !complete.AbsenceReconciled || complete.LocationsUnavailable != 10 {
			t.Fatalf("complete follow-up did not reconcile exactly ten files: %#v", complete)
		}
	}

	finalDBBytes, finalWAL := benchmarkDatabaseState(t, pool)
	walBytes, err := benchmarkWALBytes(t, pool, initialWAL, finalWAL)
	if err != nil {
		t.Fatal(err)
	}
	workloadSummaries := summarizeBenchmarkSamples(samples)
	host, _ := os.Hostname()
	report := map[string]any{
		"run_date_utc":                     time.Now().UTC().Format(time.RFC3339Nano),
		"machine":                          map[string]any{"hostname": host, "os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "go_version": runtime.Version()},
		"postgres":                         config,
		"application_pool_max_connections": 4,
		"dataset":                          map[string]any{"wav_locations": wavCount, "mp3_locations": mp3Count, "partial_directory_locations": partialCount, "initial_total_files": len(files), "initial_total_bytes": totalBytes, "manifest_sha256": fmt.Sprintf("%x", manifest.Sum(nil)), "media": "generated PCM WAV plus CC0 metadata test fixture MP3s", "file_size_bytes_wav": 244},
		"cache_conditions":                 map[string]string{"first_pass": "first scanner pass over a newly generated corpus; OS filesystem cache was not forcibly cleared, so cold-cache status is unverified", "warm": "immediate repeated scans after the first full hash pass"},
		"repetitions":                      repetitions,
		"percentile_method":                map[string]string{"p50": "middle value or arithmetic mean of the middle pair", "p95_p99": "nearest rank ceil(p*N)"},
		"sql_metrics":                      map[string]string{"statements": "pgx QueryTracer calls for Query, QueryRow, and Exec during this scan", "rows_affected": "sum of rows from INSERT, UPDATE, DELETE, and MERGE command tags; SELECT row counts are excluded"},
		"database_size_bytes_before":       initialDBBytes, "database_size_bytes_after": finalDBBytes,
		"wal_bytes_generated":  walBytes,
		"range_overlap":        concurrentRange,
		"workload_percentiles": workloadSummaries,
		"samples":              samples,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("M1.3A benchmark artifact: %s", output)
}

type benchmarkFile struct {
	relative string
	path     string
	content  []byte
}

func benchmarkWAV(seed int) []byte {
	data := make([]byte, 44+200)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 44100)
	binary.LittleEndian.PutUint32(data[28:32], 88200)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], 200)
	for i := 44; i < len(data); i++ {
		data[i] = byte(seed*31 + i*17)
	}
	binary.LittleEndian.PutUint32(data[44:48], uint32(seed))
	return data
}

func resetM12ObservationState(t *testing.T, pool *pgxpool.Pool, rootID string) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `UPDATE media_locations SET observed_mtime_ns=NULL,last_seen_run_id=NULL WHERE root_id=$1`, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `UPDATE library_roots SET last_successful_scan_id=NULL WHERE id=$1`, rootID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func writeBenchmarkFile(t *testing.T, root, relative string, content []byte) benchmarkFile {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	return benchmarkFile{relative: filepath.ToSlash(relative), path: path, content: append([]byte(nil), content...)}
}

func writeBenchmarkFileContents(t *testing.T, file benchmarkFile, content []byte) {
	t.Helper()
	if err := os.WriteFile(file.path, content, 0600); err != nil {
		t.Fatal(err)
	}
	file.content = append(file.content[:0], content...)
	future := time.Now().Add(15 * time.Second)
	if err := os.Chtimes(file.path, future, future); err != nil {
		t.Fatal(err)
	}
}

func moveBenchmarkFile(t *testing.T, root string, file benchmarkFile, destinationRelative string) {
	t.Helper()
	destination := filepath.Join(root, filepath.FromSlash(destinationRelative))
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file.path, destination); err != nil {
		t.Fatal(err)
	}
	file.relative, file.path = filepath.ToSlash(destinationRelative), destination
}

func seedM12Catalog(ctx context.Context, pool *pgxpool.Pool, root storage.LibraryRoot, rootDir string, files []benchmarkFile) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, file := range files {
		content, err := os.ReadFile(file.path)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(content)
		trackID := fmt.Sprintf("trk_bench_%04d", i)
		objectID := "obj_" + fmt.Sprintf("%x", hash[:])
		locationID := fmt.Sprintf("loc_bench_%04d", i)
		format := filepath.Ext(file.relative)[1:]
		title := strings.TrimSuffix(filepath.Base(file.relative), filepath.Ext(file.relative))
		if _, err := tx.Exec(ctx, `INSERT INTO tracks(id,title,title_source) VALUES($1,$2,'filename')`, trackID, title); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES($1,$2,$3,$4,$5)`, objectID, trackID, hash[:], format, len(content)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size)
			VALUES($1,$2,$3,$4,$5,$6)`, locationID, objectID, filepath.Join(rootDir, filepath.FromSlash(file.relative)), root.ID, file.relative, len(content)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE tracks SET metadata_source_location_id=$2 WHERE id=$1", trackID, locationID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func addManifestHash(manifest hash.Hash, relative string, content []byte) {
	hash := sha256.Sum256(content)
	_, _ = manifest.Write([]byte(relative))
	_, _ = manifest.Write([]byte{0})
	_, _ = manifest.Write(hash[:])
}

func setBenchmarkMTime(t *testing.T, file benchmarkFile, iteration, offset int) {
	t.Helper()
	future := time.Now().Add(time.Duration(20+iteration*10+offset) * time.Second)
	if err := os.Chtimes(file.path, future, future); err != nil {
		t.Fatal(err)
	}
}

func mustReadBenchmarkFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func benchmarkDatabaseState(t *testing.T, pool *pgxpool.Pool) (databaseBytes int64, walLSN string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), "SELECT pg_database_size(current_database()),pg_current_wal_lsn()::text").Scan(&databaseBytes, &walLSN); err != nil {
		t.Fatal(err)
	}
	return databaseBytes, walLSN
}

func benchmarkDatabaseConfig(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	var version, maxConnections, sharedBuffers, fsync string
	if err := pool.QueryRow(context.Background(), `SELECT current_setting('server_version'),current_setting('max_connections'),current_setting('shared_buffers'),current_setting('fsync')`).Scan(&version, &maxConnections, &sharedBuffers, &fsync); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"server_version": version, "max_connections": maxConnections, "shared_buffers": sharedBuffers, "fsync": fsync}
}

func benchmarkWALBytes(t *testing.T, pool *pgxpool.Pool, startLSN, endLSN string) (int64, error) {
	t.Helper()
	var bytes int64
	err := pool.QueryRow(context.Background(), "SELECT pg_wal_lsn_diff($1::pg_lsn,$2::pg_lsn)::bigint", endLSN, startLSN).Scan(&bytes)
	return bytes, err
}

func summarizeBenchmarkSamples(samples []reconciliationBenchmarkSample) map[string]reconciliationPercentiles {
	groups := make(map[string][]float64)
	for _, sample := range samples {
		groups[sample.Workload] = append(groups[sample.Workload], sample.DurationMS)
	}
	result := make(map[string]reconciliationPercentiles, len(groups))
	for label, values := range groups {
		sort.Float64s(values)
		result[label] = reconciliationPercentiles{Samples: len(values), P50MS: percentileBenchmark(values, .50), P95MS: percentileBenchmark(values, .95), P99MS: percentileBenchmark(values, .99)}
	}
	return result
}

func percentileBenchmark(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p == .5 && len(sorted)%2 == 0 {
		return (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	index := int(math.Ceil(float64(len(sorted))*p)) - 1
	return sorted[max(0, index)]
}

func overlapMilliseconds(rangeStart, rangeEnd, scanStart, scanEnd time.Time) int64 {
	start := max(rangeStart.UnixMilli(), scanStart.UnixMilli())
	end := min(rangeEnd.UnixMilli(), scanEnd.UnixMilli())
	if end <= start {
		return 0
	}
	return end - start
}

func benchmarkScanEventTimes(path, runID string, fallback time.Time, durationMS float64) (time.Time, time.Time) {
	if path == "" {
		return fallback, fallback.Add(time.Duration(durationMS * float64(time.Millisecond)))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback, fallback.Add(time.Duration(durationMS * float64(time.Millisecond)))
	}
	var started, finished time.Time
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var event struct {
			Message string `json:"msg"`
			Time    string `json:"time"`
			RunID   string `json:"run_id"`
		}
		if json.Unmarshal(line, &event) == nil && event.RunID == runID && (event.Message == "scan_started" || event.Message == "scan_finished") {
			parsed, err := time.Parse(time.RFC3339Nano, event.Time)
			if err == nil {
				if event.Message == "scan_started" {
					started = parsed
				} else {
					finished = parsed
				}
			}
		}
	}
	if started.IsZero() {
		started = fallback
	}
	if finished.IsZero() {
		finished = started.Add(time.Duration(durationMS * float64(time.Millisecond)))
	}
	return started, finished
}
