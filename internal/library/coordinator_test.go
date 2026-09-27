package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"resonance/internal/storage"
)

type coordinatorMemoryStore struct {
	mu    sync.Mutex
	roots []storage.LibraryRoot
}

func (s *coordinatorMemoryStore) ListRoots(context.Context) ([]storage.LibraryRoot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.LibraryRoot(nil), s.roots...), nil
}

func (s *coordinatorMemoryStore) ListRootsForReconciliation(ctx context.Context) ([]storage.LibraryRoot, error) {
	return s.ListRoots(ctx)
}

func (s *coordinatorMemoryStore) ListRootsAfter(_ context.Context, after string, limit int) ([]storage.LibraryRoot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]storage.LibraryRoot, 0, limit)
	for _, root := range s.roots {
		if root.ID > after {
			result = append(result, root)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

type coordinatorFakeWatcher struct {
	events   chan fsnotify.Event
	errors   chan error
	mu       sync.Mutex
	paths    map[string]bool
	addErr   error
	closed   sync.Once
	closedCh chan struct{}
}

func newCoordinatorFakeWatcher() *coordinatorFakeWatcher {
	return &coordinatorFakeWatcher{events: make(chan fsnotify.Event, 2048), errors: make(chan error, 8), paths: make(map[string]bool), closedCh: make(chan struct{})}
}

func (w *coordinatorFakeWatcher) Add(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.addErr != nil {
		return w.addErr
	}
	w.paths[watchPathKey(path)] = true
	return nil
}
func (w *coordinatorFakeWatcher) Remove(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.paths, watchPathKey(path))
	return nil
}
func (w *coordinatorFakeWatcher) Close() error {
	w.closed.Do(func() { close(w.events); close(w.errors); close(w.closedCh) })
	return nil
}
func (w *coordinatorFakeWatcher) Events() <-chan fsnotify.Event { return w.events }
func (w *coordinatorFakeWatcher) Errors() <-chan error          { return w.errors }

type coordinatorFakeScanner struct {
	mu      sync.Mutex
	calls   int
	results []ScanResult
	errs    []error
}

type cancelableCoordinatorScanner struct {
	started chan struct{}
	stopped chan struct{}
}

func (s *cancelableCoordinatorScanner) Scan(ctx context.Context, rootID string) (ScanResult, error) {
	close(s.started)
	<-ctx.Done()
	close(s.stopped)
	return ScanResult{RootID: rootID, Status: "canceled", ErrorCode: "canceled"}, ctx.Err()
}

func (s *coordinatorFakeScanner) Scan(_ context.Context, rootID string) (ScanResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	result := ScanResult{RunID: "fake-run", RootID: rootID, Status: "succeeded", WatchedDirectories: []string{"."}, ScanCounts: storage.ScanCounts{TraversalComplete: true, ObservationsApplied: true, AbsenceReconciled: true}}
	if s.calls <= len(s.results) {
		result = s.results[s.calls-1]
		result.RunID = "fake-run"
		result.RootID = rootID
		result.WatchedDirectories = []string{"."}
	}
	var err error
	if s.calls <= len(s.errs) {
		err = s.errs[s.calls-1]
	}
	return result, err
}

func (s *coordinatorFakeScanner) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestCoordinatorCoalescesRootEventsAndRetriesPartialAuthority(t *testing.T) {
	rootPath := t.TempDir()
	watcher := newCoordinatorFakeWatcher()
	store := &coordinatorMemoryStore{roots: []storage.LibraryRoot{{
		ID: "00000000-0000-4000-8000-000000000001", Name: "test", Enabled: true,
		VerificationState: "verified", CanonicalPath: rootPath,
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{1}},
	}}}
	scanner := &coordinatorFakeScanner{results: []ScanResult{
		{Status: "succeeded", ScanCounts: storage.ScanCounts{TraversalComplete: true, AbsenceReconciled: true}},
		{Status: "partial", ScanCounts: storage.ScanCounts{TraversalComplete: false, AbsenceReconciled: false}},
	}}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: store, Scanner: scanner, WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil },
		Logger:   quietCoordinatorLogger(),
		Debounce: 15 * time.Millisecond, MaxDebounce: 2 * time.Second, MinFollowupInterval: 15 * time.Millisecond,
		ConfigRefreshInterval: time.Hour, PeriodicInterval: time.Hour, RetryMinimum: 30 * time.Millisecond, RetryMaximum: 60 * time.Millisecond,
		WatcherRetryMinimum: 10 * time.Millisecond, WatcherRetryMaximum: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	waitForCoordinator(t, 2*time.Second, func() bool {
		status := coordinator.Status()
		return scanner.Calls() >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && !status.GlobalSweep
	})
	for i := 0; i < 10000; i++ {
		watcher.events <- fsnotify.Event{Name: filepath.Join(rootPath, "track.wav"), Op: fsnotify.Write}
	}
	waitForCoordinator(t, 2*time.Second, func() bool { return scanner.Calls() >= 2 })
	waitForCoordinator(t, 2*time.Second, func() bool { return scanner.Calls() >= 3 && !coordinator.Status().Roots[0].Dirty })
	time.Sleep(100 * time.Millisecond)
	if got := scanner.Calls(); got != 3 {
		t.Fatalf("event storm escaped root coalescing/retry: scans=%d want=3", got)
	}
	if got := coordinator.Status().EventsObserved; got != 10000 {
		t.Fatalf("event counter=%d want=10000", got)
	}
	cancel()
	<-done
}

