package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadinessSeparateFromLiveness(t *testing.T) {
	failing := func(context.Context) error { return errors.New("secret connection detail") }
	h := newHandlerWithReadiness("data/demo.wav", "Demo", io.Discard, failing)
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/health", 200, `"status":"ok"`},
		{"/ready", 503, `"catalog":"unavailable"`},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("content type: %v", w.Header())
		}
	}
	unconfigured := newHandler("data/demo.wav", "Demo", io.Discard)
	w := httptest.NewRecorder()
	unconfigured.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"catalog":"unconfigured"`) {
		t.Fatalf("unconfigured: %d %s", w.Code, w.Body.String())
	}
	working := newHandlerWithReadiness("data/demo.wav", "Demo", io.Discard, func(context.Context) error { return nil })
	w = httptest.NewRecorder()
	working.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"catalog":"ready"`) {
		t.Fatalf("ready: %d %s", w.Code, w.Body.String())
	}
}

func TestDatabaseFailureDoesNotGateDemo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.wav")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h := newHandlerWithReadiness(path, "Demo", io.Discard, func(context.Context) error { calls++; return errors.New("private connection failure") })
	for _, path := range []string{"/health", "/api/v1/demo-track", "/media/demo-track"} {
		w := doRequest(h, path, "")
		if w.Code != 200 {
			t.Fatalf("DB failure broke %s: %d", path, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("demo path consulted database readiness")
	}
	if w := doRequest(h, "/ready", ""); w.Code != 503 || calls != 1 {
		t.Fatal("readiness did not check DB")
	}
}
