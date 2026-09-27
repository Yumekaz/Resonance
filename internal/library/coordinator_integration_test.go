//go:build integration

package library

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jackc/pgx/v5/pgxpool"
	"resonance/internal/storage"
)

func TestM16RealWatcherConvergesCreateModifyDuplicateRenameDeleteAndRestart(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	rootPath := testWorkspaceDir(t)
	root := addRoot(t, s, rootPath, "real watcher convergence")

	coordinator, cancel, done := startIntegrationCoordinator(t, s)
	waitForCoordinator(t, 15*time.Second, func() bool {
		status := coordinator.Status()
		return status.ScansSucceeded >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && !status.GlobalSweep
	})

	originalPath := filepath.Join(rootPath, "original.wav")
	duplicatePath := filepath.Join(rootPath, "copies", "duplicate.wav")
	writeFile(t, originalPath, fixtureWAV())
	original := waitLocationState(t, pool, root.ID, "original.wav", "available")
	writeFile(t, duplicatePath, fixtureWAV())
	waitForCoordinator(t, 15*time.Second, func() bool {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM media_locations WHERE root_id=$1 AND availability='available'`, root.ID).Scan(&count); err != nil {
			return false
		}
		return count == 2
	})
	duplicate := waitLocationState(t, pool, root.ID, "copies/duplicate.wav", "available")
	if duplicate.TrackID != original.TrackID || duplicate.ObjectID != original.ObjectID {
		t.Fatalf("same-content duplicate did not converge on shared identities: original=%#v duplicate=%#v", original, duplicate)
	}

	modified := fixtureWAV()
	modified[len(modified)-1] ^= 1
	writeFile(t, duplicatePath, modified)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(duplicatePath, future, future); err != nil {
		t.Fatal(err)
	}
	waitForCoordinator(t, 15*time.Second, func() bool {
		var objectID string
		err := pool.QueryRow(context.Background(), `SELECT media_object_id FROM media_locations WHERE root_id=$1 AND relative_path='copies/duplicate.wav' AND availability='available'`, root.ID).Scan(&objectID)
		return err == nil && objectID != duplicate.ObjectID
	})

	renamedPath := filepath.Join(rootPath, "renamed.wav")
	if err := os.Rename(originalPath, renamedPath); err != nil {
		t.Fatal(err)
	}
	renamed := waitLocationState(t, pool, root.ID, "renamed.wav", "available")
	if renamed.TrackID != original.TrackID {
		t.Fatalf("rename changed Track identity: before=%s after=%s", original.TrackID, renamed.TrackID)
	}
	if err := os.Remove(renamedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(duplicatePath); err != nil {
		t.Fatal(err)
	}
	waitForCoordinator(t, 15*time.Second, func() bool {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM media_locations WHERE root_id=$1 AND availability='available'`, root.ID).Scan(&count); err != nil {
			return false
		}
		return count == 0
	})
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("first coordinator did not stop")
	}

	afterRestart := filepath.Join(rootPath, "after-restart.wav")
	writeFile(t, afterRestart, fixtureWAV())
	restarted, stopRestarted, restartedDone := startIntegrationCoordinator(t, s)
	waitForCoordinator(t, 15*time.Second, func() bool {
		status := restarted.Status()
		return status.ScansSucceeded >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && !status.GlobalSweep
	})
	waitLocationState(t, pool, root.ID, "after-restart.wav", "available")
	stopRestarted()
	select {
	case <-restartedDone:
	case <-time.After(10 * time.Second):
		t.Fatal("restarted coordinator did not stop")
	}
}

type swappableCoordinatorStore struct {
	mu    sync.RWMutex
	store *storage.Store
}

func (s *swappableCoordinatorStore) current() *storage.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store
}
func (s *swappableCoordinatorStore) replace(store *storage.Store) {
	s.mu.Lock()
	s.store = store
	s.mu.Unlock()
}
func (s *swappableCoordinatorStore) ListRootsForReconciliation(ctx context.Context) ([]storage.LibraryRoot, error) {
	return s.current().ListRootsForReconciliation(ctx)
}
func (s *swappableCoordinatorStore) ListRootsAfter(ctx context.Context, after string, limit int) ([]storage.LibraryRoot, error) {
	return s.current().ListRootsAfter(ctx, after, limit)
}

