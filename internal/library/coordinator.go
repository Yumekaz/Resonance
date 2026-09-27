package library

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"resonance/internal/storage"
)

const (
	defaultMaxDirtyRoots = 256
	defaultMaxWatches    = 4096
	sweepPageSize        = 32
)

type rootPageStore interface {
	ListRootsForReconciliation(context.Context) ([]storage.LibraryRoot, error)
	ListRootsAfter(context.Context, string, int) ([]storage.LibraryRoot, error)
}

type scanEngine interface {
	Scan(context.Context, string) (ScanResult, error)
}

type WatcherFactory func() (DirectoryWatcher, error)

type CoordinatorOptions struct {
	Store                 rootPageStore
	Scanner               scanEngine
	WatcherFactory        WatcherFactory
	Logger                *slog.Logger
	Now                   func() time.Time
	Debounce              time.Duration
	MaxDebounce           time.Duration
	MinFollowupInterval   time.Duration
	ConfigRefreshInterval time.Duration
	PeriodicInterval      time.Duration
	RetryMinimum          time.Duration
	RetryMaximum          time.Duration
	WatcherRetryMinimum   time.Duration
	WatcherRetryMaximum   time.Duration
	MaximumDirtyRoots     int
	MaximumWatches        int
	ShutdownWait          time.Duration
}

type RootRuntimeStatus struct {
	ID                 string     `json:"id"`
	VerificationState  string     `json:"verification_state"`
	WatchState         string     `json:"watch_state"`
	Dirty              bool       `json:"dirty"`
	LastScanAt         *time.Time `json:"last_scan_at,omitempty"`
	LastScanAgeSeconds float64    `json:"last_scan_age_seconds,omitempty"`
	LastScanStatus     string     `json:"last_scan_status,omitempty"`
	AbsenceReconciled  bool       `json:"absence_reconciled"`
	RetryAt            *time.Time `json:"retry_at,omitempty"`
	ErrorCode          string     `json:"last_scan_error_code,omitempty"`
	WatchErrorCode     string     `json:"watch_error_code,omitempty"`
	EventsObserved     uint64     `json:"events_observed"`
	Retries            uint64     `json:"retries"`
}

type CoordinatorStatus struct {
	Status           string              `json:"status"`
	DatabaseState    string              `json:"database_state"`
	WatcherState     string              `json:"watcher_state"`
	WatcherError     string              `json:"watcher_error,omitempty"`
	DirtyRoots       int                 `json:"dirty_roots"`
	GlobalSweep      bool                `json:"global_sweep"`
	RootsTotal       int                 `json:"roots_total"`
	Roots            []RootRuntimeStatus `json:"roots"`
	RootsTruncated   bool                `json:"roots_truncated"`
	EventsObserved   uint64              `json:"events_observed"`
	Overflows        uint64              `json:"overflows"`
	ScansStarted     uint64              `json:"scans_started"`
	ScansSucceeded   uint64              `json:"scans_succeeded"`
	ScansFailed      uint64              `json:"scans_failed"`
	LastRefreshAt    *time.Time          `json:"last_refresh_at,omitempty"`
	LastSweepAt      *time.Time          `json:"last_sweep_at,omitempty"`
	LastSweepReason  string              `json:"last_sweep_reason,omitempty"`
	LastSweepCycleID uint64              `json:"last_sweep_cycle_id"`
}

type Coordinator struct {
	store          rootPageStore
	scanner        scanEngine
	watcherFactory WatcherFactory
	log            *slog.Logger
	now            func() time.Time
	debounce       time.Duration
	maxDebounce    time.Duration
	minFollowup    time.Duration
	configRefresh  time.Duration
	periodic       time.Duration
	retryMin       time.Duration
	retryMax       time.Duration
	watchRetryMin  time.Duration
	watchRetryMax  time.Duration
	maxDirty       int
	maxWatches     int
	shutdownWait   time.Duration

	// The following mutable state is owned by Run's single coordinator loop.
	roots          map[string]storage.LibraryRoot
	dirty          map[string]*dirtyRoot
	watchPaths     map[string]string
	rootWatchPaths map[string]map[string]struct{}
	watcher        DirectoryWatcher
	watchEvents    <-chan fsnotifyEvent
	watchErrors    <-chan error
	watchCount     int
	activeRoot     string
	scanCancel     context.CancelFunc
	workerJobs     chan scanJob
	workerResults  chan scanResult
	workerDone     chan struct{}
	cycle          uint64
	sweepCycle     uint64
	lastServedID   string

	sweepActive     bool
	sweepAgain      bool
	sweepCursor     string
	sweepPage       []storage.LibraryRoot
	sweepPageIndex  int
	sweepFetchAfter time.Time
	watchRetryAt    time.Time
	watchRetryDelay time.Duration
	watcherState    string
	watcherError    string

	statusMu sync.RWMutex
	status   CoordinatorStatus
}

