//go:build integration

package library

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"resonance/internal/storage"
)

func isolatedLibraryStore(t *testing.T) (*storage.Store, *pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("RESONANCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("RESONANCE_TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "librarytest_" + hex.EncodeToString(random[:])
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	scoped := dsn
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		scoped = u.String()
	} else {
		scoped = dsn + " search_path=" + schema
	}
	store, err := storage.Open(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	queryPool, err := pgxpool.New(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		queryPool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	return store, queryPool, scoped
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureMP3(t *testing.T) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "metadata", "untagged.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	textFrame := func(name, value string) []byte {
		payload := []byte{1, 0xff, 0xfe}
		for _, r := range utf16.Encode([]rune(value)) {
			var b [2]byte
			binary.LittleEndian.PutUint16(b[:], r)
			payload = append(payload, b[:]...)
		}
		frame := make([]byte, 10)
		copy(frame, name)
		binary.BigEndian.PutUint32(frame[4:], uint32(len(payload)))
		return append(frame, payload...)
	}
	frames := append(textFrame("TIT2", "Tagged Song"), textFrame("TPE1", "One & Only Artist")...)
	frames = append(frames, textFrame("TALB", "One Album")...)
	frames = append(frames, textFrame("TPE2", "Album Credit")...)
	frames = append(frames, textFrame("TRCK", "2/10")...)
	frames = append(frames, textFrame("TPOS", "1/2")...)
	frames = append(frames, textFrame("TCON", "Jazz")...)
	frames = append(frames, textFrame("TYER", "2024")...)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/dXcAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	picture := append([]byte{0}, []byte("image/png")...)
	picture = append(picture, 0, 3, 0)
	picture = append(picture, png...)
	frames = append(frames, func() []byte {
		f := make([]byte, 10)
		copy(f, "APIC")
		binary.BigEndian.PutUint32(f[4:], uint32(len(picture)))
		return append(f, picture...)
	}()...)
	n := len(frames)
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 127), byte(n >> 14 & 127), byte(n >> 7 & 127), byte(n & 127)}
	return append(append(header, frames...), content...)
}

