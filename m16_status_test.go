package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"resonance/internal/library"
	"resonance/internal/storage"
)

type statusTestStore struct{}

func (statusTestStore) ListRootsForReconciliation(context.Context) ([]storage.LibraryRoot, error) {
	return nil, nil
}
func (statusTestStore) ListRootsAfter(context.Context, string, int) ([]storage.LibraryRoot, error) {
	return nil, nil
}

type statusTestScanner struct{}

func (statusTestScanner) Scan(context.Context, string) (library.ScanResult, error) {
	return library.ScanResult{}, errors.New("unexpected scan")
}

func TestM16DiagnosticsRemainSeparateFromReadiness(t *testing.T) {
	coordinator, err := library.NewCoordinator(library.CoordinatorOptions{
		Store: statusTestStore{}, Scanner: statusTestScanner{}, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WatcherFactory:      func() (library.DirectoryWatcher, error) { return nil, errors.New("injected watcher start failure") },
		WatcherRetryMinimum: time.Hour, WatcherRetryMaximum: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = coordinator.Run(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for coordinator.Status().WatcherState != "unavailable" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	handler := newHandlerWithCoordinator("data/demo.wav", "Demo", io.Discard,
		func(context.Context) error { return errors.New("injected catalog readiness failure") }, nil, coordinator)
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/api/v1/library/status", nil))
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("diagnostics status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["watcher_state"] != "unavailable" {
		t.Fatalf("watcher startup failure missing from diagnostics: %#v", status)
	}
	if status["database_state"] != "degraded" {
		t.Fatalf("diagnostics reported stale database readiness: %#v", status)
	}
	readyResponse := httptest.NewRecorder()
	handler.ServeHTTP(readyResponse, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if readyResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness ignored catalog failure: %d", readyResponse.Code)
	}
	healthResponse := httptest.NewRecorder()
	handler.ServeHTTP(healthResponse, httptest.NewRequest(http.MethodGet, "/health", nil))
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("health depended on watcher/catalog: %d", healthResponse.Code)
	}
	cancel()
	<-done
}
