package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsOverlap(t *testing.T) {
	base := filepath.Join(t.TempDir(), "music")
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{base, base, true},
		{base, filepath.Join(base, "album"), true},
		{filepath.Join(base, "album"), base, true},
		{base, base + "-other", false},
		{base, filepath.Join(filepath.Dir(base), "different"), false},
	} {
		if got := pathsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("overlap %q %q = %v", tc.a, tc.b, got)
		}
	}
}

func TestLibraryRootJSONNeverContainsCanonicalPath(t *testing.T) {
	root := LibraryRoot{ID: "00000000-0000-4000-8000-000000000000", Name: "private", Enabled: true, CanonicalPath: `C:\Users\owner\Music`}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Users") || strings.Contains(string(encoded), "canonical") {
		t.Fatalf("private path serialized: %s", encoded)
	}
}

func TestTruncateTextPreservesUTF8(t *testing.T) {
	value := strings.Repeat("夜", 513)
	got := truncateText(value, 512)
	if got != strings.Repeat("夜", 512) {
		t.Fatalf("rune-aware truncation failed: bytes=%d", len(got))
	}
	got = truncateText(strings.ToValidUTF8("bad\xffpath", "�"), 512)
	if !strings.Contains(got, "�") {
		t.Fatalf("invalid UTF-8 was not made safe: %q", got)
	}
}

func TestRootIDValidation(t *testing.T) {
	id, err := newUUID()
	if err != nil || !validUUID(id) {
		t.Fatalf("UUID %q: %v", id, err)
	}
	for _, bad := range []string{"", "../secret", "00000000-0000-0000-0000-00000000000z"} {
		if validUUID(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}