// fsnotifyEvent is an alias kept behind this package boundary so tests can
// inject the same event shape without exposing platform paths in diagnostics.
type fsnotifyEvent = fsnotify.Event

type dirtyRoot struct {
	epoch       uint64
	firstDirty  time.Time
	lastEvent   time.Time
	dueAt       time.Time
	retryAt     time.Time
	retryDelay  time.Duration
	trigger     string
	inProgress  bool
	scanEpoch   uint64
	scanStarted time.Time
}

type scanJob struct {
	ctx     context.Context
	rootID  string
	cycle   uint64
	trigger string
}

type scanResult struct {
	rootID  string
	cycle   uint64
	result  ScanResult
	err     error
	started time.Time
	ended   time.Time
}

func NewCoordinator(options CoordinatorOptions) (*Coordinator, error) {
	if options.Store == nil || options.Scanner == nil {
		return nil, errors.New("coordinator requires storage and scanner")
	}
	if options.WatcherFactory == nil {
		options.WatcherFactory = newOSDirectoryWatcher
	}
	if options.Logger == nil {
		options.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	setDuration := func(value *time.Duration, fallback time.Duration) {
		if *value <= 0 {
			*value = fallback
		}
	}
	setDuration(&options.Debounce, 750*time.Millisecond)
	setDuration(&options.MaxDebounce, 5*time.Second)
	setDuration(&options.MinFollowupInterval, 750*time.Millisecond)
	setDuration(&options.ConfigRefreshInterval, 60*time.Second)
	setDuration(&options.PeriodicInterval, 30*time.Minute)
	setDuration(&options.RetryMinimum, 5*time.Second)
	setDuration(&options.RetryMaximum, 5*time.Minute)
	setDuration(&options.WatcherRetryMinimum, time.Second)
	setDuration(&options.WatcherRetryMaximum, time.Minute)
	setDuration(&options.ShutdownWait, 8*time.Second)
	if options.MaxDebounce < options.Debounce {
		options.MaxDebounce = options.Debounce
	}
	if options.RetryMaximum < options.RetryMinimum {
		options.RetryMaximum = options.RetryMinimum
	}
	if options.WatcherRetryMaximum < options.WatcherRetryMinimum {
		options.WatcherRetryMaximum = options.WatcherRetryMinimum
	}
	if options.MaximumDirtyRoots <= 0 {
		options.MaximumDirtyRoots = defaultMaxDirtyRoots
	}
	if options.MaximumDirtyRoots > defaultMaxDirtyRoots {
		options.MaximumDirtyRoots = defaultMaxDirtyRoots
	}
	if options.MaximumWatches <= 0 {
		options.MaximumWatches = defaultMaxWatches
	}
	if options.MaximumWatches > defaultMaxWatches {
		options.MaximumWatches = defaultMaxWatches
	}
	c := &Coordinator{
		store: options.Store, scanner: options.Scanner, watcherFactory: options.WatcherFactory,
		log: options.Logger, now: options.Now, debounce: options.Debounce, maxDebounce: options.MaxDebounce,
		minFollowup: options.MinFollowupInterval, configRefresh: options.ConfigRefreshInterval,
		periodic: options.PeriodicInterval, retryMin: options.RetryMinimum, retryMax: options.RetryMaximum,
		watchRetryMin: options.WatcherRetryMinimum, watchRetryMax: options.WatcherRetryMaximum,
		maxDirty: options.MaximumDirtyRoots, maxWatches: options.MaximumWatches, shutdownWait: options.ShutdownWait,
		roots: make(map[string]storage.LibraryRoot), dirty: make(map[string]*dirtyRoot),
		watchPaths: make(map[string]string), rootWatchPaths: make(map[string]map[string]struct{}),
		workerJobs: make(chan scanJob), workerResults: make(chan scanResult, 1), workerDone: make(chan struct{}),
		watcherState: "starting",
		status:       CoordinatorStatus{Status: "starting", DatabaseState: "unknown", WatcherState: "starting", Roots: []RootRuntimeStatus{}},
	}
	return c, nil
}

// Status returns a bounded path-free snapshot. Root identities and enrolled
// paths never leave storage/coordinator internals.
func (c *Coordinator) Status() CoordinatorStatus {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	out := c.status
	out.Roots = append([]RootRuntimeStatus(nil), c.status.Roots...)
	now := c.now()
	for i := range out.Roots {
		if out.Roots[i].LastScanAt != nil {
			out.Roots[i].LastScanAgeSeconds = max(0, now.Sub(*out.Roots[i].LastScanAt).Seconds())
		}
	}
	return out
}

func (c *Coordinator) Run(ctx context.Context) error {
	go c.runWorker()
	defer func() {
		if c.scanCancel != nil {
			c.scanCancel()
		}
		c.closeWatcher()
		close(c.workerJobs)
		select {
		case <-c.workerDone:
		case <-time.After(c.shutdownWait):
			c.log.Warn("reconcile_shutdown_timeout", "code", "scan_finalization_timeout")
		}
		c.watcherState = "stopped"
		c.setStatus(func(status *CoordinatorStatus) { status.Status = "stopped"; status.WatcherState = "stopped" })
	}()

	if err := c.ensureWatcher(); err != nil {
		c.log.Warn("watcher_unavailable", "code", "watcher_start_failed")
	}
	if err := c.refreshRoots(ctx); err != nil {
		c.log.Warn("root_configuration_unavailable", "code", "database_unavailable")
	}
	c.setStatus(func(status *CoordinatorStatus) { status.Status = "running" })
	c.requestSweep("startup")
	now := c.now()
	configTicker := time.NewTicker(c.configRefresh)
	defer configTicker.Stop()
	periodicTimer := time.NewTimer(c.periodic + c.periodicStagger())
	defer periodicTimer.Stop()
	loopTicker := time.NewTicker(100 * time.Millisecond)
	defer loopTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-c.watchEvents:
			if !ok {
				c.watcherLost("watcher_event_channel_closed")
				continue
			}
			c.handleEvent(event)
		case err, ok := <-c.watchErrors:
			if !ok {
				c.watcherLost("watcher_error_channel_closed")
				continue
			}
			code := "watcher_error"
			if isWatcherOverflow(err) {
				code = "watcher_overflow"
				c.incrementGlobalCounter(func(status *CoordinatorStatus) { status.Overflows = saturatingIncrement(status.Overflows) })
			}
			c.watcherLost(code)
		case result := <-c.workerResults:
			c.finishScan(result)
		case <-configTicker.C:
			if err := c.refreshRoots(ctx); err != nil {
				c.log.Warn("root_configuration_unavailable", "code", "database_unavailable")
			}
		case <-periodicTimer.C:
			c.requestSweep("periodic")
			periodicTimer.Reset(c.periodic + c.periodicStagger())
		case <-loopTicker.C:
			now = c.now()
			if c.watcher == nil && !c.watchRetryAt.IsZero() && !now.Before(c.watchRetryAt) {
				_ = c.ensureWatcher()
			}
			c.fillSweep(ctx, now)
			c.startDueScan(ctx, now)
		}
	}
}

