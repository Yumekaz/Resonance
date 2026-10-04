package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostFolderPickerBoundaryAndResult(t *testing.T) {
	calls := 0
	path := filepath.Join(t.TempDir(), "Music Ω")
	picker := &processFolderPicker{supported: true, run: func(context.Context) ([]byte, error) { calls++; return json.Marshal(folderChoice{Path: path}) }}
	handler := newAdminHandlerWithPicker(nil, nil, nil, io.Discard, "127.0.0.1:8080", picker)
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		want   int
	}{
		{"same origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:8081") }, 200},
		{"remote peer", func(r *http.Request) { r.RemoteAddr = "192.168.1.20:1234" }, 403},
		{"cross origin", func(r *http.Request) { r.Header.Set("Origin", "http://attacker.example") }, 403},
		{"missing header", func(r *http.Request) { r.Header.Del("X-Resonance-Admin") }, 403},
		{"input path forbidden", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(`{"path":"ignore"}`)) }, 400},
		{"null forbidden", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(`null`)) }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			r := localAdminRequest("POST", "/api/v1/admin/folder-picker", "{}")
			tc.change(r)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if tc.want != 200 && calls != before {
				t.Fatal("rejected request opened picker")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("picker result cacheable")
			}
			if tc.want == 200 {
				var result struct{ Path, Name string }
				if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Path != path || result.Name != "Music Ω" {
					t.Fatal("choice lost")
				}
			}
		})
	}
}

func TestHostPickerParentPipeEndsChild(t *testing.T) {
	if os.Getenv("RESONANCE_PICKER_PIPE_CHILD") == "1" {
		watchPickerParent(os.Stdin, func() { os.Exit(0) })
		io.WriteString(os.Stdout, "ready\n")
		select {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, hold, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer hold.Close()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestHostPickerParentPipeEndsChild$")
	configurePickerProcess(child)
	child.Env = append(os.Environ(), "RESONANCE_PICKER_PIPE_CHILD=1")
	child.Stdin = reader
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child did not start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("child exited while parent alive: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	hold.Close()
	if err := <-done; err != nil {
		t.Fatalf("child did not exit when parent control pipe closed: %v", err)
	}
}
func TestHostFolderPickerCancellationErrorsAndSingleFlight(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		err  error
		want int
	}{
		{"cancelled", `{"cancelled":true}`, nil, 200},
		{"unavailable", ``, errPickerUnsupported, 503},
		{"timeout", ``, context.DeadlineExceeded, 408},
		{"relative path", `{"path":"Music","cancelled":false}`, nil, 503},
		{"cancel with path", `{"path":"Music","cancelled":true}`, nil, 503},
		{"extra result", `{"cancelled":true} {}`, nil, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			picker := &processFolderPicker{supported: true, run: func(context.Context) ([]byte, error) { return []byte(tc.data), tc.err }}
			handler := newAdminHandlerWithPicker(nil, nil, nil, io.Discard, "", picker)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, localAdminRequest("POST", "/api/v1/admin/folder-picker", "{}"))
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
	started, release := make(chan struct{}), make(chan struct{})
	picker := &processFolderPicker{supported: true, run: func(ctx context.Context) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return []byte(`{"cancelled":true}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := picker.Pick(ctx); done <- err }()
	<-started
	if _, err := picker.Pick(context.Background()); !errors.Is(err, errPickerBusy) {
		t.Fatal("second picker admitted")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
	picker.run = func(context.Context) ([]byte, error) { return []byte(`{"cancelled":true}`), nil }
	if _, err := picker.Pick(context.Background()); err != nil {
		t.Fatal("picker gate not released")
	}
}
func TestHostFolderPickerUnsupportedAndOutputBound(t *testing.T) {
	p := &processFolderPicker{}
	if _, err := p.Pick(context.Background()); !errors.Is(err, errPickerUnsupported) {
		t.Fatal("unsupported picker admitted")
	}
	var output pickerOutput
	if _, err := output.Write(make([]byte, 8193)); err == nil || output.Len() != 0 {
		t.Fatal("output unbounded")
	}
	handler := newAdminHandlerWithPicker(nil, nil, nil, io.Discard, "", p)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, localAdminRequest("POST", "/api/v1/admin/folder-picker", "{}"))
	if w.Code != 501 {
		t.Fatal("missing platform capability not reported")
	}
	public := newHandler("missing.wav", "Demo", io.Discard)
	for _, method := range []string{"GET", "POST"} {
		response := httptest.NewRecorder()
		public.ServeHTTP(response, httptest.NewRequest(method, "/api/v1/admin/folder-picker", strings.NewReader("{}")))
		if response.Code != 404 {
			t.Fatal("native picker route exposed on music listener")
		}
	}
}