func TestCoordinatorOverflowRequestsSweepAndRecreatesWatcher(t *testing.T) {
	rootPath := t.TempDir()
	firstWatcher := newCoordinatorFakeWatcher()
	watchers := []*coordinatorFakeWatcher{firstWatcher}
	var factoryMu sync.Mutex
	factory := func() (DirectoryWatcher, error) {
		factoryMu.Lock()
		defer factoryMu.Unlock()
		if fakeWatcherClosed(watchers[len(watchers)-1]) {
			watchers = append(watchers, newCoordinatorFakeWatcher())
		}
		return watchers[len(watchers)-1], nil
	}
	store := &coordinatorMemoryStore{roots: []storage.LibraryRoot{{
		ID: "00000000-0000-4000-8000-000000000002", Enabled: true,
		VerificationState: "verified", CanonicalPath: rootPath,
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{2}},
	}}}
	scanner := &coordinatorFakeScanner{}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: store, Scanner: scanner, WatcherFactory: factory,
		Logger:   quietCoordinatorLogger(),
		Debounce: 10 * time.Millisecond, MaxDebounce: 30 * time.Millisecond, MinFollowupInterval: 10 * time.Millisecond,
		ConfigRefreshInterval: time.Hour, PeriodicInterval: time.Hour, RetryMinimum: 20 * time.Millisecond, RetryMaximum: 40 * time.Millisecond,
		WatcherRetryMinimum: 10 * time.Millisecond, WatcherRetryMaximum: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	waitForCoordinator(t, 2*time.Second, func() bool {
		status := coordinator.Status()
		return scanner.Calls() >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && !status.GlobalSweep
	})
	firstWatcher.errors <- fsnotify.ErrEventOverflow
	waitForCoordinator(t, 2*time.Second, func() bool {
		factoryMu.Lock()
		defer factoryMu.Unlock()
		return len(watchers) >= 2 && scanner.Calls() >= 2 && coordinator.Status().WatcherState == "running"
	})
	if status := coordinator.Status(); status.Overflows != 1 || status.LastSweepReason != "watcher_overflow" {
		t.Fatalf("overflow recovery status: %#v", status)
	}
	factoryMu.Lock()
	secondWatcher := watchers[len(watchers)-1]
	factoryMu.Unlock()
	if err := secondWatcher.Close(); err != nil {
		t.Fatal(err)
	}
	waitForCoordinator(t, 2*time.Second, func() bool {
		factoryMu.Lock()
		count := len(watchers)
		factoryMu.Unlock()
		status := coordinator.Status()
		return count >= 3 && scanner.Calls() >= 3 && status.WatcherState == "running"
	})
	if reason := coordinator.Status().LastSweepReason; reason != "watcher_event_channel_closed" && reason != "watcher_error_channel_closed" {
		t.Fatalf("closed watcher channels did not request a sweep: %s", reason)
	}
	cancel()
	<-done
}

