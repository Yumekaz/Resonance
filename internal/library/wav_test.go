package library

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testWorkspaceDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "data", "m12-test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		rel, e := filepath.Rel(root, dir)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Errorf("unsafe cleanup target %q", dir)
			return
		}
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
	})
	return dir
}

func TestDatabaseFailurePreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := databaseFailure(ctx); err != context.Canceled {
		t.Fatalf("cancellation mislabeled: %v", err)
	}
	if err := databaseFailure(context.Background()); err != errDatabase {
		t.Fatalf("database failure mislabeled: %v", err)
	}
}

func TestTraversalEntryLimitIncludesNonFiles(t *testing.T) {
	result := ScanResult{entriesVisited: maxVisited}
	if err := recordTraversalEntry(&result); err != errLimit {
		t.Fatalf("entry traversal cap not enforced: %v", err)
	}
}

func fixtureWAV() []byte {
	b := make([]byte, 44+200)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 44100)
	binary.LittleEndian.PutUint32(b[28:], 88200)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 200)
	return b
}

func TestPCMEnvelope(t *testing.T) {
	good := fixtureWAV()
	if err := validatePCM(bytes.NewReader(good), int64(len(good))); err != nil {
		t.Fatal(err)
	}
	bad := append([]byte{}, good...)
	binary.LittleEndian.PutUint16(bad[20:], 3)
	if err := validatePCM(bytes.NewReader(bad), int64(len(bad))); err == nil {
		t.Fatal("non-PCM accepted")
	}
	if err := validatePCM(bytes.NewReader(good[:30]), 30); err == nil {
		t.Fatal("truncated accepted")
	}
}

func TestCanonicalizeRoot(t *testing.T) {
	dir := testWorkspaceDir(t)
	got, err := CanonicalizeRoot(dir)
	if err != nil || !filepath.IsAbs(got) {
		t.Fatalf("root %q: %v", got, err)
	}
	if _, err := CanonicalizeRoot(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing root accepted")
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalizeRoot(file); err == nil {
		t.Fatal("file root accepted")
	}
}
