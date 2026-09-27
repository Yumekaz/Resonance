//go:build !windows

package library

import (
	"os"
	"testing"
)

func TestRootIdentityNeverAcceptsUnbornDeviceInodeEvidence(t *testing.T) {
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	identity, ok, err := platformRootIdentity(f)
	if err != nil {
		t.Fatal(err)
	}
	if ok && len(identity.BirthToken) == 0 {
		t.Fatal("device/inode without birth evidence was accepted for destructive reconciliation")
	}
}