func fakeWatcherClosed(w *coordinatorFakeWatcher) bool {
	select {
	case <-w.closedCh:
		return true
	default:
		return false
	}
}

func waitForCoordinator(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("coordinator condition was not reached")
}

func waitCoordinatorState(t *testing.T, coordinator *Coordinator, scanner *coordinatorFakeScanner, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("coordinator condition was not reached: scans=%d status=%#v", scanner.Calls(), coordinator.Status())
}

func TestCoordinatorStatusContainsNoFilesystemPaths(t *testing.T) {
	rootPath := filepath.Join(os.TempDir(), "private-root-path")
	store := &coordinatorMemoryStore{}
	scanner := &coordinatorFakeScanner{}
	coordinator, err := NewCoordinator(CoordinatorOptions{Store: store, Scanner: scanner, Logger: quietCoordinatorLogger()})
	if err != nil {
		t.Fatal(err)
	}
	coordinator.setRootStatus("00000000-0000-4000-8000-000000000003", func(status *RootRuntimeStatus) {
		status.ErrorCode = "root_unavailable"
	})
	encoded, err := json.Marshal(coordinator.Status())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), rootPath) || strings.Contains(string(encoded), "private-root-path") {
		t.Fatalf("filesystem path escaped into diagnostics: %s", encoded)
	}
}

func TestCoordinatorWatcherStartupFailureStillScansAndRecovers(t *testing.T) {
	rootPath := t.TempDir()
	watcher := newCoordinatorFakeWatcher()
	var factoryMu sync.Mutex
	factoryCalls := 0
	store := &coordinatorMemoryStore{roots: []storage.LibraryRoot{{
		ID: "00000000-0000-4000-8000-000000000004", Enabled: true,
		VerificationState: "verified", CanonicalPath: rootPath,
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{4}},
	}}}
	scanner := &coordinatorFakeScanner{}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: store, Scanner: scanner, Logger: quietCoordinatorLogger(),
		WatcherFactory: func() (DirectoryWatcher, error) {
			factoryMu.Lock()
			defer factoryMu.Unlock()
			factoryCalls++
			if factoryCalls == 1 {
				return nil, errors.New("injected watcher startup failure")
			}
			return watcher, nil
		},
		Debounce: 10 * time.Millisecond, MaxDebounce: 30 * time.Millisecond, MinFollowupInterval: 10 * time.Millisecond,
		ConfigRefreshInterval: time.Hour, PeriodicInterval: time.Hour, RetryMinimum: 20 * time.Millisecond, RetryMaximum: 40 * time.Millisecond,
		WatcherRetryMinimum: 10 * time.Millisecond, WatcherRetryMaximum: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	waitForCoordinator(t, 2*time.Second, func() bool {
		status := coordinator.Status()
		return scanner.Calls() >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && status.WatcherState == "running"
	})
	factoryMu.Lock()
	gotFactoryCalls := factoryCalls
	factoryMu.Unlock()
	watcher.mu.Lock()
	gotWatches := len(watcher.paths)
	watcher.mu.Unlock()
	if gotFactoryCalls < 2 || gotWatches == 0 {
		t.Fatalf("watcher recovery did not restore coverage: attempts=%d watches=%d", gotFactoryCalls, gotWatches)
	}
	cancel()
	<-done
}

