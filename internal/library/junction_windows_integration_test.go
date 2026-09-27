//go:build integration && windows

package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestM16WindowsJunctionIsNotTraversedOrWatched(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	parent := testWorkspaceDir(t)
	rootPath := filepath.Join(parent, "root")
	external := filepath.Join(parent, "external")
	if err := os.MkdirAll(rootPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(external, "outside.wav"), fixtureWAV())
	junction := filepath.Join(rootPath, "linked-library")
	command := "$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path " + quotePowerShellPath(junction) + " -Target " + quotePowerShellPath(external) + " | Out-Null"
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
	if err != nil {
		safeOutput := strings.ReplaceAll(string(output), junction, "<junction>")
		safeOutput = strings.ReplaceAll(safeOutput, external, "<target>")
		t.Fatalf("create Windows junction failed: %s", strings.TrimSpace(safeOutput))
	}
	root := addRoot(t, s, rootPath, "junction test")
	result, err := testScanner(s).Scan(context.Background(), root.ID)
	if err != nil || !result.TraversalComplete || !result.AbsenceReconciled {
		t.Fatalf("junction scan: %#v %v", result, err)
	}
	if countRows(t, pool, "media_locations") != 0 {
		t.Fatal("scanner imported media through an NTFS junction")
	}
	for _, relative := range result.WatchedDirectories {
		if strings.Contains(strings.ToLower(relative), "linked-library") {
			t.Fatalf("scanner proposed a watch inside an NTFS junction: %q", relative)
		}
	}
	for _, item := range result.WatchedDirectories {
		if filepath.IsAbs(item) || item == ".." || strings.HasPrefix(filepath.Clean(item), ".."+string(filepath.Separator)) {
			t.Fatalf("watch list escaped root-relative form: %q", item)
		}
	}
	state, err := s.GetRoot(context.Background(), root.ID)
	if err != nil || state.VerificationState != "verified" {
		t.Fatalf("junction affected root verification: %#v %v", state, err)
	}
}

func quotePowerShellPath(path string) string { return `'` + strings.ReplaceAll(path, `'`, `''`) + `'` }