func (c *Coordinator) runWorker() {
	defer close(c.workerDone)
	for job := range c.workerJobs {
		started := c.now()
		result, err := c.scanner.Scan(job.ctx, job.rootID)
		ended := c.now()
		select {
		case c.workerResults <- scanResult{rootID: job.rootID, cycle: job.cycle, result: result, err: err, started: started, ended: ended}:
		case <-job.ctx.Done():
			// The bounded result channel normally accepts this even during shutdown.
			select {
			case c.workerResults <- scanResult{rootID: job.rootID, cycle: job.cycle, result: result, err: err, started: started, ended: ended}:
			default:
			}
		}
	}
}

func (c *Coordinator) refreshRoots(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	roots, err := c.store.ListRootsForReconciliation(ctx)
	if err != nil {
		c.setStatus(func(status *CoordinatorStatus) { status.DatabaseState = "degraded" })
		return err
	}
	now := c.now()
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		seen[root.ID] = struct{}{}
		previous, exists := c.roots[root.ID]
		changed := exists && rootChanged(previous, root)
		if changed {
			c.removeRootWatches(root.ID)
		}
		c.roots[root.ID] = root
		c.ensureRootStatus(root.ID, root.VerificationState)
		if !root.Enabled || root.VerificationState != "verified" || root.Identity == nil {
			c.removeRootWatches(root.ID)
			if d := c.dirty[root.ID]; d != nil && !d.inProgress {
				delete(c.dirty, root.ID)
			}
			watchState := "disabled"
			if root.Enabled {
				watchState = root.VerificationState
			}
			c.setRootStatus(root.ID, func(status *RootRuntimeStatus) {
				status.VerificationState = root.VerificationState
				status.WatchState = watchState
				status.Dirty = c.dirty[root.ID] != nil
				status.RetryAt = nil
			})
			if c.activeRoot == root.ID && c.scanCancel != nil {
				c.scanCancel()
			}
			continue
		}
		c.setRootStatus(root.ID, func(status *RootRuntimeStatus) {
			status.VerificationState = root.VerificationState
			if status.WatchState == "disabled" || status.WatchState == "unverified" || status.WatchState == "quarantined" {
				status.WatchState = "pending"
			}
		})
		if c.watcher != nil && (changed || len(c.rootWatchPaths[root.ID]) == 0) {
			c.registerWatch(root.ID, root.CanonicalPath)
		}
		if !exists || changed {
			c.markDirty(root.ID, "configuration", now, false)
		}
	}
	for id := range c.roots {
		if _, ok := seen[id]; ok {
			continue
		}
		c.removeRootWatches(id)
		delete(c.roots, id)
		delete(c.dirty, id)
		c.deleteRootStatus(id)
		if c.activeRoot == id && c.scanCancel != nil {
			c.scanCancel()
		}
	}
	c.setStatus(func(status *CoordinatorStatus) {
		t := now
		status.LastRefreshAt = &t
		status.RootsTotal = len(c.roots)
		status.DatabaseState = "available"
	})
	return nil
}