func TestCoordinatorPermissionDeniedWatchDegradesCoverageButKeepsScanning(t *testing.T) {
	rootPath := t.TempDir()
	watcher := newCoordinatorFakeWatcher()
	watcher.addErr = os.ErrPermission
	store := &coordinatorMemoryStore{roots: []storage.LibraryRoot{{
		ID: "00000000-0000-4000-8000-000000000007", Enabled: true,
		VerificationState: "verified", CanonicalPath: rootPath,
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{7}},
	}}}
	scanner := &coordinatorFakeScanner{}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: store, Scanner: scanner, Logger: quietCoordinatorLogger(), WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil },
		Debounce: 10 * time.Millisecond, MaxDebounce: 30 * time.Millisecond, MinFollowupInterval: 10 * time.Millisecond,
		ConfigRefreshInterval: time.Hour, PeriodicInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	waitForCoordinator(t, 2*time.Second, func() bool {
		status := coordinator.Status()
		return scanner.Calls() >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && status.Roots[0].AbsenceReconciled
	})
	status := coordinator.Status()
	if status.WatcherState != "running" || status.Roots[0].WatchState != "degraded" || status.Roots[0].WatchErrorCode == "" {
		t.Fatalf("watch permission failure was not reported as degraded coverage: %#v", status)
	}
	cancel()
	<-done
}

func TestCoordinatorWatchStatusRecoversAfterLossAndCoverageRepair(t *testing.T) {
	rootPath := t.TempDir()
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000aa", Enabled: true,
		VerificationState: "verified", CanonicalPath: rootPath,
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xaa}}}
	first, second := newCoordinatorFakeWatcher(), newCoordinatorFakeWatcher()
	created := 0
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: &coordinatorMemoryStore{roots: []storage.LibraryRoot{root}}, Scanner: &coordinatorFakeScanner{}, Logger: quietCoordinatorLogger(),
		WatcherFactory: func() (DirectoryWatcher, error) {
			created++
			if created == 1 {
				return first, nil
			}
			return second, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ensureWatcher(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.refreshRoots(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := coordinator.Status().Roots[0].WatchState; got != "watching" {
		t.Fatalf("initial coverage=%q", got)
	}
	coordinator.watcherLost("watcher_overflow")
	status := coordinator.Status()
	if status.WatcherState != "recovering" || status.Roots[0].WatchState != "degraded" || status.Roots[0].WatchErrorCode != "watcher_overflow" {
		t.Fatalf("lost watcher still claimed root coverage: %#v", status)
	}
	if err := coordinator.ensureWatcher(); err != nil {
		t.Fatal(err)
	}
	coordinator.updateRootWatches(root, []string{"."}, true)
	status = coordinator.Status()
	if status.WatcherState != "running" || status.Roots[0].WatchState != "watching" || status.Roots[0].WatchErrorCode != "" {
		t.Fatalf("restored coverage stayed degraded: %#v", status)
	}
	coordinator.closeWatcher()
}

func TestCoordinatorEpochRetainsEventsAcrossPublication(t *testing.T) {
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000ab", Enabled: true,
		VerificationState: "verified", Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xab}}}
	c, err := NewCoordinator(CoordinatorOptions{Store: &coordinatorMemoryStore{}, Scanner: &coordinatorFakeScanner{}, Logger: quietCoordinatorLogger(),
		Debounce: time.Millisecond, MaxDebounce: time.Second, MinFollowupInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c.roots[root.ID] = root
	c.ensureRootStatus(root.ID, root.VerificationState)
	now := time.Now()
	c.markDirty(root.ID, "watcher", now, true)
	d := c.dirty[root.ID]
	d.inProgress, d.scanEpoch = true, d.epoch
	c.activeRoot = root.ID
	// A notification received after the worker captured its epoch, including
	// one during final publication, must survive the successful result.
	c.markDirty(root.ID, "watcher", now.Add(time.Millisecond), true)
	authoritative := ScanResult{RunID: "", Status: "succeeded", ScanCounts: storage.ScanCounts{TraversalComplete: true, AbsenceReconciled: true}}
	c.finishScan(scanResult{rootID: root.ID, result: authoritative, ended: now.Add(2 * time.Millisecond)})
	if c.dirty[root.ID] == nil || c.dirty[root.ID].epoch == c.dirty[root.ID].scanEpoch {
		t.Fatal("newer event was cleared by the prior scan")
	}
	d = c.dirty[root.ID]
	d.inProgress, d.scanEpoch = true, d.epoch
	c.activeRoot = root.ID
	c.finishScan(scanResult{rootID: root.ID, result: authoritative, ended: now.Add(3 * time.Millisecond)})
	if c.dirty[root.ID] != nil {
		t.Fatal("authoritative follow-up left stale dirtiness")
	}
	c.markDirty(root.ID, "watcher", now.Add(4*time.Millisecond), true)
	if c.dirty[root.ID] == nil {
		t.Fatal("event after publication was lost")
	}
}

func TestCoordinatorRemovedOrDisabledRootDoesNotMisreportWatchState(t *testing.T) {
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000ac", Enabled: false,
		VerificationState: "verified", Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xac}}}
	c, err := NewCoordinator(CoordinatorOptions{Store: &coordinatorMemoryStore{}, Scanner: &coordinatorFakeScanner{}, Logger: quietCoordinatorLogger()})
	if err != nil {
		t.Fatal(err)
	}
	c.roots[root.ID] = root
	c.ensureRootStatus(root.ID, root.VerificationState)
	c.finishScan(scanResult{rootID: root.ID, result: ScanResult{Status: "failed", ErrorCode: "root_unavailable"}, err: os.ErrNotExist})
	if got := c.Status().Roots[0].WatchState; got != "disabled" {
		t.Fatalf("disabled root was reported as %q", got)
	}
	delete(c.roots, root.ID)
	c.deleteRootStatus(root.ID)
	c.finishScan(scanResult{rootID: root.ID, result: ScanResult{Status: "canceled"}, err: context.Canceled})
	if len(c.Status().Roots) != 0 {
		t.Fatal("removed root was resurrected by a late scan result")
	}
}

