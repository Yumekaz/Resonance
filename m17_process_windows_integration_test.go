//go:build windows && integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"resonance/internal/library"
)

func m17ServerProcess(t *testing.T, dsn, media, addr, label string) *exec.Cmd {
	t.Helper()
	exe := os.Getenv("RESONANCE_M17_SERVER_EXE")
	if exe == "" {
		t.Skip("production executable required for process drill")
	}
	cmd := exec.Command(exe, "-addr", addr, "-media", media)
	cmd.Env = append(os.Environ(), "RESONANCE_DATABASE_URL="+dsn)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x10, HideWindow: true}
	output := os.Getenv("RESONANCE_M17_ATTEMPT_OUTPUT")
	if output == "" {
		output = t.TempDir()
	}
	f, err := os.OpenFile(filepath.Join(output, label+"-server.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		f.Close()
	})
	return cmd
}
func m17WaitHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
}
func m17HTTPJSON(t *testing.T, base, method, path, key string, body any) []byte {
	t.Helper()
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(method, base+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("HTTP %s %s status=%d: %s %v", method, path, resp.StatusCode, out, err)
	}
	return out
}

func TestM17NormalShutdownStreamPersistenceAndLostResponse(t *testing.T) {
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	dir, err := os.MkdirTemp("data/m17-closure", "shutdown-")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.Abs(dir)
	data, err := os.ReadFile(filepath.Join(os.TempDir(), "ResonanceM17G1-20260928", "journey-corpus-final", "library", "long.wav"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := library.CanonicalizeRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, ok, err := library.CaptureRootIdentity(canonical)
	if err != nil || !ok {
		t.Fatal(err)
	}
	root, err := store.AddRootWithIdentity(ctx, "shutdown", canonical, id)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := addCatalogFixture(t, pool, root.ID, canonical, "long", "wav", data, 17)
	if err := store.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(canonical, "Artist", "Album", "long.wav")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	base := "http://" + addr
	dsn := os.Getenv("RESONANCE_TEST_DATABASE_URL") + " search_path=" + pool.Config().ConnConfig.RuntimeParams["search_path"]
	first := m17ServerProcess(t, dsn, media, addr, "before-shutdown")
	m17WaitHTTP(t, base+"/ready")
	key := "00000000-0000-4000-8000-000000000017"
	request := map[string]any{"track_id": track, "placement": "now", "expected_version": 0}
	// The durable HTTP result is intentionally discarded by this caller.
	committedResponse := m17HTTPJSON(t, base, "POST", "/api/v1/queue/items", key, request)
	beforeQueue := m17HTTPJSON(t, base, "GET", "/api/v1/queue", "", nil)
	_ = m17HTTPJSON(t, base, "PUT", "/api/v1/favorites/tracks/"+track, "", nil)
	playlist := m17HTTPJSON(t, base, "POST", "/api/v1/playlists", "00000000-0000-4000-8000-000000000018", map[string]any{"name": "Shutdown state", "expected_version": 0})
	resp, err := http.Get(base + "/api/v1/tracks/" + track + "/stream")
	if err != nil || resp.StatusCode != 200 {
		t.Fatal("stream open", err)
	}
	buffer := make([]byte, 1024)
	n, err := resp.Body.Read(buffer)
	if err != nil || n == 0 {
		t.Fatal("stream did not open", err)
	}
	readDone := make(chan struct{})
	received := int64(n)
	readError := ""
	go func() {
		defer close(readDone)
		defer resp.Body.Close()
		for {
			n, e := resp.Body.Read(buffer)
			received += int64(n)
			if e != nil {
				readError = e.Error()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	signalScript, _ := filepath.Abs("tools/m17-signal-child.ps1")
	signal := exec.Command("powershell.exe", "-NoProfile", "-File", signalScript, "-TargetProcessId", fmt.Sprint(first.Process.Pid))
	shutdownStart := time.Now()
	if out, err := signal.CombinedOutput(); err != nil {
		t.Fatalf("normal console interrupt unavailable: %s %v", out, err)
	}
	exit := make(chan error, 1)
	go func() { exit <- first.Wait() }()
	select {
	case e := <-exit:
		if e != nil {
			t.Fatal("production normal shutdown failed", e)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("production server did not shut down")
	}
	shutdownMS := float64(time.Since(shutdownStart).Microseconds()) / 1000
	select {
	case <-readDone:
	case <-time.After(30 * time.Second):
		resp.Body.Close()
		<-readDone
	}
	second := m17ServerProcess(t, dsn, media, addr, "after-restart")
	_ = second
	m17WaitHTTP(t, base+"/ready")
	afterQueue := m17HTTPJSON(t, base, "GET", "/api/v1/queue", "", nil)
	if !bytes.Equal(beforeQueue, afterQueue) {
		t.Fatal("normal restart changed queue selection/order")
	}
	replay := m17HTTPJSON(t, base, "POST", "/api/v1/queue/items", key, request)
	if !bytes.Equal(committedResponse, replay) {
		t.Fatal("restart retry did not replay original result")
	}
	if !bytes.Equal(afterQueue, m17HTTPJSON(t, base, "GET", "/api/v1/queue", "", nil)) {
		t.Fatal("lost-response retry duplicated queue mutation")
	}
	favorites := m17HTTPJSON(t, base, "GET", "/api/v1/favorites", "", nil)
	if !bytes.Contains(favorites, []byte(track)) {
		t.Fatal("favorite lost")
	}
	playlists := m17HTTPJSON(t, base, "GET", "/api/v1/playlists", "", nil)
	if !bytes.Contains(playlists, []byte("Shutdown state")) || len(playlist) == 0 {
		t.Fatal("playlist lost")
	}
	result := map[string]any{"shutdown_ms": shutdownMS, "media_expected_bytes": len(data), "media_received_bytes": received, "stream_error": readError, "stream_completed": received == int64(len(data)), "queue_persisted": true, "favorite_persisted": true, "playlist_persisted": true, "original_receipt_replayed_after_restart": true}
	out, _ := json.MarshalIndent(result, "", "  ")
	if path := os.Getenv("RESONANCE_M17_ATTEMPT_OUTPUT"); path != "" {
		if err := os.WriteFile(filepath.Join(path, "shutdown-result.json"), out, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(string(out))
}

func TestM17ConfiguredStartupRejection(t *testing.T) {
	cases := []struct{ name, damage string }{{"unreachable", ""}, {"damaged", "DROP INDEX mutation_receipts_expiry_idx"}, {"pending", "DELETE FROM schema_migrations WHERE version=8"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, pool := catalogTestStore(t)
			dsn := os.Getenv("RESONANCE_TEST_DATABASE_URL") + " search_path=" + pool.Config().ConnConfig.RuntimeParams["search_path"]
			if c.name == "unreachable" {
				dsn = "host=127.0.0.1 port=1 user=m17_credentials_canary dbname=missing password=m17_secret_canary connect_timeout=1 sslmode=disable"
			}
			if c.damage != "" {
				if _, err := pool.Exec(context.Background(), c.damage); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(os.Getenv("RESONANCE_M17_SERVER_EXE"), "-addr", "127.0.0.1:0")
			cmd.Env = append(os.Environ(), "RESONANCE_DATABASE_URL="+dsn)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatal("incompatible startup succeeded")
			}
			for _, private := range []string{"m17_secret_canary", "m17_credentials_canary", "SQLSTATE", "canonical_path"} {
				if bytes.Contains(out, []byte(private)) {
					t.Fatal("startup leaked private detail")
				}
			}
		})
	}
}