func addRoot(t *testing.T, s *storage.Store, path, name string) storage.LibraryRoot {
	t.Helper()
	canonical, err := CanonicalizeRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(context.Background(), name, canonical)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func testScanner(s *storage.Store) *Scanner {
	return &Scanner{Store: s, Log: slog.New(slog.NewJSONHandler(io.Discard, nil))}
}

func TestInitialImportTwoRootsDedupAndRestart(t *testing.T) {
	s, pool, dsn := isolatedLibraryStore(t)
	base := testWorkspaceDir(t)
	a := filepath.Join(base, "root-a")
	b := filepath.Join(base, "root-b")
	if err := os.MkdirAll(a, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0700); err != nil {
		t.Fatal(err)
	}
	encoded := fixtureMP3(t)
	writeFile(t, filepath.Join(a, "album", "tagged.mp3"), encoded)
	writeFile(t, filepath.Join(a, "夜の歌.wav"), fixtureWAV())
	writeFile(t, filepath.Join(b, "copy.mp3"), encoded)
	flac, err := os.ReadFile(filepath.Join("..", "..", "testdata", "metadata", "untagged.flac"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(b, "untagged.flac"), flac)
	writeFile(t, filepath.Join(b, "bad.mp3"), []byte("ID3\x03\x00\x00\x7f\x7f\x7f\x7f"))
	writeFile(t, filepath.Join(b, "bad.flac"), []byte("fLaCgarbage"))
	writeFile(t, filepath.Join(b, "notes.txt"), []byte("not audio"))
	rootA := addRoot(t, s, a, "A")
	rootB := addRoot(t, s, b, "B")
	if _, err := s.AddRoot(context.Background(), "Duplicate", a); !errors.Is(err, storage.ErrRootOverlap) {
		t.Fatalf("duplicate root: %v", err)
	}
	if _, err := s.AddRoot(context.Background(), "Nested", filepath.Join(a, "album")); !errors.Is(err, storage.ErrRootOverlap) {
		t.Fatalf("nested root: %v", err)
	}
	listed, err := s.ListRoots(context.Background())
	if err != nil || len(listed) != 2 || listed[0].CanonicalPath != "" {
		t.Fatalf("public list: %#v %v", listed, err)
	}
	first, err := testScanner(s).Scan(context.Background(), rootA.ID)
	if err != nil || first.Imported != 2 {
		t.Fatalf("first scan: %#v %v", first, err)
	}
	second, err := testScanner(s).Scan(context.Background(), rootB.ID)
	if err != nil || second.Imported != 2 || second.Failed != 2 || second.Status != "partial" {
		t.Fatalf("second scan: %#v %v", second, err)
	}
	if countRows(t, pool, "tracks") != 3 || countRows(t, pool, "media_objects") != 3 || countRows(t, pool, "media_locations") != 4 {
		t.Fatal("identity or dedup counts wrong")
	}
	var title, artist, album, albumArtist, genre string
	var track, disc, year int
	if err := pool.QueryRow(context.Background(), `SELECT title,artist_credit,album_title,album_artist_credit,genre,track_number,disc_number,release_year FROM tracks WHERE title='Tagged Song'`).Scan(&title, &artist, &album, &albumArtist, &genre, &track, &disc, &year); err != nil {
		t.Fatal(err)
	}
	if artist != "One & Only Artist" || album != "One Album" || albumArtist != "Album Credit" || genre != "Jazz" || track != 2 || disc != 1 || year != 2024 {
		t.Fatalf("metadata: %q %q %q %q %d %d %d", artist, album, albumArtist, genre, track, disc, year)
	}
	var artCount int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM media_objects WHERE artwork_sha256 IS NOT NULL AND artwork_mime='image/png'").Scan(&artCount); err != nil || artCount != 1 {
		t.Fatalf("artwork reference: %d %v", artCount, err)
	}
	var unknownCount int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM tracks WHERE artist_credit IS NULL AND album_title IS NULL").Scan(&unknownCount); err != nil || unknownCount != 2 {
		t.Fatalf("unknowns merged: %d %v", unknownCount, err)
	}
	if countRows(t, pool, "scan_errors") < 3 {
		t.Fatal("bounded errors not recorded")
	}
	// A new Store instance sees the completed import and does not rehash known locations.
	reopened, err := storage.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	repeat, err := testScanner(reopened).Scan(context.Background(), rootA.ID)
	if err != nil || repeat.Imported != 0 || repeat.BytesHashed != 0 || countRows(t, pool, "tracks") != 3 {
		t.Fatalf("restart scan: %#v %v", repeat, err)
	}
	if err := s.DisableRoot(context.Background(), rootA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testScanner(s).Scan(context.Background(), rootA.ID); !errors.Is(err, storage.ErrRootDisabled) {
		t.Fatalf("disabled scan: %v", err)
	}
	if countRows(t, pool, "media_locations") != 4 {
		t.Fatal("disable deleted catalog locations")
	}
}

func TestByteIdentityDoesNotBecomeUniversalTrackIdentity(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	first := fixtureMP3(t)
	second := append(append([]byte{}, first...), 0)
	writeFile(t, filepath.Join(dir, "first.mp3"), first)
	writeFile(t, filepath.Join(dir, "first-copy.mp3"), first)
	writeFile(t, filepath.Join(dir, "second-encoding.mp3"), second)
	root := addRoot(t, s, dir, "identity")
	result, err := testScanner(s).Scan(context.Background(), root.ID)
	if err != nil || result.Imported != 3 {
		t.Fatalf("scan: %#v %v", result, err)
	}
	if countRows(t, pool, "tracks") != 2 || countRows(t, pool, "media_objects") != 2 || countRows(t, pool, "media_locations") != 3 {
		t.Fatal("byte deduplication collapsed distinct encoded objects into one Track or failed to share an exact copy")
	}
	var distinctTrackIDs, hashDerivedTrackIDs int
	if err := pool.QueryRow(context.Background(), `SELECT count(DISTINCT track_id), count(*) FILTER (WHERE track_id LIKE 'obj_%') FROM media_objects`).Scan(&distinctTrackIDs, &hashDerivedTrackIDs); err != nil {
		t.Fatal(err)
	}
	if distinctTrackIDs != 2 || hashDerivedTrackIDs != 0 {
		t.Fatalf("track identity coupled to byte identity: tracks=%d hash_ids=%d", distinctTrackIDs, hashDerivedTrackIDs)
	}
}

func TestFilesystemFailuresContinue(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	writeFile(t, filepath.Join(dir, "ok.wav"), fixtureWAV())
	writeFile(t, filepath.Join(dir, "denied.mp3"), fixtureMP3(t))
	writeFile(t, filepath.Join(dir, "vanished.mp3"), fixtureMP3(t))
	outside := filepath.Join(testWorkspaceDir(t), "outside.mp3")
	writeFile(t, outside, fixtureMP3(t))
	if err := os.Symlink(outside, filepath.Join(dir, "escape.mp3")); err != nil {
		t.Logf("symlink creation unavailable: %v", err)
	}
	root := addRoot(t, s, dir, "failures")
	scanner := testScanner(s)
	scanner.openFile = func(r *os.Root, name string) (*os.File, error) {
		switch filepath.Base(name) {
		case "denied.mp3":
			return nil, os.ErrPermission
		case "vanished.mp3":
			_ = os.Remove(filepath.Join(dir, name))
			return nil, os.ErrNotExist
		default:
			return r.Open(name)
		}
	}
	result, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || result.Imported != 1 || result.Failed < 2 || result.Status != "partial" {
		t.Fatalf("failure continuation: %#v %v", result, err)
	}
	if countRows(t, pool, "media_locations") != 1 {
		t.Fatal("escaped or failed file imported")
	}
	var denied, vanished int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM scan_errors WHERE code='permission_denied'").Scan(&denied)
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM scan_errors WHERE code='file_unavailable'").Scan(&vanished)
	if denied != 1 || vanished != 1 {
		t.Fatalf("error codes: %d %d", denied, vanished)
	}
}

func TestMissingRootAndScanLease(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	root := addRoot(t, s, dir, "missing")
	other := addRoot(t, s, testWorkspaceDir(t), "other")
	lease, _, err := s.BeginScan(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.BeginScan(context.Background(), root.ID); !errors.Is(err, storage.ErrScanRunning) {
		t.Fatalf("overlap: %v", err)
	}
	if _, _, err := s.BeginScan(context.Background(), other.ID); !errors.Is(err, storage.ErrScanRunning) {
		t.Fatalf("global worker limit: %v", err)
	}
	lease.Close()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	result, err := testScanner(s).Scan(context.Background(), root.ID)
	if err == nil || result.Status != "failed" || result.ErrorCode != "root_unavailable" {
		t.Fatalf("missing root: %#v %v", result, err)
	}
	if countRows(t, pool, "scan_runs") != 2 {
		t.Fatal("missing root run not persisted")
	}
}

func TestEmptyRootSucceeds(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	root := addRoot(t, s, testWorkspaceDir(t), "empty")
	result, err := testScanner(s).Scan(context.Background(), root.ID)
	if err != nil || result.Status != "succeeded" || result.FilesVisited != 0 || result.Imported != 0 || result.Failed != 0 {
		t.Fatalf("empty root: %#v %v", result, err)
	}
	if countRows(t, pool, "scan_runs") != 1 {
		t.Fatal("empty scan not recorded")
	}
}

func TestErrorRecordsAreBounded(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	for i := 0; i < 150; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("note-%03d.txt", i)), []byte("not audio"))
	}
	root := addRoot(t, s, dir, "many unsupported")
	result, err := testScanner(s).Scan(context.Background(), root.ID)
	if err != nil || result.Skipped != 150 || countRows(t, pool, "scan_errors") != 100 {
		t.Fatalf("error cap: %#v %v", result, err)
	}
}

