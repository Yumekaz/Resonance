//go:build windows && integration

package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsJunctionSkippedDuringScan(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	rootDir := testWorkspaceDir(t)
	outside := testWorkspaceDir(t)
	writeFile(t, filepath.Join(outside, "outside.wav"), fixtureWAV())
	link := filepath.Join(rootDir, "escape")
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:RESONANCE_TEST_LINK -Target $env:RESONANCE_TEST_TARGET | Out-Null`)
	cmd.Env = append(os.Environ(), "RESONANCE_TEST_LINK="+link, "RESONANCE_TEST_TARGET="+outside)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v %s", err, output)
	}
	defer os.Remove(link)
	root := addRoot(t, s, rootDir, "junction")
	result, err := testScanner(s).Scan(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 || countRows(t, pool, "media_locations") != 0 {
		t.Fatalf("junction was traversed: %#v", result)
	}
}
