//go:build windows

package library

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsCanonicalizationCollapsesCaseAndJunctionAliases(t *testing.T) {
	target := testWorkspaceDir(t)
	canonical, err := CanonicalizeRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	caseAlias, err := CanonicalizeRoot(strings.ToUpper(target))
	if err != nil || caseAlias != canonical {
		t.Fatalf("case alias not canonicalized: %q %q %v", canonical, caseAlias, err)
	}
	container := testWorkspaceDir(t)
	link := filepath.Join(container, "alias")
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:RESONANCE_TEST_LINK -Target $env:RESONANCE_TEST_TARGET | Out-Null`)
	cmd.Env = append(os.Environ(), "RESONANCE_TEST_LINK="+link, "RESONANCE_TEST_TARGET="+target)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v %s", err, output)
	}
	defer os.Remove(link)
	junctionAlias, err := CanonicalizeRoot(link)
	if err != nil || junctionAlias != canonical {
		t.Fatalf("junction alias not canonicalized: %q %q %v", canonical, junctionAlias, err)
	}
}

func TestWindowsEnrollmentRejectsDeviceAndAlternateStreamPaths(t *testing.T) {
	dir := testWorkspaceDir(t)
	for _, path := range []string{`\\.\NUL`, `\\?\GLOBALROOT\Device\Null`, dir + ":stream"} {
		if got, err := CanonicalizeRoot(path); err == nil {
			t.Fatalf("unsafe root accepted: %q -> %q", path, got)
		}
	}
}

func TestWindowsRootRejectsTraversalAbsoluteAndDeviceNames(t *testing.T) {
	dir := testWorkspaceDir(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{`..\outside.wav`, filepath.Join(dir, "absolute.wav"), `NUL`, `COM1`} {
		if file, err := root.Open(name); err == nil {
			file.Close()
			t.Fatalf("unsafe root-relative name accepted: %q", name)
		}
	}
}