func TestCoordinatorScanResultImmediatelyDropsOfflineAndDisabledWatches(t *testing.T) {
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000b1", Enabled: true,
		VerificationState: "verified", CanonicalPath: t.TempDir(),
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xb1}}}
	watcher := newCoordinatorFakeWatcher()
	c, err := NewCoordinator(CoordinatorOptions{Store: &coordinatorMemoryStore{}, Scanner: &coordinatorFakeScanner{}, Logger: quietCoordinatorLogger(),
		WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil }})
	if err != nil {
		t.Fatal(err)
	}
	c.roots[root.ID] = root
	if err := c.ensureWatcher(); err != nil {
		t.Fatal(err)
	}
	if c.watchCount != 1 {
		t.Fatal("root watch was not installed")
	}
	c.finishScan(scanResult{rootID: root.ID, result: ScanResult{RunID: "failed", Status: "failed", ErrorCode: "root_unavailable"}, err: storage.ErrRootUnavailable})
	if c.watchCount != 0 || c.Status().Roots[0].WatchState != "offline" {
		t.Fatalf("offline root retained or falsely reported watch coverage: %#v", c.Status())
	}
	c.registerWatch(root.ID, root.CanonicalPath)
	c.finishScan(scanResult{rootID: root.ID, result: ScanResult{RunID: "disabled", Status: "failed", ErrorCode: "root_disabled"}, err: storage.ErrRootDisabled})
	if c.watchCount != 0 || c.roots[root.ID].Enabled || c.Status().Roots[0].WatchState != "disabled" {
		t.Fatalf("disabled root retained watch or dirty state: %#v", c.Status())
	}
	c.closeWatcher()
}

func TestCoordinatorHardCapsExposeDegradationAndGlobalSweep(t *testing.T) {
	watcher := newCoordinatorFakeWatcher()
	c, err := NewCoordinator(CoordinatorOptions{Store: &coordinatorMemoryStore{}, Scanner: &coordinatorFakeScanner{}, Logger: quietCoordinatorLogger(),
		WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil }, MaximumDirtyRoots: 256, MaximumWatches: 4096})
	if err != nil {
		t.Fatal(err)
	}
	rootPath := t.TempDir()
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000ad", Enabled: true,
		VerificationState: "verified", CanonicalPath: rootPath,
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xad}}}
	c.roots[root.ID] = root
	if err := c.ensureWatcher(); err != nil {
		t.Fatal(err)
	}
	c.registerWatch(root.ID, rootPath)
	directories := make([]string, 4096)
	for i := range directories {
		directories[i] = fmt.Sprintf("album-%04d", i)
	}
	c.updateRootWatches(root, directories, true)
	if c.watchCount != 4096 || c.Status().Roots[0].WatchState != "degraded" || c.Status().Roots[0].WatchErrorCode != "watch_coverage_incomplete" {
		t.Fatalf("4096-directory cap was silent or exceeded: watches=%d status=%#v", c.watchCount, c.Status())
	}
	for i := 0; i < 257; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		c.roots[id] = storage.LibraryRoot{ID: id, Enabled: true, VerificationState: "verified", Identity: root.Identity}
		c.markDirty(id, "watcher", time.Now(), true)
	}
	if len(c.dirty) != 256 || !c.Status().GlobalSweep {
		t.Fatalf("256-root dirty cap was silent or exceeded: dirty=%d status=%#v", len(c.dirty), c.Status())
	}
	c.closeWatcher()
}