func rootChanged(a, b storage.LibraryRoot) bool {
	identityEqual := a.Identity == nil && b.Identity == nil || storage.RootIdentityEqual(a.Identity, b.Identity)
	return a.CanonicalPath != b.CanonicalPath || a.Enabled != b.Enabled || a.VerificationState != b.VerificationState || !identityEqual
}

func (c *Coordinator) ensureWatcher() error {
	if c.watcher != nil {
		return nil
	}
	watcher, err := c.watcherFactory()
	if err != nil {
		c.watcherState = "unavailable"
		c.watcherError = "watcher_start_failed"
		c.planWatcherRetry()
		c.publishWatcherState()
		return err
	}
	c.watcher = watcher
	c.watchEvents = watcher.Events()
	c.watchErrors = watcher.Errors()
	c.watcherState = "running"
	c.watcherError = ""
	c.watchRetryDelay = c.watchRetryMin
	c.watchRetryAt = time.Time{}
	for id, root := range c.roots {
		if root.Enabled && root.VerificationState == "verified" && root.Identity != nil && len(c.rootWatchPaths[id]) == 0 {
			c.registerWatch(id, root.CanonicalPath)
		}
	}
	c.publishWatcherState()
	return nil
}

func (c *Coordinator) registerWatch(rootID, path string) bool {
	if c.watcher == nil || path == "" {
		return false
	}
	path = filepath.Clean(path)
	key := watchPathKey(path)
	if _, ok := c.watchPaths[key]; ok {
		return true
	}
	if c.watchCount >= c.maxWatches {
		c.setRootStatus(rootID, func(status *RootRuntimeStatus) {
			status.WatchState = "degraded"
			status.WatchErrorCode = "watch_limit_reached"
		})
		return false
	}
	if err := c.watcher.Add(path); err != nil {
		c.setRootStatus(rootID, func(status *RootRuntimeStatus) {
			status.WatchState = "degraded"
			status.WatchErrorCode = "watch_registration_failed"
		})
		return false
	}
	c.watchPaths[key] = rootID
	if c.rootWatchPaths[rootID] == nil {
		c.rootWatchPaths[rootID] = make(map[string]struct{})
	}
	c.rootWatchPaths[rootID][key] = struct{}{}
	c.watchCount++
	c.setRootStatus(rootID, func(status *RootRuntimeStatus) {
		if status.WatchState != "degraded" {
			status.WatchState = "watching"
			status.WatchErrorCode = ""
		}
	})
	return true
}

func (c *Coordinator) updateRootWatches(root storage.LibraryRoot, directories []string, traversalComplete bool) bool {
	if c.watcher == nil || !root.Enabled || root.VerificationState != "verified" || root.Identity == nil {
		return false
	}
	desired := make(map[string]string, len(directories)+1)
	desired[watchPathKey(root.CanonicalPath)] = root.CanonicalPath
	for _, relative := range directories {
		clean := filepath.Clean(filepath.FromSlash(relative))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			continue
		}
		path := filepath.Join(root.CanonicalPath, clean)
		desired[watchPathKey(path)] = path
	}
	before := make(map[string]struct{}, len(c.rootWatchPaths[root.ID]))
	for key := range c.rootWatchPaths[root.ID] {
		before[key] = struct{}{}
		if _, ok := desired[key]; !ok && traversalComplete {
			c.removeWatchKey(key)
		}
	}
	truncated := false
	keys := make([]string, 0, len(desired))
	for key := range desired {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if c.watchCount >= c.maxWatches {
			if _, exists := c.watchPaths[key]; !exists {
				truncated = true
				continue
			}
		}
		if _, exists := c.watchPaths[key]; !exists {
			if !c.registerWatch(root.ID, desired[key]) {
				truncated = true
			}
		}
	}
	if truncated {
		c.setRootStatus(root.ID, func(status *RootRuntimeStatus) {
			status.WatchState = "degraded"
			status.WatchErrorCode = "watch_coverage_incomplete"
		})
	} else {
		c.setRootStatus(root.ID, func(status *RootRuntimeStatus) {
			status.WatchState = "watching"
			status.WatchErrorCode = ""
		})
	}
	if len(before) != len(c.rootWatchPaths[root.ID]) {
		return true
	}
	for key := range before {
		if _, ok := c.rootWatchPaths[root.ID][key]; !ok {
			return true
		}
	}
	return false
}

