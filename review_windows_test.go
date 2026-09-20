//go:build windows

package main

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsJunctionContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.wav"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	// Fixed script; paths are environment data, never interpolated into code.
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:RESONANCE_TEST_LINK -Target $env:RESONANCE_TEST_TARGET | Out-Null`)
	cmd.Env = append(os.Environ(), "RESONANCE_TEST_LINK="+link, "RESONANCE_TEST_TARGET="+outside)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v %s", err, output)
	}
	defer os.Remove(link)
	a := &app{tracks: map[string]track{"demo-track": {root: root, filename: "escape/secret.wav"}}, log: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	r := httptest.NewRequest("GET", "/media/demo-track", nil)
	r.SetPathValue("id", "demo-track")
	w := httptest.NewRecorder()
	a.media(w, r)
	if w.Code != 503 {
		t.Fatalf("junction escaped root: %d %q", w.Code, w.Body.String())
	}
}