func TestCoordinatorShutdownCancelsActiveScanAndClosesWatcher(t *testing.T) {
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000ae", Enabled: true,
		VerificationState: "verified", CanonicalPath: t.TempDir(),
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xae}}}
	watcher := newCoordinatorFakeWatcher()
	scanner := &cancelableCoordinatorScanner{started: make(chan struct{}), stopped: make(chan struct{})}
	c, err := NewCoordinator(CoordinatorOptions{Store: &coordinatorMemoryStore{roots: []storage.LibraryRoot{root}}, Scanner: scanner,
		Logger: quietCoordinatorLogger(), WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil },
		Debounce: time.Millisecond, MaxDebounce: time.Second, ShutdownWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	select {
	case <-scanner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("startup scan did not begin")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not stop after cancellation")
	}
	select {
	case <-scanner.stopped:
	default:
		t.Fatal("active scan was not canceled")
	}
	if !fakeWatcherClosed(watcher) || c.Status().Status != "stopped" {
		t.Fatalf("shutdown retained watcher or running status: %#v", c.Status())
	}
}

func TestCoordinatorManualScanLeaseContentionRetries(t *testing.T) {
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000af", Enabled: true,
		VerificationState: "verified", CanonicalPath: t.TempDir(),
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xaf}}}
	scanner := &coordinatorFakeScanner{errs: []error{storage.ErrScanRunning}}
	c, err := NewCoordinator(CoordinatorOptions{Store: &coordinatorMemoryStore{roots: []storage.LibraryRoot{root}}, Scanner: scanner,
		Logger: quietCoordinatorLogger(), WatcherFactory: func() (DirectoryWatcher, error) { return newCoordinatorFakeWatcher(), nil },
		Debounce: time.Millisecond, MaxDebounce: time.Second, RetryMinimum: 20 * time.Millisecond, RetryMaximum: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	waitCoordinatorState(t, c, scanner, 3*time.Second, func() bool {
		status := c.Status()
		return scanner.Calls() >= 2 && status.ScansFailed >= 1 && status.ScansSucceeded >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty
	})
	cancel()
	<-done
}

func TestCoordinatorPeriodicSweepRunsWithoutAnyWatcherEvent(t *testing.T) {
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000b0", Enabled: true,
		VerificationState: "verified", CanonicalPath: t.TempDir(),
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xb0}}}
	scanner := &coordinatorFakeScanner{}
	c, err := NewCoordinator(CoordinatorOptions{Store: &coordinatorMemoryStore{roots: []storage.LibraryRoot{root}}, Scanner: scanner,
		Logger: quietCoordinatorLogger(), WatcherFactory: func() (DirectoryWatcher, error) { return newCoordinatorFakeWatcher(), nil },
		Debounce: time.Millisecond, MaxDebounce: time.Second, PeriodicInterval: 150 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	waitCoordinatorState(t, c, scanner, 3*time.Second, func() bool {
		status := c.Status()
		return scanner.Calls() >= 2 && status.ScansSucceeded >= 2 && status.LastSweepReason == "periodic"
	})
	if c.Status().EventsObserved != 0 {
		t.Fatal("periodic convergence relied on a watcher event")
	}
	cancel()
	<-done
}

func TestCoordinatorRootOfflineThenMatchingReturnRetries(t *testing.T) {
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000b2", Enabled: true,
		VerificationState: "verified", CanonicalPath: t.TempDir(),
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xb2}}}
	scanner := &coordinatorFakeScanner{results: []ScanResult{{Status: "failed", ErrorCode: "root_unavailable"}}, errs: []error{storage.ErrRootUnavailable}}
	watcher := newCoordinatorFakeWatcher()
	c, err := NewCoordinator(CoordinatorOptions{Store: &coordinatorMemoryStore{roots: []storage.LibraryRoot{root}}, Scanner: scanner,
		Logger: quietCoordinatorLogger(), WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil },
		Debounce: time.Millisecond, MaxDebounce: time.Second, RetryMinimum: 500 * time.Millisecond, RetryMaximum: 500 * time.Millisecond,
		ConfigRefreshInterval: time.Hour, PeriodicInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	waitCoordinatorState(t, c, scanner, 3*time.Second, func() bool {
		status := c.Status()
		return status.ScansFailed >= 1 && len(status.Roots) == 1 && status.Roots[0].Dirty && status.Roots[0].WatchState == "offline" && c.watchCount == 0
	})
	waitCoordinatorState(t, c, scanner, 3*time.Second, func() bool {
		status := c.Status()
		return status.ScansSucceeded >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && status.Roots[0].WatchState == "watching"
	})
	cancel()
	<-done
}

