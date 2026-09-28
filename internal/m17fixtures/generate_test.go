package m17fixtures

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"resonance/internal/metadata"
)

func source(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "metadata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestScaleCorpusDistinctTagsAndManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scale")
	summary, err := GenerateScale(root, source(t, "untagged.mp3"), 250, 20260928)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TrackCount != 250 || summary.FileCount != 250 || summary.UniqueSHA256 != 250 || summary.ArtistCount != 3 || summary.AlbumCount != 25 {
		t.Fatalf("unexpected scale summary: %+v", summary)
	}
	manifest, err := os.Open(filepath.Join(root, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	seen := map[string]bool{}
	decoder := json.NewDecoder(manifest)
	for i := 0; i < summary.FileCount; i++ {
		var entry Entry
		if err := decoder.Decode(&entry); err != nil {
			t.Fatalf("manifest row %d: %v", i, err)
		}
		if seen[entry.SHA256] {
			t.Fatalf("duplicate file hash: %s", entry.SHA256)
		}
		seen[entry.SHA256] = true
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != entry.SHA256 {
			t.Fatalf("manifest hash mismatch for %s", entry.Path)
		}
		parsed, _, err := metadata.Read(bytes.NewReader(data))
		if err != nil || parsed.Title == nil || *parsed.Title != entry.Title || parsed.Artist == nil || *parsed.Artist != entry.Artist || parsed.Album == nil || *parsed.Album != entry.Album {
			t.Fatalf("invalid fixture %s: metadata=%+v err=%v", entry.Path, parsed, err)
		}
	}
	if _, err := GenerateScale(root, source(t, "untagged.mp3"), 250, 20260928); err == nil {
		t.Fatal("existing output directory was overwritten")
	}
}

func TestJourneyCorpusKeepsScaleSeparateFromLongPlayback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "journey")
	summary, err := GenerateJourney(root, source(t, "untagged.mp3"), source(t, "untagged.flac"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != "journey" || summary.TrackCount != 9 || summary.FileCount != 14 || summary.UniqueSHA256 >= summary.FileCount {
		t.Fatalf("unexpected journey summary: %+v", summary)
	}
	long, err := os.Stat(filepath.Join(root, "library", "long.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if long.Size() != 44+300*44100*2 {
		t.Fatalf("long WAV bytes = %d", long.Size())
	}
	first, err := os.ReadFile(filepath.Join(root, "library", "Browser Artist", "Browser Album", "Browser Song.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	copyBytes, err := os.ReadFile(filepath.Join(root, "library", "Exact Copy", "Browser Song Copy.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, copyBytes) {
		t.Fatal("exact-copy journey fixture differs at the byte level")
	}
	if _, err := os.Stat(filepath.Join(root, "negative-fixtures", "malformed.mp3")); err != nil {
		t.Fatal(err)
	}
}