func (c *Coordinator) removeWatchKey(key string) {
	rootID, ok := c.watchPaths[key]
	if !ok {
		return
	}
	if c.watcher != nil {
		_ = c.watcher.Remove(key)
	}
	delete(c.watchPaths, key)
	if paths := c.rootWatchPaths[rootID]; paths != nil {
		delete(paths, key)
		if len(paths) == 0 {
			delete(c.rootWatchPaths, rootID)
		}
	}
	if c.watchCount > 0 {
		c.watchCount--
	}
}

func (c *Coordinator) removeRootWatches(rootID string) {
	for key := range c.rootWatchPaths[rootID] {
		c.removeWatchKey(key)
	}
}

func (c *Coordinator) closeWatcher() {
	if c.watcher != nil {
		_ = c.watcher.Close()
	}
	c.watcher = nil
	c.watchEvents = nil
	c.watchErrors = nil
	c.watchPaths = make(map[string]string)
	c.rootWatchPaths = make(map[string]map[string]struct{})
	c.watchCount = 0
}

func (c *Coordinator) watcherLost(code string) {
	c.watcherState = "recovering"
	c.watcherError = code
	c.closeWatcher()
	for id, root := range c.roots {
		if root.Enabled && root.VerificationState == "verified" && root.Identity != nil {
			c.setRootStatus(id, func(status *RootRuntimeStatus) {
				status.WatchState = "degraded"
				status.WatchErrorCode = code
			})
		}
	}
	c.planWatcherRetry()
	c.requestSweep(code)
	c.publishWatcherState()
	c.log.Warn("watcher_recovery_scheduled", "cycle_id", c.sweepCycle, "code", code)
}

func (c *Coordinator) planWatcherRetry() {
	delay := c.watchRetryDelay
	if delay <= 0 {
		delay = c.watchRetryMin
	}
	c.watchRetryAt = c.now().Add(withJitter(delay))
	if delay < c.watchRetryMax {
		delay *= 2
		if delay > c.watchRetryMax {
			delay = c.watchRetryMax
		}
	}
	c.watchRetryDelay = delay
}

func (c *Coordinator) handleEvent(event fsnotifyEvent) {
	name := filepath.Clean(event.Name)
	rootID := c.rootForPath(name)
	if rootID == "" {
		c.incrementGlobalCounter(func(status *CoordinatorStatus) { status.EventsObserved = saturatingIncrement(status.EventsObserved) })
		c.requestSweep("event_unattributed")
		return
	}
	if key := watchPathKey(name); c.watchPaths[key] == rootID && (event.Op&fsnotify.Remove != 0 || event.Op&fsnotify.Rename != 0) {
		c.removeWatchKey(key)
		c.setRootStatus(rootID, func(status *RootRuntimeStatus) {
			status.WatchState = "degraded"
			status.WatchErrorCode = "watched_directory_removed"
		})
	}
	c.incrementGlobalCounter(func(status *CoordinatorStatus) { status.EventsObserved = saturatingIncrement(status.EventsObserved) })
	c.setRootStatus(rootID, func(status *RootRuntimeStatus) { status.EventsObserved = saturatingIncrement(status.EventsObserved) })
	c.markDirty(rootID, "watcher", c.now(), true)
}

func (c *Coordinator) rootForPath(path string) string {
	if id := c.watchPaths[watchPathKey(path)]; id != "" {
		return id
	}
	best, bestLength := "", -1
	for id, root := range c.roots {
		if !root.Enabled || root.VerificationState != "verified" {
			continue
		}
		rel, err := filepath.Rel(root.CanonicalPath, path)
		if err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if len(root.CanonicalPath) > bestLength {
				best, bestLength = id, len(root.CanonicalPath)
			}
		}
	}
	return best
}

func (c *Coordinator) markDirty(rootID, trigger string, now time.Time, event bool) {
	root, exists := c.roots[rootID]
	if !exists || !root.Enabled || root.VerificationState != "verified" || root.Identity == nil {
		c.requestSweep("ineligible_root_event")
		return
	}
	d := c.dirty[rootID]
	if d == nil {
		if len(c.dirty) >= c.maxDirty {
			c.requestSweep("dirty_capacity")
			return
		}
		d = &dirtyRoot{firstDirty: now, retryDelay: c.retryMin, trigger: trigger}
		c.dirty[rootID] = d
	}
	d.epoch = saturatingIncrement(d.epoch)
	if d.firstDirty.IsZero() {
		d.firstDirty = now
	}
	d.lastEvent = now
	d.trigger = trigger
	due := now.Add(c.debounce)
	maximum := d.firstDirty.Add(c.maxDebounce)
	if due.After(maximum) {
		due = maximum
	}
	d.dueAt = due
	if !d.inProgress && d.retryAt.After(due) {
		d.dueAt = d.retryAt
	}
	if event {
		c.setRootStatus(rootID, func(status *RootRuntimeStatus) { status.Dirty = true })
	}
}