func TestCoordinatorConfigurationDisableCancelsActiveScan(t *testing.T) {
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-0000000000b3", Enabled: true,
		VerificationState: "verified", CanonicalPath: t.TempDir(),
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{0xb3}}}
	store := &coordinatorMemoryStore{roots: []storage.LibraryRoot{root}}
	scanner := &cancelableCoordinatorScanner{started: make(chan struct{}), stopped: make(chan struct{})}
	c, err := NewCoordinator(CoordinatorOptions{Store: store, Scanner: scanner, Logger: quietCoordinatorLogger(),
		WatcherFactory:        func() (DirectoryWatcher, error) { return newCoordinatorFakeWatcher(), nil },
		ConfigRefreshInterval: 100 * time.Millisecond, PeriodicInterval: time.Hour, Debounce: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	select {
	case <-scanner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not start before disable")
	}
	store.mu.Lock()
	store.roots[0].Enabled = false
	store.mu.Unlock()
	waitForCoordinator(t, 3*time.Second, func() bool {
		status := c.Status()
		return len(status.Roots) == 1 && status.Roots[0].WatchState == "disabled" && !status.Roots[0].Dirty
	})
	select {
	case <-scanner.stopped:
	default:
		t.Fatal("disabling the root did not cancel its active scan")
	}
	cancel()
	<-done
}

func TestCoordinatorRetriesFailedScanAndRecovers(t *testing.T) {
	rootPath := t.TempDir()
	watcher := newCoordinatorFakeWatcher()
	store := &coordinatorMemoryStore{roots: []storage.LibraryRoot{{
		ID: "00000000-0000-4000-8000-000000000005", Enabled: true,
		VerificationState: "verified", CanonicalPath: rootPath,
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{5}},
	}}}
	scanner := &coordinatorFakeScanner{
		results: []ScanResult{{Status: "succeeded", ScanCounts: storage.ScanCounts{TraversalComplete: true, AbsenceReconciled: true}}, {Status: "failed", ErrorCode: "database_unavailable"}},
		errs:    []error{nil, errors.New("injected database interruption")},
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: store, Scanner: scanner, Logger: quietCoordinatorLogger(),
		WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil },
		Debounce:       10 * time.Millisecond, MaxDebounce: 30 * time.Millisecond, MinFollowupInterval: 10 * time.Millisecond,
		ConfigRefreshInterval: time.Hour, PeriodicInterval: time.Hour, RetryMinimum: 500 * time.Millisecond, RetryMaximum: 500 * time.Millisecond,
		WatcherRetryMinimum: 10 * time.Millisecond, WatcherRetryMaximum: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	waitForCoordinator(t, 2*time.Second, func() bool {
		status := coordinator.Status()
		return scanner.Calls() >= 1 && len(status.Roots) == 1 && !status.Roots[0].Dirty && !status.GlobalSweep
	})
	watcher.events <- fsnotify.Event{Name: filepath.Join(rootPath, "offline.wav"), Op: fsnotify.Write}
	waitCoordinatorState(t, coordinator, scanner, 2*time.Second, func() bool {
		status := coordinator.Status()
		return scanner.Calls() >= 2 && status.DatabaseState == "degraded" && len(status.Roots) == 1 && status.Roots[0].Dirty && status.Roots[0].ErrorCode == "database_unavailable"
	})
	waitCoordinatorState(t, coordinator, scanner, 2*time.Second, func() bool {
		status := coordinator.Status()
		return scanner.Calls() >= 3 && status.DatabaseState == "available" && len(status.Roots) == 1 && !status.Roots[0].Dirty
	})
	cancel()
	<-done
}

