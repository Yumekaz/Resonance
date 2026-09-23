//go:build windows

package library

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsNativeIdentityComesFromStableOpenHandleEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.wav")
	if err := os.WriteFile(path, fixtureWAV(), 0600); err != nil {
		t.Fatal(err)
	}
	provider := windowsNativeIdentityProvider{}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, ok, err := provider.FromOpenFile(file)
	if err != nil || !ok || first.Kind != "windows_file_index" || first.Scope == "" || len(first.ID) != 8 || len(first.BirthToken) != 8 {
		file.Close()
		t.Fatalf("Windows handle identity: %#v %v %v", first, ok, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	second, ok, err := provider.FromOpenFile(file)
	if err != nil || !ok || first.Kind != second.Kind || first.Scope != second.Scope || !bytes.Equal(first.ID, second.ID) || !bytes.Equal(first.BirthToken, second.BirthToken) {
		t.Fatalf("same file identity changed across handles: %#v %#v %v %v", first, second, ok, err)
	}
}