func (c *Coordinator) requestSweep(reason string) {
	now := c.now()
	if c.sweepActive {
		if c.sweepCursor != "" {
			c.sweepAgain = true
		}
		return
	}
	c.sweepActive = true
	c.sweepAgain = false
	c.sweepCursor = ""
	c.sweepPage = nil
	c.sweepPageIndex = 0
	c.sweepFetchAfter = now
	c.sweepCycle = saturatingIncrement(c.sweepCycle)
	c.setStatus(func(status *CoordinatorStatus) {
		status.GlobalSweep = true
		t := now
		status.LastSweepAt = &t
		status.LastSweepReason = reason
		status.LastSweepCycleID = c.sweepCycle
	})
	c.log.Info("reconciliation_sweep_scheduled", "cycle_id", c.sweepCycle, "trigger", reason)
}

func (c *Coordinator) fillSweep(parent context.Context, now time.Time) {
	if !c.sweepActive || now.Before(c.sweepFetchAfter) {
		return
	}
	for c.sweepActive {
		if c.sweepPageIndex >= len(c.sweepPage) {
			ctx, cancel := context.WithTimeout(parent, 5*time.Second)
			page, err := c.store.ListRootsAfter(ctx, c.sweepCursor, sweepPageSize)
			cancel()
			if err != nil {
				c.sweepFetchAfter = now.Add(c.retryMin)
				c.setStatus(func(status *CoordinatorStatus) { status.DatabaseState = "degraded" })
				return
			}
			if len(page) == 0 {
				c.finishSweep()
				return
			}
			c.sweepPage = page
			c.sweepPageIndex = 0
		}
		root := c.sweepPage[c.sweepPageIndex]
		if current, ok := c.roots[root.ID]; !ok {
			c.sweepCursor = root.ID
			c.sweepPageIndex++
			continue
		} else {
			root = current
		}
		if d := c.dirty[root.ID]; d != nil {
			c.sweepCursor = root.ID
			c.sweepPageIndex++
			continue
		}
		if len(c.dirty) >= c.maxDirty {
			return
		}
		if root.Enabled && root.VerificationState == "verified" && root.Identity != nil {
			c.roots[root.ID] = root
			if c.watcher != nil && len(c.rootWatchPaths[root.ID]) == 0 {
				c.registerWatch(root.ID, root.CanonicalPath)
			}
			c.dirty[root.ID] = &dirtyRoot{epoch: 1, firstDirty: now, dueAt: now.Add(time.Duration(c.sweepPageIndex) * 20 * time.Millisecond), retryDelay: c.retryMin, trigger: "sweep"}
			c.setRootStatus(root.ID, func(status *RootRuntimeStatus) {
				status.VerificationState = root.VerificationState
				status.Dirty = true
				status.RetryAt = nil
			})
		}
		c.sweepCursor = root.ID
		c.sweepPageIndex++
	}
}

func (c *Coordinator) finishSweep() {
	c.sweepActive = false
	c.sweepPage = nil
	c.sweepPageIndex = 0
	c.setStatus(func(status *CoordinatorStatus) { status.GlobalSweep = false })
	if c.sweepAgain {
		c.sweepAgain = false
		c.requestSweep("coalesced_recovery")
	}
}

