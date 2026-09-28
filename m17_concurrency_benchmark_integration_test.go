//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"resonance/internal/library"
	"resonance/internal/m17fixtures"
)

type m17HTTPRequest struct {
	Sample    int       `json:"sample"`
	Kind      string    `json:"kind"`
	Start     time.Time `json:"started_utc"`
	End       time.Time `json:"finished_utc"`
	Status    int       `json:"status"`
	RequestID string    `json:"request_id"`
	Bytes     int       `json:"bytes"`
	Error     string    `json:"error,omitempty"`
}
type m17ScanSample struct {
	Index                    int                `json:"index"`
	Start                    time.Time          `json:"started_utc"`
	End                      time.Time          `json:"finished_utc"`
	DiscoveryLastObserved    time.Time          `json:"discovery_last_observed_utc"`
	PublishingObserved       time.Time          `json:"publishing_observed_utc"`
	TransactionStart         time.Time          `json:"transaction_started_utc"`
	TransactionFirstObserved time.Time          `json:"transaction_first_observed_utc"`
	TransactionLastObserved  time.Time          `json:"transaction_last_observed_utc"`
	ExpectedGroupingObserved bool               `json:"expected_grouping_observed"`
	Result                   library.ScanResult `json:"result"`
}

// An explicit benchmark, not part of an ordinary regression invocation.
// Requests use the production HTTP handler, scanner and storage in one schema.
func TestM17ChangedMediaHTTPConcurrency(t *testing.T) {
	if os.Getenv("RESONANCE_M17_CHANGED_BENCHMARK") != "1" {
		t.Skip("explicit M1.7 changed-media benchmark only")
	}
	output := os.Getenv("RESONANCE_M17_ATTEMPT_OUTPUT")
	if output == "" {
		t.Fatal("attempt output required")
	}
	ctx := context.Background()
	store, pool := catalogTestStore(t)
	var databaseBefore int64
	var walBefore string
	if err := pool.QueryRow(ctx, "SELECT pg_database_size(current_database()),pg_current_wal_lsn()::text").Scan(&databaseBefore, &walBefore); err != nil {
		t.Fatal(err)
	}
	rootDir, err := os.MkdirTemp(filepath.Join("data", "m17-closure"), "concurrency-")
	if err != nil {
		t.Fatal(err)
	}
	rootDir, err = filepath.Abs(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	corpus := filepath.Join(rootDir, "scale")
	source, err := os.ReadFile("testdata/metadata/untagged.mp3")
	if err != nil {
		t.Fatal(err)
	}
	corpusSummary, err := m17fixtures.GenerateScale(corpus, source, 10000, 20260928)
	if err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(corpus, "Stable", "long.wav")
	if err := os.MkdirAll(filepath.Dir(stable), 0700); err != nil {
		t.Fatal(err)
	}
	longBytes, err := os.ReadFile(filepath.Join(os.TempDir(), "ResonanceM17G1-20260928", "journey-corpus-final", "library", "long.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stable, longBytes, 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := library.CanonicalizeRoot(corpus)
	if err != nil {
		t.Fatal(err)
	}
	identity, ok, err := library.CaptureRootIdentity(canonical)
	if err != nil || !ok {
		t.Fatal("persistent root identity unavailable", err)
	}
	root, err := store.AddRootWithIdentity(ctx, "M17 10k concurrent changes", canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	scanner := &library.Scanner{Store: store}
	initial, err := scanner.Scan(ctx, root.ID)
	if err != nil || !initial.AbsenceReconciled {
		t.Fatal("initial scale import", err)
	}
	var stableID string
	var tracks, objects, locations int
	if err := pool.QueryRow(ctx, "SELECT id FROM tracks WHERE title='long'").Scan(&stableID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM tracks),(SELECT count(*) FROM media_objects),(SELECT count(*) FROM media_locations WHERE availability='available')").Scan(&tracks, &objects, &locations); err != nil || tracks != 10001 || objects != 10001 || locations != 10001 {
		t.Fatal("distinct scale cardinality", tracks, objects, locations, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO queue_items(id,track_id,position) SELECT 'qi_'||lpad(to_hex(n),32,'0'),$1,n-1 FROM generate_series(1,1000) n; UPDATE active_queue SET revision=1`, pgx.QueryExecModeSimpleProtocol, stableID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newHandlerWithCatalog(stable, "M17", io.Discard, store.Ready, store))
	defer server.Close()
	client := &http.Client{Timeout: 15 * time.Second}
	requestFile, err := os.OpenFile(filepath.Join(output, "requests.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer requestFile.Close()
	scanFile, err := os.OpenFile(filepath.Join(output, "scans.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer scanFile.Close()
	var requestMu sync.Mutex
	requests := []m17HTTPRequest{}
	var workerErr error
	record := func(r m17HTTPRequest) {
		requestMu.Lock()
		defer requestMu.Unlock()
		requests = append(requests, r)
		_ = json.NewEncoder(requestFile).Encode(r)
		if r.Error != "" || ((r.Kind == "range" && r.Status != 206) || (r.Kind != "range" && r.Status != 200)) {
			workerErr = fmt.Errorf("HTTP %s failed status %d %s", r.Kind, r.Status, r.Error)
		}
	}
	do := func(index int, kind, method, url string, body []byte, headers map[string]string) {
		r := m17HTTPRequest{Sample: index, Kind: kind, Start: time.Now().UTC()}
		req, e := http.NewRequest(method, server.URL+url, bytes.NewReader(body))
		if e == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			var resp *http.Response
			resp, e = client.Do(req)
			if e == nil {
				r.Status = resp.StatusCode
				r.RequestID = resp.Header.Get("X-Request-ID")
				var data []byte
				data, e = io.ReadAll(resp.Body)
				r.Bytes = len(data)
				if kind == "range" && e == nil && (resp.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-65535/%d", len(longBytes)) || !bytes.Equal(data, longBytes[:65536])) {
					e = fmt.Errorf("Range header/body mismatch")
				}
				resp.Body.Close()
			}
		}
		r.End = time.Now().UTC()
		if e != nil {
			r.Error = e.Error()
		}
		record(r)
	}
	target := filepath.Join(corpus, "Scale Artist 000", "Scale Album 00", "Scale Track 00000.mp3")
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	altered := bytes.ReplaceAll(original, utf16LE("Scale Artist 000"), utf16LE("Other Artist 000"))
	altered = bytes.ReplaceAll(altered, utf16LE("Scale Album 00"), utf16LE("Other Album 00"))
	if bytes.Equal(original, altered) {
		t.Fatal("retag failed")
	}
	// The monitor uses its own connection so it does not affect the product pool.
	monitorCfg := pool.Config().Copy()
	monitorCfg.ConnConfig.RuntimeParams["application_name"] = "m17_monitor"
	monitor, err := pgxpool.NewWithConfig(ctx, monitorCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	samples := []m17ScanSample{}
	for index := 1; index <= 30; index++ {
		content := bytes.ReplaceAll(original, utf16LE("Scale Artist 000"), utf16LE(fmt.Sprintf("Other Artist %03d", index)))
		content = bytes.ReplaceAll(content, utf16LE("Scale Album 00"), utf16LE(fmt.Sprintf("Other Album %02d", index)))
		if err := os.WriteFile(target, content, 0600); err != nil {
			t.Fatal(err)
		}
		future := time.Now().Add(time.Duration(index) * time.Second)
		if err := os.Chtimes(target, future, future); err != nil {
			t.Fatal(err)
		}
		sample := m17ScanSample{Index: index, Start: time.Now().UTC()}
		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				do(index, "catalog", "GET", "/api/v1/tracks?limit=50", nil, nil)
				time.Sleep(5 * time.Millisecond)
			}
		}()
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				do(index, "range", "GET", "/api/v1/tracks/"+stableID+"/stream", nil, map[string]string{"Range": "bytes=0-65535"})
				time.Sleep(5 * time.Millisecond)
			}
		}()
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				q, e := store.ReadQueue(ctx)
				if e != nil {
					requestMu.Lock()
					workerErr = e
					requestMu.Unlock()
					return
				}
				ids := make([]string, len(q.Items))
				for i, item := range q.Items {
					ids[len(ids)-1-i] = item.ID
				}
				body, _ := json.Marshal(map[string]any{"item_ids": ids, "expected_version": q.Revision})
				do(index, "queue_reorder", "PUT", "/api/v1/queue/order", body, map[string]string{"Content-Type": "application/json", "Idempotency-Key": fmt.Sprintf("00000000-0000-4000-8000-%012d", index*100000+int(q.Revision))})
				time.Sleep(5 * time.Millisecond)
			}
		}()
		monitorDone := make(chan struct{})
		go func() {
			defer close(monitorDone)
			var transactionPID int
			var transactionStart time.Time
			for {
				select {
				case <-done:
					return
				default:
				}
				var phase string
				var clock time.Time
				_ = monitor.QueryRow(ctx, "SELECT phase,clock_timestamp() FROM scan_runs WHERE root_id=$1 AND status='running' ORDER BY started_at DESC LIMIT 1", root.ID).Scan(&phase, &clock)
				if phase == "discovering" {
					sample.DiscoveryLastObserved = clock.UTC()
				}
				if phase == "publishing" && sample.PublishingObserved.IsZero() {
					sample.PublishingObserved = clock.UTC()
				}
				if transactionPID == 0 {
					_ = monitor.QueryRow(ctx, `SELECT a.pid,a.xact_start,clock_timestamp() FROM pg_stat_activity a WHERE a.datname=current_database() AND a.pid<>pg_backend_pid() AND a.state='active' AND a.query LIKE '%UPDATE media_locations SET last_seen_run_id%' AND EXISTS(SELECT 1 FROM pg_locks l WHERE l.pid=a.pid AND l.relation='media_locations'::regclass AND l.mode='RowExclusiveLock') ORDER BY a.xact_start DESC LIMIT 1`).Scan(&transactionPID, &transactionStart, &clock)
					if transactionPID != 0 {
						sample.TransactionStart = transactionStart.UTC()
						sample.TransactionFirstObserved = clock.UTC()
						sample.TransactionLastObserved = sample.TransactionFirstObserved
					}
				} else {
					var same bool
					_ = monitor.QueryRow(ctx, "SELECT xact_start=$2,clock_timestamp() FROM pg_stat_activity WHERE pid=$1", transactionPID, transactionStart).Scan(&same, &clock)
					if same {
						sample.TransactionLastObserved = clock.UTC()
					}
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
		result, scanErr := scanner.Scan(ctx, root.ID)
		sample.End = time.Now().UTC()
		close(done)
		wg.Wait()
		<-monitorDone
		sample.Result = result
		var credit, albumTitle, groupCredit, groupAlbum string
		e := pool.QueryRow(ctx, `SELECT t.artist_credit,t.album_title,ca.display_credit,al.display_title FROM media_locations ml JOIN media_objects mo ON mo.id=ml.media_object_id JOIN tracks t ON t.id=mo.track_id JOIN track_artist_memberships am ON am.track_id=t.id AND am.role='track_credit' JOIN catalog_artists ca ON ca.id=am.artist_id JOIN track_album_memberships alm ON alm.track_id=t.id JOIN catalog_albums al ON al.id=alm.album_id WHERE ml.root_id=$1 AND ml.relative_path='Scale Artist 000/Scale Album 00/Scale Track 00000.mp3' AND ml.availability='available'`, root.ID).Scan(&credit, &albumTitle, &groupCredit, &groupAlbum)
		sample.ExpectedGroupingObserved = e == nil && credit == fmt.Sprintf("Other Artist %03d", index) && groupCredit == credit && albumTitle == fmt.Sprintf("Other Album %02d", index) && groupAlbum == albumTitle
		_ = json.NewEncoder(scanFile).Encode(sample)
		samples = append(samples, sample)
		if scanErr != nil || !result.AbsenceReconciled || result.FilesHashed != 1 || result.MetadataExtractions != 1 || result.GroupingMS <= 0 || !sample.ExpectedGroupingObserved {
			t.Fatalf("changed-media sample %d status=%s hash=%d parse=%d grouping_ms=%f authoritative=%t grouping_observed=%t error=%v", index, result.Status, result.FilesHashed, result.MetadataExtractions, result.GroupingMS, result.AbsenceReconciled, sample.ExpectedGroupingObserved, scanErr)
		}
		requestMu.Lock()
		e = workerErr
		requestMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
	}
	summary := map[string]any{"samples": 30, "dataset": corpusSummary, "actual_catalog_tracks": tracks, "actual_media_objects": objects, "actual_locations": locations, "queue_items": 1000, "initial_scan": initial, "cache": "warm/uncontrolled; no SLO", "transaction_interval": "PostgreSQL clock_timestamp from first and last active observations of the same schema's publication transaction; discovery core ends at its last observed discovering phase; 2ms polling"}
	var databaseAfter, walBytes int64
	if err := pool.QueryRow(ctx, "SELECT pg_database_size(current_database()),pg_wal_lsn_diff(pg_current_wal_lsn(),$1::pg_lsn)::bigint", walBefore).Scan(&databaseAfter, &walBytes); err != nil {
		t.Fatal(err)
	}
	summary["database_size_bytes_before"] = databaseBefore
	summary["database_size_bytes_after"] = databaseAfter
	summary["cluster_wal_bytes_during_import_and_samples"] = walBytes
	summary["host_os"] = runtime.GOOS
	summary["host_arch"] = runtime.GOARCH
	summary["go_version"] = runtime.Version()
	summary["cpu_count"] = runtime.NumCPU()
	settings := map[string]string{}
	for _, name := range []string{"server_version", "shared_buffers", "fsync", "synchronous_commit"} {
		var val string
		if err := pool.QueryRow(ctx, "SHOW "+name).Scan(&val); err != nil {
			t.Fatal(err)
		}
		settings[name] = val
	}
	summary["postgresql"] = settings
	overlaps := map[string]int{}
	pubOverlaps := map[string]int{}
	discoveryOverlaps := map[string]int{}
	durations := []float64{}
	sampleChecks := []map[string]any{}
	for _, s := range samples {
		durations = append(durations, s.Result.DurationMS)
		discovery := map[string]int{}
		publication := map[string]int{}
		for _, r := range requests {
			if r.Sample != s.Index {
				continue
			}
			if m17IntervalsOverlap(r.Start, r.End, s.Start, s.End) {
				overlaps[r.Kind]++
			}
			if m17IntervalsOverlap(r.Start, r.End, s.Start, s.DiscoveryLastObserved) {
				discoveryOverlaps[r.Kind]++
				discovery[r.Kind]++
			}
			if !s.TransactionFirstObserved.IsZero() && m17IntervalsOverlap(r.Start, r.End, s.TransactionFirstObserved, s.TransactionLastObserved) {
				pubOverlaps[r.Kind]++
				publication[r.Kind]++
			}
		}
		sampleChecks = append(sampleChecks, map[string]any{"index": s.Index, "discovery": discovery, "publication": publication})
		for _, kind := range []string{"catalog", "range", "queue_reorder"} {
			if discovery[kind] < 1 || publication[kind] < 1 {
				t.Errorf("sample %d missing %s overlap discovery=%d publication=%d", s.Index, kind, discovery[kind], publication[kind])
			}
		}
	}
	sort.Float64s(durations)
	summary["p50_ms"] = (durations[14] + durations[15]) / 2
	summary["p95_ms"] = durations[28]
	summary["p99_ms"] = durations[29]
	summary["scan_overlapping_requests"] = overlaps
	summary["publication_core_overlapping_requests"] = pubOverlaps
	summary["discovery_core_overlapping_requests"] = discoveryOverlaps
	summary["sample_overlap_checks"] = sampleChecks
	for _, kind := range []string{"catalog", "range", "queue_reorder"} {
		if overlaps[kind] < 30 || pubOverlaps[kind] < 30 {
			t.Errorf("insufficient %s overlaps: scan=%d publication=%d", kind, overlaps[kind], pubOverlaps[kind])
		}
	}
	body, _ := json.MarshalIndent(summary, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "summary.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}
func utf16LE(s string) []byte {
	out := []byte{}
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func m17IntervalsOverlap(a0, a1, b0, b1 time.Time) bool {
	return a0.Before(a1) && b0.Before(b1) && a0.Before(b1) && b0.Before(a1)
}
func TestM17MeasuredIntervalOverlap(t *testing.T) {
	base := time.Unix(0, 0)
	for _, c := range []struct {
		name           string
		a0, a1, b0, b1 int
		want           bool
	}{{"positive", 0, 10, 5, 15, true}, {"touching", 0, 5, 5, 10, false}, {"zero_inside", 5, 5, 0, 10, false}, {"reversed", 9, 4, 0, 10, false}, {"disjoint", 0, 2, 4, 8, false}} {
		t.Run(c.name, func(t *testing.T) {
			at := func(n int) time.Time { return base.Add(time.Duration(n) * time.Millisecond) }
			if m17IntervalsOverlap(at(c.a0), at(c.a1), at(c.b0), at(c.b1)) != c.want {
				t.Fatal("incorrect interval evidence")
			}
		})
	}
}