type swappableScanner struct {
	mu      sync.RWMutex
	scanner *Scanner
}

func (s *swappableScanner) current() *Scanner {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scanner
}
func (s *swappableScanner) replace(scanner *Scanner) {
	s.mu.Lock()
	s.scanner = scanner
	s.mu.Unlock()
}
func (s *swappableScanner) Scan(ctx context.Context, rootID string) (ScanResult, error) {
	return s.current().Scan(ctx, rootID)
}

func TestM16WatcherIncompleteTraversalPreservesThousandLocationsAndUserState(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	rootPath := testWorkspaceDir(t)
	album := filepath.Join(rootPath, "album")
	for i := 0; i < 1000; i++ {
		writeFile(t, filepath.Join(album, fmt.Sprintf("track-%04d.wav", i)), fixtureWAV())
	}
	root := addRoot(t, s, rootPath, "watcher destructive safety")
	scanner := testScanner(s)
	first, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || !first.AbsenceReconciled || first.LocationsAdded != 1000 {
		t.Fatalf("initial catalog: %#v %v", first, err)
	}
	var trackID string
	if err := pool.QueryRow(context.Background(), "SELECT id FROM tracks").Scan(&trackID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"INSERT INTO favorite_tracks(track_id) VALUES($1)",
		"INSERT INTO playlists(id,name) VALUES('pl_m16_safety',$1)",
		"INSERT INTO playlist_items(id,playlist_id,track_id,position) VALUES('pli_m16_safety','pl_m16_safety',$1,0)",
		"INSERT INTO queue_items(id,track_id,position) VALUES('qi_m16_safety',$1,0)",
		`INSERT INTO playback_sessions(id,track_id,client_instance_id,meaningful_at,listened_ms)
		VALUES('00000000-0000-4000-8000-000000000091',$1,'00000000-0000-4000-8000-000000000092',now(),30000)`,
	} {
		if _, err := pool.Exec(context.Background(), statement, trackID); err != nil {
			t.Fatal(err)
		}
	}
	var deny atomic.Bool
	scanner.openDir = func(r *os.Root, name string) (*os.File, error) {
		if name == "album" && deny.Load() {
			return nil, os.ErrPermission
		}
		return r.Open(name)
	}
	watcher := newCoordinatorFakeWatcher()
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: s, Scanner: scanner, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil },
		Debounce:       20 * time.Millisecond, MaxDebounce: 80 * time.Millisecond, MinFollowupInterval: 20 * time.Millisecond,
		RetryMinimum: 500 * time.Millisecond, RetryMaximum: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("coordinator failed to stop")
		}
	}()
	waitForCoordinator(t, 20*time.Second, func() bool {
		status := coordinator.Status()
		return status.ScansSucceeded >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && !status.GlobalSweep
	})
	for i := 0; i < 900; i++ {
		if err := os.Remove(filepath.Join(album, fmt.Sprintf("track-%04d.wav", i))); err != nil {
			t.Fatal(err)
		}
	}
	deny.Store(true)
	watcher.events <- fsnotify.Event{Name: album, Op: fsnotify.Write}
	waitForCoordinator(t, 20*time.Second, func() bool {
		status := coordinator.Status()
		return status.ScansFailed >= 1 && len(status.Roots) == 1 && status.Roots[0].Dirty && !status.Roots[0].AbsenceReconciled
	})
	var available, unavailable int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE availability='available'),count(*) FILTER (WHERE availability='unavailable') FROM media_locations WHERE root_id=$1`, root.ID).Scan(&available, &unavailable); err != nil {
		t.Fatal(err)
	}
	if available != 1000 || unavailable != 0 {
		t.Fatalf("incomplete watcher scan inferred removal: available=%d unavailable=%d", available, unavailable)
	}
	deny.Store(false)
	watcher.events <- fsnotify.Event{Name: album, Op: fsnotify.Write}
	waitForCoordinator(t, 30*time.Second, func() bool {
		status := coordinator.Status()
		if status.ScansSucceeded < 2 || status.Roots[0].Dirty || !status.Roots[0].AbsenceReconciled {
			return false
		}
		return pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE availability='available'),count(*) FILTER (WHERE availability='unavailable') FROM media_locations WHERE root_id=$1`, root.ID).Scan(&available, &unavailable) == nil && available == 100 && unavailable == 900
	})
	var favorite, playlist, queue, history int
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM favorite_tracks WHERE track_id=$1),
		(SELECT count(*) FROM playlist_items WHERE track_id=$1),
		(SELECT count(*) FROM queue_items WHERE track_id=$1),
		(SELECT count(*) FROM playback_sessions WHERE track_id=$1 AND meaningful_at IS NOT NULL)`, trackID).Scan(&favorite, &playlist, &queue, &history); err != nil {
		t.Fatal(err)
	}
	if favorite != 1 || playlist != 1 || queue != 1 || history != 1 {
		t.Fatalf("watcher reconciliation changed user intent: favorite=%d playlist=%d queue=%d history=%d", favorite, playlist, queue, history)
	}
}

func TestM16DatabaseInterruptionRetainsDirtyRootAndRecovers(t *testing.T) {
	s, pool, dsn := isolatedLibraryStore(t)
	rootPath := testWorkspaceDir(t)
	root := addRoot(t, s, rootPath, "database recovery")
	storeProxy := &swappableCoordinatorStore{store: s}
	scannerProxy := &swappableScanner{scanner: testScanner(s)}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: storeProxy, Scanner: scannerProxy, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		RetryMinimum: 100 * time.Millisecond, RetryMaximum: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	waitForCoordinator(t, 15*time.Second, func() bool {
		status := coordinator.Status()
		return status.ScansSucceeded >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && !status.GlobalSweep
	})

	// Closing the scanner's real PostgreSQL pool injects an outage at the next
	// event-triggered scan. The watcher and root configuration remain alive.
	s.Close()
	path := filepath.Join(rootPath, "after-database-outage.wav")
	writeFile(t, path, fixtureWAV())
	waitForCoordinator(t, 5*time.Second, func() bool {
		status := coordinator.Status()
		return status.DatabaseState == "degraded" && status.ScansFailed > 0 && len(status.Roots) == 1 && status.Roots[0].Dirty
	})

	reopened, err := storage.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Ready(context.Background()); err != nil {
		t.Fatalf("PostgreSQL recovery readiness: %v", err)
	}
	storeProxy.replace(reopened)
	scannerProxy.replace(testScanner(reopened))
	waitForCoordinator(t, 10*time.Second, func() bool {
		status := coordinator.Status()
		return status.DatabaseState == "available" && len(status.Roots) == 1 && !status.Roots[0].Dirty
	})
	waitLocationState(t, pool, root.ID, "after-database-outage.wav", "available")
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("database recovery coordinator did not stop")
	}
}

func startIntegrationCoordinator(t *testing.T, store *storage.Store) (*Coordinator, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: store, Scanner: testScanner(store), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	return coordinator, cancel, done
}

func waitLocationState(t *testing.T, pool *pgxpool.Pool, rootID, relativePath, availability string) testLocation {
	t.Helper()
	var result testLocation
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(), `SELECT ml.id,ml.relative_path,ml.media_object_id,mo.track_id,ml.availability,ml.unavailable_reason
			FROM media_locations ml JOIN media_objects mo ON mo.id=ml.media_object_id WHERE ml.root_id=$1 AND ml.relative_path=$2 ORDER BY ml.id LIMIT 1`, rootID, relativePath).
			Scan(&result.ID, &result.Path, &result.ObjectID, &result.TrackID, &result.Availability, &result.Reason)
		if err == nil && result.Availability == availability {
			return result
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("location did not converge to requested state for root %s", rootID)
	return testLocation{}
}
