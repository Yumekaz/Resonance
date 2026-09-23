package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewHTTPRangeSemantics(t *testing.T) {
	h, _ := fixture(t, []byte("0123456789"))
	for _, tc := range []struct {
		name, method, rangeValue, ifRange string
		status                            int
		body                              string
	}{
		{"HEAD ignores Range", "HEAD", "bytes=2-3", "", 200, ""},
		{"HEAD ignores invalid Range", "HEAD", "bytes=oops", "", 200, ""},
		{"unmatched If-Range", "GET", "bytes=2-3", `"old"`, 200, "0123456789"},
		{"unknown unit", "GET", "items=2-3", "", 200, "0123456789"},
		{"multiple ranges", "GET", "bytes=0-1,4-5", "", 200, "0123456789"},
		{"large end", "GET", "bytes=2-99999999999999999999999", "", 206, "23456789"},
		{"large suffix", "GET", "bytes=-99999999999999999999999", "", 206, "0123456789"},
		{"large start", "GET", "bytes=99999999999999999999999-", "", 416, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/media/demo-track", nil)
			r.Header.Set("Range", tc.rangeValue)
			if tc.ifRange != "" {
				r.Header.Set("If-Range", tc.ifRange)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if tc.status < 400 && w.Body.String() != tc.body {
				t.Fatalf("body %q", w.Body.String())
			}
			if tc.method == "HEAD" && (w.Header().Get("Content-Length") != "10" || w.Header().Get("Content-Range") != "") {
				t.Fatal(w.Header())
			}
		})
	}
	r := httptest.NewRequest("GET", "/media/demo-track", nil)
	r.Header.Add("Range", "bytes=0-1")
	r.Header.Add("Range", "bytes=4-5")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "0123456789" {
		t.Fatalf("duplicate Range: %d %q", w.Code, w.Body.String())
	}
}

func TestReviewAllRequestsHaveIDs(t *testing.T) {
	h, _ := fixture(t, []byte("audio"))
	for _, path := range []string{"/health", "/api/v1/demo-track", "/", "/missing", "/media/unknown"} {
		if w := doRequest(h, path, ""); w.Header().Get("X-Request-ID") == "" {
			t.Errorf("no ID for %s", path)
		}
	}
}

func TestReviewAlreadyCanceledDoesNotStream(t *testing.T) {
	h, _ := fixture(t, bytes.Repeat([]byte("x"), 128*1024))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/media/demo-track", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Body.Len() != 0 {
		t.Fatalf("streamed %d bytes after cancellation", w.Body.Len())
	}
}

// A channel makes completion observable without racing a bytes.Buffer.
type eventWriter struct{ events chan []byte }

func (w eventWriter) Write(p []byte) (int, error) { w.events <- bytes.Clone(p); return len(p), nil }

func TestReviewRealDisconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(256 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	logs := eventWriter{make(chan []byte, 20)}
	h := newHandler(path, "Large", logs)
	s := httptest.NewServer(h)
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", s.URL+"/media/demo-track", nil)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1024)
	if _, err = io.ReadFull(response.Body, b); err != nil {
		t.Fatal(err)
	}
	cancel()
	response.Body.Close()
	select {
	case line := <-logs.events:
		var event map[string]any
		if err = json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event["canceled"] != true || event["bytes_served"].(float64) >= 256<<20 {
			t.Fatalf("%s", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not stop after disconnect")
	}
	// On Windows deletion fails if the streaming handle is still open.
	if err = os.Remove(path); err != nil {
		t.Fatalf("media handle leaked: %v", err)
	}
}

func TestReviewErrorRepresentation(t *testing.T) {
	h, _ := fixture(t, []byte("12345"))
	w := doRequest(h, "/media/demo-track", "bytes=100-")
	if strings.HasPrefix(w.Header().Get("Content-Type"), "audio/") {
		t.Fatal("error advertised as audio")
	}
}

func TestReviewMediaSymlinkCannotEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.wav")
	if err := os.WriteFile(outside, []byte("private bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "demo.wav")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	h := newHandler(link, "Demo", io.Discard)
	w := doRequest(h, "/media/demo-track", "")
	if w.Code != 503 || strings.Contains(w.Body.String(), "private bytes") {
		t.Fatalf("escaped root: %d %q", w.Code, w.Body.String())
	}
}

func TestReviewStaticAssetsAreEmbedded(t *testing.T) {
	h, _ := fixture(t, []byte("123"))
	t.Chdir(t.TempDir())
	if err := os.Mkdir("web", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("web/secret.txt", []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := doRequest(h, "/secret.txt", ""); w.Code != 404 {
		t.Fatalf("static directory exposed: %d", w.Code)
	}
	if w := doRequest(h, "/", ""); w.Code != 200 {
		t.Fatalf("embedded client missing: %d", w.Code)
	}
}

func TestReviewCopyStopsOnReadFailure(t *testing.T) {
	w := httptest.NewRecorder()
	n, err := copyMedia(context.Background(), w, strings.NewReader("short"), 100)
	if n != 5 || err != io.EOF {
		t.Fatalf("count=%d error=%v", n, err)
	}
}

func TestReviewTelemetrySeparatesRequestedAndSelected(t *testing.T) {
	h, logs := fixture(t, []byte("0123456789"))
	doRequest(h, "/media/demo-track", "bytes=2-999")
	var event map[string]any
	if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["source"] != "demo" || event["range_header"] != "bytes=2-999" || event["selected_end"] != float64(9) || event["range_applied"] != true || event["bytes_served"] != float64(8) {
		t.Fatalf("%s", logs.String())
	}
}

type truncateWriter struct {
	*httptest.ResponseRecorder
	path string
	t    *testing.T
}

func (w *truncateWriter) Write(p []byte) (int, error) {
	if err := os.Truncate(w.path, 0); err != nil {
		w.t.Fatal(err)
	}
	return w.ResponseRecorder.Write(p)
}

func TestReviewTruncationAbortsAndLogsIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncate.wav")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 128*1024), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := newHandler(path, "Truncate", &logs)
	w := &truncateWriter{httptest.NewRecorder(), path, t}
	func() {
		defer func() {
			if got := recover(); got != http.ErrAbortHandler {
				t.Errorf("want transfer abort, got %v", got)
			}
		}()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/media/demo-track", nil))
	}()
	var event map[string]any
	if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["bytes_served"] != float64(32*1024) || event["bytes_intended"] != float64(128*1024) || event["error"] != "stream_failed" {
		t.Fatalf("%s", logs.String())
	}
	if w.Body.Len() != 32*1024 {
		t.Fatalf("unexpected appended error or byte count: %d", w.Body.Len())
	}
}

func TestReviewWindowsStyleClientPaths(t *testing.T) {
	h, _ := fixture(t, []byte("only registered bytes"))
	for _, path := range []string{"/media/C:%5cWindows%5cwin.ini", "/media/demo-track:secret", "/media/%5c%5cserver%5cshare", "/media/..%5csecret", "/media/%252e%252e%252fsecret"} {
		w := doRequest(h, path, "")
		if w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}