func (c *Coordinator) startDueScan(parent context.Context, now time.Time) {
	if c.activeRoot != "" {
		return
	}
	ids := make([]string, 0, len(c.dirty))
	var oldestDue time.Time
	for id, d := range c.dirty {
		if !d.inProgress && !now.Before(d.dueAt) && (d.retryAt.IsZero() || !now.Before(d.retryAt)) {
			root := c.roots[id]
			if root.Enabled && root.VerificationState == "verified" && root.Identity != nil {
				if oldestDue.IsZero() || d.dueAt.Before(oldestDue) {
					oldestDue = d.dueAt
					ids = ids[:0]
					ids = append(ids, id)
				} else if d.dueAt.Equal(oldestDue) {
					ids = append(ids, id)
				}
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	sort.Strings(ids)
	selected := ids[0]
	for _, id := range ids {
		if id > c.lastServedID {
			selected = id
			break
		}
	}
	d := c.dirty[selected]
	d.inProgress = true
	d.scanEpoch = d.epoch
	d.scanStarted = now
	c.activeRoot = selected
	c.lastServedID = selected
	c.cycle = saturatingIncrement(c.cycle)
	ctx, cancel := context.WithCancel(parent)
	c.scanCancel = cancel
	job := scanJob{ctx: ctx, rootID: selected, cycle: c.cycle, trigger: d.trigger}
	c.setStatus(func(status *CoordinatorStatus) { status.ScansStarted = saturatingIncrement(status.ScansStarted) })
	c.setRootStatus(selected, func(status *RootRuntimeStatus) { status.Dirty = true; status.RetryAt = nil })
	c.log.Info("reconciliation_scan_started", "cycle_id", c.cycle, "root_id", selected, "trigger", d.trigger)
	c.workerJobs <- job
}

func (c *Coordinator) finishScan(result scanResult) {
	if c.scanCancel != nil {
		c.scanCancel()
		c.scanCancel = nil
	}
	c.activeRoot = ""
	ended := result.ended
	if ended.IsZero() {
		ended = c.now()
	}
	d := c.dirty[result.rootID]
	root, configured := c.roots[result.rootID]
	if !configured {
		// Configuration refresh may remove a root while its canceled scan is
		// finishing. Do not resurrect it in the public diagnostic snapshot.
		c.log.Info("reconciliation_scan_finished", "cycle_id", result.cycle, "root_id", result.rootID, "code", "root_removed")
		return
	}
	code := result.result.ErrorCode
	if code == "" && result.err != nil {
		code = coordinatorErrorCode(result.err)
	}
	if code == "root_disabled" {
		root.Enabled = false
		c.roots[result.rootID] = root
	}
	authoritative := result.err == nil && result.result.TraversalComplete && result.result.AbsenceReconciled
	scanStatus := result.result.Status
	if scanStatus == "" && result.err != nil {
		scanStatus = "failed"
	}
	c.setRootStatus(result.rootID, func(status *RootRuntimeStatus) {
		t := ended
		status.LastScanAt = &t
		status.LastScanStatus = scanStatus
		status.AbsenceReconciled = authoritative
		status.ErrorCode = code
		if !root.Enabled {
			status.WatchState = "disabled"
			status.WatchErrorCode = ""
		} else if code == "root_unavailable" {
			status.WatchState = "offline"
		}
	})
	if code == "database_unavailable" || code == "publish_failed" {
		c.setStatus(func(status *CoordinatorStatus) { status.DatabaseState = "degraded" })
	} else if authoritative {
		c.setStatus(func(status *CoordinatorStatus) { status.DatabaseState = "available" })
	}
	if result.err != nil || !authoritative {
		c.setStatus(func(status *CoordinatorStatus) { status.ScansFailed = saturatingIncrement(status.ScansFailed) })
	} else {
		c.setStatus(func(status *CoordinatorStatus) { status.ScansSucceeded = saturatingIncrement(status.ScansSucceeded) })
	}
	c.log.Info("reconciliation_scan_finished", "cycle_id", result.cycle, "root_id", result.rootID,
		"status", result.result.Status, "error_code", code, "traversal_complete", result.result.TraversalComplete,
		"absence_reconciled", result.result.AbsenceReconciled, "duration_ms", result.result.DurationMS)

	watchSetChanged := false
	if root.ID != "" && result.result.RunID != "" && code != "root_identity_mismatch" && code != "root_identity_unavailable" && code != "root_unavailable" && code != "root_disabled" {
		watchSetChanged = c.updateRootWatches(root, result.result.WatchedDirectories, result.result.TraversalComplete)
	}
	if code == "root_unavailable" || code == "root_disabled" {
		c.removeRootWatches(result.rootID)
	}
	if code == "root_identity_mismatch" || code == "root_identity_unavailable" {
		c.removeRootWatches(result.rootID)
		root.VerificationState = "quarantined"
		c.roots[result.rootID] = root
		c.setRootStatus(result.rootID, func(status *RootRuntimeStatus) {
			status.VerificationState = "quarantined"
			status.WatchState = "quarantined"
		})
	}
	if d == nil {
		return
	}
	d.inProgress = false
	if !root.Enabled || root.VerificationState != "verified" || root.Identity == nil || code == "root_disabled" {
		delete(c.dirty, result.rootID)
		c.setRootStatus(result.rootID, func(status *RootRuntimeStatus) { status.Dirty = false; status.RetryAt = nil })
		return
	}
	if watchSetChanged {
		c.markDirty(result.rootID, "watch_coverage_refresh", c.now(), false)
	}
	if authoritative && d.epoch == d.scanEpoch {
		delete(c.dirty, result.rootID)
		c.setRootStatus(result.rootID, func(status *RootRuntimeStatus) { status.Dirty = false; status.RetryAt = nil })
		return
	}
	now := c.now()
	changedDuringScan := d.epoch != d.scanEpoch
	if result.err != nil || !authoritative {
		delay := d.retryDelay
		if delay < c.retryMin {
			delay = c.retryMin
		}
		d.retryAt = now.Add(withJitter(delay))
		if delay < c.retryMax {
			delay *= 2
			if delay > c.retryMax {
				delay = c.retryMax
			}
		}
		d.retryDelay = delay
		d.dueAt = maxTime(d.retryAt, d.dueAt)
		c.setRootStatus(result.rootID, func(status *RootRuntimeStatus) {
			status.Dirty = true
			retry := d.retryAt
			status.RetryAt = &retry
			status.Retries = saturatingIncrement(status.Retries)
		})
	} else {
		d.retryAt = time.Time{}
		d.retryDelay = c.retryMin
		d.dueAt = maxTime(d.lastEvent.Add(c.debounce), result.ended.Add(c.minFollowup))
		c.setRootStatus(result.rootID, func(status *RootRuntimeStatus) { status.Dirty = true; status.RetryAt = nil })
	}
	if changedDuringScan && d.dueAt.Before(result.ended.Add(c.minFollowup)) {
		d.dueAt = result.ended.Add(c.minFollowup)
	}
}

func (c *Coordinator) periodicStagger() time.Duration {
	if c.periodic <= time.Second {
		return 0
	}
	return time.Duration(rand.Int63n(int64(min(c.periodic/10, time.Minute))))
}

func withJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	factor := 800 + rand.Intn(401)
	return time.Duration(int64(delay) * int64(factor) / 1000)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func coordinatorErrorCode(err error) string {
	switch {
	case errors.Is(err, storage.ErrRootUnverified):
		return "root_unverified"
	case errors.Is(err, storage.ErrRootQuarantined), errors.Is(err, storage.ErrRootIdentityMismatch):
		return "root_identity_mismatch"
	case errors.Is(err, storage.ErrRootIdentityUnavailable):
		return "root_identity_unavailable"
	case errors.Is(err, storage.ErrRootUnavailable), errors.Is(err, os.ErrNotExist):
		return "root_unavailable"
	case errors.Is(err, storage.ErrScanRunning):
		return "scan_lease_busy"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "database_unavailable"
	}
}

func watchPathKey(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return clean
}

func saturatingIncrement(value uint64) uint64 {
	if value < ^uint64(0) {
		return value + 1
	}
	return value
}

func (c *Coordinator) ensureRootStatus(id, verification string) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	for i := range c.status.Roots {
		if c.status.Roots[i].ID == id {
			c.status.Roots[i].VerificationState = verification
			return
		}
	}
	c.status.RootsTotal++
	c.status.Roots = append(c.status.Roots, RootRuntimeStatus{ID: id, VerificationState: verification, WatchState: "pending"})
	c.sortStatusRootsLocked()
}

func (c *Coordinator) setRootStatus(id string, update func(*RootRuntimeStatus)) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	c.status.DirtyRoots = len(c.dirty)
	c.status.GlobalSweep = c.sweepActive
	c.status.WatcherState = c.watcherState
	c.status.WatcherError = c.watcherError
	for i := range c.status.Roots {
		if c.status.Roots[i].ID == id {
			update(&c.status.Roots[i])
			return
		}
	}
	status := RootRuntimeStatus{ID: id}
	update(&status)
	c.status.RootsTotal++
	c.status.Roots = append(c.status.Roots, status)
	c.sortStatusRootsLocked()
}

func (c *Coordinator) deleteRootStatus(id string) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	c.status.DirtyRoots = len(c.dirty)
	c.status.GlobalSweep = c.sweepActive
	for i := range c.status.Roots {
		if c.status.Roots[i].ID == id {
			c.status.Roots = append(c.status.Roots[:i], c.status.Roots[i+1:]...)
			c.status.RootsTotal--
			return
		}
	}
}

func (c *Coordinator) sortStatusRootsLocked() {
	sort.Slice(c.status.Roots, func(i, j int) bool { return c.status.Roots[i].ID < c.status.Roots[j].ID })
	if len(c.status.Roots) > defaultMaxDirtyRoots {
		c.status.Roots = c.status.Roots[:defaultMaxDirtyRoots]
	}
	c.status.RootsTruncated = c.status.RootsTotal > len(c.status.Roots)
}

func (c *Coordinator) setStatus(update func(*CoordinatorStatus)) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	update(&c.status)
	c.status.DirtyRoots = len(c.dirty)
	c.status.GlobalSweep = c.sweepActive
	c.status.WatcherState = c.watcherState
	c.status.WatcherError = c.watcherError
	c.status.RootsTruncated = c.status.RootsTotal > len(c.status.Roots)
	for i := range c.status.Roots {
		c.status.Roots[i].Dirty = c.dirty[c.status.Roots[i].ID] != nil
	}
}

func (c *Coordinator) publishWatcherState() {
	c.setStatus(func(*CoordinatorStatus) {})
}

func (c *Coordinator) incrementGlobalCounter(update func(*CoordinatorStatus)) {
	c.setStatus(update)
}
