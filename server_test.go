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
	"strconv"
	"strings"
	"testing"
)

func fixture(t *testing.T, content []byte) (http.Handler, *bytes.Buffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "demo.wav")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	return newHandler(path, "Unicode ♫", &logs), &logs
}

func doRequest(h http.Handler, path, rangeHeader string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealthAndMetadata(t *testing.T) {
	h, _ := fixture(t, []byte("audio"))
	w := doRequest(h, "/health", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("health: %d %s", w.Code, w.Body.String())
	}
	w = doRequest(h, "/api/v1/demo-track", "")
	if w.Code != 200 {
		t.Fatalf("metadata: %d", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "demo-track" || body["title"] != "Unicode ♫" || body["stream_url"] != "/media/demo-track" || len(body) != 3 {
		t.Fatalf("metadata: %#v", body)
	}
}

func TestMediaRanges(t *testing.T) {
	content := make([]byte, 1024)
	for i := range content {
		content[i] = byte(i)
	}
	h, logs := fixture(t, content)
	cases := []struct {
		name, header string
		status       int
		start, end   int
		contentRange string
	}{
		{"full", "", 200, 0, 1023, ""},
		{"bounded", "bytes=0-99", 206, 0, 99, "bytes 0-99/1024"},
		{"open", "bytes=100-", 206, 100, 1023, "bytes 100-1023/1024"},
		{"suffix", "bytes=-100", 206, 924, 1023, "bytes 924-1023/1024"},
		{"clamped", "bytes=1000-2000", 206, 1000, 1023, "bytes 1000-1023/1024"},
		{"tiny", "bytes=1023-1023", 206, 1023, 1023, "bytes 1023-1023/1024"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(h, "/media/demo-track", tc.header)
			if w.Code != tc.status {
				t.Fatalf("status: %d", w.Code)
			}
			if w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("Content-Type") != "audio/wav" {
				t.Fatalf("headers: %v", w.Header())
			}
			if w.Header().Get("Content-Range") != tc.contentRange {
				t.Fatalf("range: %s", w.Header().Get("Content-Range"))
			}
			if got := w.Body.Bytes(); !bytes.Equal(got, content[tc.start:tc.end+1]) {
				t.Fatalf("body mismatch: %d bytes", len(got))
			}
			if w.Header().Get("Content-Length") != strings.TrimSpace(stringInt(len(w.Body.Bytes()))) {
				t.Fatalf("length: %s", w.Header().Get("Content-Length"))
			}
			if w.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request ID")
			}
		})
	}
	if !strings.Contains(logs.String(), `"media_id":"demo-track"`) || !strings.Contains(logs.String(), `"bytes_served":100`) {
		t.Fatalf("telemetry: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"status":206`) || !strings.Contains(logs.String(), `"duration_ms":`) || !strings.Contains(logs.String(), `"range":true`) {
		t.Fatalf("missing telemetry fields: %s", logs.String())
	}
}

func TestMediaHead(t *testing.T) {
	h, logs := fixture(t, []byte("12345"))
	r := httptest.NewRequest(http.MethodHead, "/media/demo-track", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "5" {
		t.Fatalf("HEAD: %d %v %q", w.Code, w.Header(), w.Body.String())
	}
	if !strings.Contains(logs.String(), `"bytes_served":0`) {
		t.Fatalf("HEAD telemetry: %s", logs.String())
	}
}

func stringInt(n int) string { return strconv.Itoa(n) }

func TestInvalidRangesAndUnknownID(t *testing.T) {
	h, _ := fixture(t, []byte("12345"))
	for _, tc := range []struct {
		header string
		status int
	}{
		{"bytes=5-", 416}, {"bytes=4-3", 416}, {"bytes=-0", 416},
		{"bytes=0-1,3-4", 200}, {"items=0-1", 200}, {"bytes=x-1", 400},
		{"bytes=--", 400}, {"bytes=999999999999999999999-", 416},
	} {
		w := doRequest(h, "/media/demo-track", tc.header)
		if w.Code != tc.status {
			t.Errorf("%q: %d", tc.header, w.Code)
		}
		if tc.status == 416 && w.Header().Get("Content-Range") != "bytes */5" {
			t.Errorf("%q content range: %s", tc.header, w.Header().Get("Content-Range"))
		}
	}
	w := doRequest(h, "/media/not-a-track", "")
	if w.Code != 404 || strings.Contains(w.Body.String(), "demo.wav") {
		t.Fatalf("unknown: %d %s", w.Code, w.Body.String())
	}
	w = doRequest(h, "/media/..%2f..%2fsecret", "")
	if w.Code == 200 {
		t.Fatal("traversal resolved")
	}
}

func TestEmptyMissingAndTruncated(t *testing.T) {
	h, _ := fixture(t, nil)
	w := doRequest(h, "/media/demo-track", "")
	if w.Code != 200 || w.Header().Get("Content-Length") != "0" {
		t.Fatalf("empty: %d %v", w.Code, w.Header())
	}
	w = doRequest(h, "/media/demo-track", "bytes=0-")
	if w.Code != 416 {
		t.Fatalf("empty range: %d", w.Code)
	}
	path := filepath.Join(t.TempDir(), "missing.wav")
	h = newHandler(path, "Missing", io.Discard)
	w = doRequest(h, "/media/demo-track", "")
	if w.Code != 503 || strings.Contains(w.Body.String(), path) {
		t.Fatalf("missing: %d %s", w.Code, w.Body.String())
	}
}

type cancelWriter struct {
	ctxCancel context.CancelFunc
	header    http.Header
	written   bool
}

func (w *cancelWriter) Header() http.Header { return w.header }
func (w *cancelWriter) WriteHeader(int)     {}
func (w *cancelWriter) Write(p []byte) (int, error) {
	w.ctxCancel()
	w.written = true
	return 0, context.Canceled
}

func TestCancellationTelemetry(t *testing.T) {
	h, logs := fixture(t, make([]byte, 128*1024))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/media/demo-track", nil).WithContext(ctx)
	w := &cancelWriter{ctxCancel: cancel, header: make(http.Header)}
	h.ServeHTTP(w, r)
	var event map[string]any
	if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
		t.Fatalf("cancellation telemetry: %v; logs=%s", err, logs.String())
	}
	if !w.written || event["request_id"] == "" || event["canceled"] != true || event["bytes_intended"] != float64(128*1024) || event["bytes_served"] != float64(0) || event["error"] != "client_canceled" {
		t.Fatalf("cancellation telemetry: %s", logs.String())
	}
	if event["error"] == "stream_failed" {
		t.Fatalf("cancellation reported as server stream failure: %s", logs.String())
	}
}
