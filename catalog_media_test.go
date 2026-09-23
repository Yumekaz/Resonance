package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"resonance/internal/library"
	"resonance/internal/storage"
)

func catalogWorkspaceTempDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempRoot := filepath.Join(cwd, "data")
	if err := os.MkdirAll(tempRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMP", tempRoot)
	return t.TempDir()
}

func TestPublicCatalogStructsHaveNoHostPathFields(t *testing.T) {
	for _, value := range []any{storage.CatalogTrack{}, storage.CatalogArtist{}, storage.CatalogAlbum{}} {
		typ := reflect.TypeOf(value)
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := field.Tag.Get("json")
			if name == "-" {
				continue
			}
			for _, part := range []string{"path", "root", "location"} {
				if strings.Contains(strings.ToLower(name), part) {
					t.Fatalf("%s serializes host-location field %q", typ.Name(), name)
				}
			}
		}
	}
}

func TestVerifiedArtworkRejectsWrongHashAndMIME(t *testing.T) {
	png, e := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/dXcAAAAASUVORK5CYII=")
	if e != nil {
		t.Fatal(e)
	}
	base, e := os.ReadFile("testdata/metadata/untagged.mp3")
	if e != nil {
		t.Fatal(e)
	}
	picture := append([]byte{0}, []byte("image/png")...)
	picture = append(picture, 0, 3, 0)
	picture = append(picture, png...)
	frame := make([]byte, 10)
	copy(frame, "APIC")
	binary.BigEndian.PutUint32(frame[4:], uint32(len(picture)))
	frame = append(frame, picture...)
	n := len(frame)
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 127), byte(n >> 14 & 127), byte(n >> 7 & 127), byte(n & 127)}
	path := filepath.Join(t.TempDir(), "art.mp3")
	if e = os.WriteFile(path, append(append(header, frame...), base...), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	mime := "image/png"
	hash := sha256.Sum256(png)
	candidate := storage.PlaybackCandidate{ArtworkSHA256: hash[:], ArtworkMIME: &mime}
	data, gotMIME, e := verifiedArtwork(f, candidate)
	if e != nil || gotMIME != mime || string(data) != string(png) {
		t.Fatalf("valid artwork: mime=%q error=%v", gotMIME, e)
	}
	wrong := sha256.Sum256([]byte("wrong"))
	candidate.ArtworkSHA256 = wrong[:]
	if _, _, e = verifiedArtwork(f, candidate); e == nil {
		t.Fatal("wrong hash accepted")
	}
	mime = "image/gif"
	candidate.ArtworkSHA256 = hash[:]
	if _, _, e = verifiedArtwork(f, candidate); e == nil {
		t.Fatal("unsupported MIME accepted")
	}
}

func TestStoredMediaPathValidation(t *testing.T) {
	for _, bad := range []string{"", "../secret.mp3", "a/../secret.mp3", "a//b.mp3", "a:b.mp3", "/absolute.mp3", "a\x00b.mp3"} {
		if validRelativeMediaPath(bad) {
			t.Fatalf("accepted unsafe path %q", bad)
		}
	}
	if !validRelativeMediaPath(filepath.Join("Artist", "Album", "song.wav")) {
		t.Fatal("valid enrolled relative path rejected")
	}
}

func TestCatalogOpenRejectsLinksAndChangedNativeEvidence(t *testing.T) {
	root := catalogWorkspaceTempDir(t)
	file := filepath.Join(root, "song.mp3")
	if err := os.WriteFile(file, []byte("bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	canonicalRoot, err := library.CanonicalizeRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	candidate := storage.PlaybackCandidate{RootPath: canonicalRoot, RelativePath: "song.mp3", ObservedSize: &size, ObservedMTimeNS: &mtime}
	unverified := candidate
	unverified.ObservedMTimeNS = nil
	if f, err := openCatalogCandidate(unverified); err == nil {
		f.Close()
		t.Fatal("location without scan mtime was accepted")
	}
	f, err := openCatalogCandidate(candidate)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	link := filepath.Join(root, "link.mp3")
	if err := os.Symlink(file, link); err == nil {
		candidate.RelativePath = "link.mp3"
		if f, err := openCatalogCandidate(candidate); err == nil {
			f.Close()
			t.Fatal("link was accepted")
		}
	}
	if runtime.GOOS == "windows" {
		kind := "windows_file_index"
		scope := "wrong-volume"
		candidate.RelativePath = "song.mp3"
		candidate.NativeKind = &kind
		candidate.NativeScope = &scope
		candidate.NativeID = []byte{1, 2, 3}
		if f, err := openCatalogCandidate(candidate); err == nil {
			f.Close()
			t.Fatal("changed native evidence accepted")
		}
	}
}

func TestCatalogOpenRejectsPathReplacementAfterOpen(t *testing.T) {
	root := catalogWorkspaceTempDir(t)
	file := filepath.Join(root, "song.mp3")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	canonicalRoot, err := library.CanonicalizeRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	candidate := storage.PlaybackCandidate{RootPath: canonicalRoot, RelativePath: "song.mp3", ObservedSize: &size, ObservedMTimeNS: &mtime}
	replaced := false
	f, err := openCatalogCandidateWithHook(candidate, func() {
		if e := os.Rename(file, filepath.Join(root, "detached.tmp")); e != nil {
			t.Skipf("filesystem does not permit renaming an open file: %v", e)
		}
		if e := os.WriteFile(file, []byte("newbytes"), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Chtimes(file, info.ModTime(), info.ModTime()); e != nil {
			t.Fatal(e)
		}
		replaced = true
	})
	if f != nil {
		f.Close()
	}
	if !replaced || err == nil {
		t.Fatalf("stale open handle survived path replacement: replaced=%v error=%v", replaced, err)
	}
}

func TestCatalogOpenRejectsRedirectedEnrolledRoot(t *testing.T) {
	parent := catalogWorkspaceTempDir(t)
	enrolled := filepath.Join(parent, "enrolled")
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(enrolled, 0700); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := library.CanonicalizeRoot(enrolled)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(enrolled, enrolled+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "song.mp3")
	if err := os.WriteFile(file, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, enrolled); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("directory links unavailable: %v", err)
		}
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:RESONANCE_TEST_LINK -Target $env:RESONANCE_TEST_TARGET | Out-Null`)
		cmd.Env = append(os.Environ(), "RESONANCE_TEST_LINK="+enrolled, "RESONANCE_TEST_TARGET="+outside)
		if output, e := cmd.CombinedOutput(); e != nil {
			t.Skipf("directory junction unavailable: %v %s", e, output)
		}
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	candidate := storage.PlaybackCandidate{RootPath: canonicalRoot, RelativePath: "song.mp3", ObservedSize: &size, ObservedMTimeNS: &mtime}
	if f, err := openCatalogCandidate(candidate); err == nil {
		f.Close()
		t.Fatal("redirected enrolled root exposed an outside file")
	}
}
