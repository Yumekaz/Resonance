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

func TestWindowsCaseOnlyRenameKeepsOneActiveLocation(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	oldPath := filepath.Join(dir, "case.wav")
	newPath := filepath.Join(dir, "CASE.wav")
	writeFile(t, oldPath, fixtureWAV())
	root := addRoot(t, s, dir, "case-only rename")
	scanner := testScanner(s)
	scanner.nativeIdentity = noNativeIdentityProvider{}
	if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
		t.Fatal(err)
	}
	old := locationsForRoot(t, pool, root.ID)[0]
	temporaryPath := filepath.Join(dir, "case-temp.wav")
	if err := os.Rename(oldPath, temporaryPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporaryPath, newPath); err != nil {
		t.Fatal(err)
	}
	result, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || result.FilesHashed != 0 || result.FilesUnchanged != 1 || result.LocationsMoved != 1 {
		t.Fatalf("case-only rename: %#v %v", result, err)
	}
	locations := locationsForRoot(t, pool, root.ID)
	if len(locations) != 1 || locations[0].ID != old.ID || locations[0].Path != "CASE.wav" || locations[0].Availability != "available" {
		t.Fatalf("case-only rename created duplicate identity: %#v", locations)
	}
}

func TestWindowsKnownLocationReplacedBySymlinkIsDirectlyUnavailable(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	rootDir := testWorkspaceDir(t)
	outsideDir := testWorkspaceDir(t)
	knownPath := filepath.Join(rootDir, "known.wav")
	outsidePath := filepath.Join(outsideDir, "outside.wav")
	writeFile(t, knownPath, fixtureWAV())
	writeFile(t, outsidePath, fixtureWAV())
	root := addRoot(t, s, rootDir, "known symlink replacement")
	scanner := testScanner(s)
	scanner.nativeIdentity = noNativeIdentityProvider{}
	if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
		t.Fatal(err)
	}
	old := locationsForRoot(t, pool, root.ID)[0]
	if err := os.Remove(knownPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, knownPath); err != nil {
		t.Skipf("file symlink creation is unavailable: %v", err)
	}
	result, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || !result.TraversalComplete || !result.AbsenceReconciled || result.LocationsUnavailable != 1 || result.TracksCreated != 0 || result.MediaObjectsCreated != 0 {
		t.Fatalf("symlink replacement scan: %#v %v", result, err)
	}
	locations := locationsForRoot(t, pool, root.ID)
	if len(locations) != 1 || locations[0].ID != old.ID || locations[0].Availability != "unavailable" || locations[0].Reason == nil || *locations[0].Reason != "symlink" {
		t.Fatalf("known location symlink replacement: %#v", locations)
	}
}
