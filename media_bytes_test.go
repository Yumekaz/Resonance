package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedMediaPreHeaderFailureKeepsErrorResponse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.wav")
	if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	r := httptest.NewRequest(http.MethodGet, "/media/demo-track", nil)
	w := httptest.NewRecorder()
	result := serveMediaBytes(r.Context(), w, r, f, "audio/wav")
	if result.status != http.StatusServiceUnavailable || result.headersStarted || result.err == nil || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("pre-header failure must remain a safe HTTP error: result=%#v status=%d", result, w.Code)
	}
}