func TestCanceledScanAndDatabaseFailure(t *testing.T) {
	s, pool, dsn := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	writeFile(t, filepath.Join(dir, "first.wav"), fixtureWAV())
	writeFile(t, filepath.Join(dir, "second.wav"), fixtureWAV())
	root := addRoot(t, s, dir, "interrupted")
	ctx, cancel := context.WithCancel(context.Background())
	scanner := testScanner(s)
	scanner.openFile = func(r *os.Root, name string) (*os.File, error) { cancel(); return r.Open(name) }
	canceled, err := scanner.Scan(ctx, root.ID)
	if !errors.Is(err, context.Canceled) || canceled.Status != "canceled" {
		t.Fatalf("cancel: %#v %v", canceled, err)
	}
	// Kill the pool during a later file's import. The run remains recoverable as
	// interrupted when another process acquires the scan lock.
	opened := 0
	scanner = testScanner(s)
	scanner.openFile = func(r *os.Root, name string) (*os.File, error) {
		opened++
		if opened == 2 {
			s.Close()
		}
		return r.Open(name)
	}
	failed, err := scanner.Scan(context.Background(), root.ID)
	if err == nil || failed.ErrorCode != "database_unavailable" {
		t.Fatalf("database failure: %#v %v", failed, err)
	}
	reopened, err := storage.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	final, err := testScanner(reopened).Scan(context.Background(), root.ID)
	if err != nil || final.Status != "succeeded" {
		t.Fatalf("recovery: %#v %v", final, err)
	}
	var interrupted int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM scan_runs WHERE error_code='interrupted'").Scan(&interrupted); err != nil || interrupted != 1 {
		t.Fatalf("interrupted run: %d %v", interrupted, err)
	}
}