func TestCoordinatorDirtyCapacityUsesPagedFairSweep(t *testing.T) {
	parent := t.TempDir()
	roots := make([]storage.LibraryRoot, 7)
	for i := range roots {
		path := filepath.Join(parent, fmt.Sprintf("root-%d", i))
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		roots[i] = storage.LibraryRoot{
			ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), Enabled: true,
			VerificationState: "verified", CanonicalPath: path,
			Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{byte(i + 1)}},
		}
	}
	watcher := newCoordinatorFakeWatcher()
	store := &coordinatorMemoryStore{roots: roots}
	scanner := &coordinatorFakeScanner{}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Store: store, Scanner: scanner, Logger: quietCoordinatorLogger(), WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil },
		Debounce: 5 * time.Millisecond, MaxDebounce: 10 * time.Millisecond, MinFollowupInterval: 5 * time.Millisecond,
		ConfigRefreshInterval: time.Hour, PeriodicInterval: time.Hour, RetryMinimum: 10 * time.Millisecond, RetryMaximum: 20 * time.Millisecond,
		MaximumDirtyRoots: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	waitCoordinatorState(t, coordinator, scanner, 10*time.Second, func() bool {
		status := coordinator.Status()
		return scanner.Calls() == len(roots) && status.DirtyRoots == 0 && !status.GlobalSweep
	})
	if got := scanner.Calls(); got != len(roots) {
		t.Fatalf("paged sweep scans=%d roots=%d", got, len(roots))
	}
	cancel()
	<-done
}

func TestCoordinatorRefreshDisablesAndRemovesRootWatches(t *testing.T) {
	rootPath := t.TempDir()
	watcher := newCoordinatorFakeWatcher()
	root := storage.LibraryRoot{ID: "00000000-0000-4000-8000-000000000006", Enabled: true, VerificationState: "verified", CanonicalPath: rootPath,
		Identity: &storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{6}}}
	store := &coordinatorMemoryStore{roots: []storage.LibraryRoot{root}}
	coordinator, err := NewCoordinator(CoordinatorOptions{Store: store, Scanner: &coordinatorFakeScanner{}, Logger: quietCoordinatorLogger(), WatcherFactory: func() (DirectoryWatcher, error) { return watcher, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ensureWatcher(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.refreshRoots(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(watcher.paths) != 1 {
		t.Fatalf("initial root watch count=%d", len(watcher.paths))
	}
	store.mu.Lock()
	store.roots[0].Enabled = false
	store.mu.Unlock()
	if err := coordinator.refreshRoots(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(watcher.paths) != 0 || coordinator.Status().Roots[0].WatchState != "disabled" {
		t.Fatalf("disabled root retained watcher state: paths=%d status=%#v", len(watcher.paths), coordinator.Status().Roots[0])
	}
	store.mu.Lock()
	store.roots = nil
	store.mu.Unlock()
	if err := coordinator.refreshRoots(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(coordinator.Status().Roots) != 0 {
		t.Fatalf("removed root remained in diagnostics: %#v", coordinator.Status().Roots)
	}
	coordinator.closeWatcher()
}

func quietCoordinatorLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
