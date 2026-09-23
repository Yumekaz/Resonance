//go:build integration

package library

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"time"
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
	return fixtureMP3WithTitle(t, "Tagged Song")
}

func fixtureMP3WithTitle(t *testing.T, title string) []byte {
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
	frames := append(textFrame("TIT2", title), textFrame("TPE1", "One & Only Artist")...)
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
	lease, _, _, err := s.BeginScan(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.BeginScan(context.Background(), root.ID); !errors.Is(err, storage.ErrScanRunning) {
		t.Fatalf("overlap: %v", err)
	}
	if _, _, _, err := s.BeginScan(context.Background(), other.ID); !errors.Is(err, storage.ErrScanRunning) {
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
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM scan_runs WHERE error_code='interrupted' AND phase='finished'").Scan(&interrupted); err != nil || interrupted != 1 {
		t.Fatalf("interrupted run: %d %v", interrupted, err)
	}
}

type testLocation struct {
	ID           string
	Path         string
	ObjectID     string
	TrackID      string
	Availability string
	Reason       *string
}

func locationsForRoot(t *testing.T, pool *pgxpool.Pool, rootID string) []testLocation {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT ml.id,ml.relative_path,ml.media_object_id,mo.track_id,ml.availability,ml.unavailable_reason
		FROM media_locations ml JOIN media_objects mo ON mo.id=ml.media_object_id WHERE ml.root_id=$1 ORDER BY ml.relative_path,ml.id`, rootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []testLocation
	for rows.Next() {
		var item testLocation
		if err := rows.Scan(&item.ID, &item.Path, &item.ObjectID, &item.TrackID, &item.Availability, &item.Reason); err != nil {
			t.Fatal(err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertLocationIDsStable(t *testing.T, before, after []testLocation) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("location count changed: before=%d after=%d", len(before), len(after))
	}
	ids := make(map[string]string, len(before))
	for _, location := range before {
		ids[location.Path] = location.ID
	}
	for _, location := range after {
		if ids[location.Path] == "" || ids[location.Path] != location.ID {
			t.Fatalf("location identity changed at %q: before=%q after=%q", location.Path, ids[location.Path], location.ID)
		}
	}
}

func testNativeProvider(id, birth []byte) NativeIdentityProvider {
	id = append([]byte(nil), id...)
	birth = append([]byte(nil), birth...)
	return FakeNativeIdentityProvider(func(*os.File) (storage.NativeIdentity, bool, error) {
		return storage.NativeIdentity{Kind: "test", Scope: "test-volume", ID: append([]byte(nil), id...), BirthToken: append([]byte(nil), birth...)}, true, nil
	})
}

func TestM13BaselineFromM12AndUnchangedRescan(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	content := fixtureWAV()
	writeFile(t, filepath.Join(dir, "baseline.wav"), content)
	root := addRoot(t, s, dir, "M1.2 baseline")
	hash := sha256.Sum256(content)
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "INSERT INTO tracks(id,title,title_source) VALUES('trk_m12_baseline','baseline','filename')"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES('obj_m12_baseline','trk_m12_baseline',$1,'wav',$2)`, hash[:], len(content)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size)
		VALUES('loc_m12_baseline','obj_m12_baseline',$1,$2,'baseline.wav',$3)`, filepath.Join(dir, "baseline.wav"), root.ID, len(content)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), "UPDATE tracks SET metadata_source_location_id='loc_m12_baseline' WHERE id='trk_m12_baseline'"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	scanner := testScanner(s)
	scanner.nativeIdentity = noNativeIdentityProvider{}
	baseline, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || baseline.Status != "succeeded" || !baseline.TraversalComplete || !baseline.ObservationsApplied || !baseline.AbsenceReconciled || baseline.FilesHashed != 1 || baseline.MetadataExtractions != 0 || baseline.TracksCreated != 0 || baseline.MediaObjectsCreated != 0 || baseline.LocationsAdded != 0 {
		t.Fatalf("baseline reconciliation: %#v %v", baseline, err)
	}
	var locationID, objectID, trackID, seenRun string
	if err := pool.QueryRow(context.Background(), `SELECT ml.id,ml.media_object_id,mo.track_id,ml.last_seen_run_id::text FROM media_locations ml JOIN media_objects mo ON mo.id=ml.media_object_id WHERE ml.root_id=$1`, root.ID).Scan(&locationID, &objectID, &trackID, &seenRun); err != nil {
		t.Fatal(err)
	}
	if locationID != "loc_m12_baseline" || objectID != "obj_m12_baseline" || trackID != "trk_m12_baseline" || seenRun != baseline.RunID {
		t.Fatalf("M1.2 identities were not preserved: %q %q %q %q", locationID, objectID, trackID, seenRun)
	}
	unchanged, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || unchanged.Status != "succeeded" || unchanged.FilesHashed != 0 || unchanged.BytesHashed != 0 || unchanged.MetadataExtractions != 0 || unchanged.FilesUnchanged != 1 || unchanged.TracksCreated != 0 || unchanged.MediaObjectsCreated != 0 || unchanged.LocationsAdded != 0 {
		t.Fatalf("unchanged rescan: %#v %v", unchanged, err)
	}
}

func TestM13MtimeChangeAndExactCopy(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	encoded := fixtureMP3(t)
	file := filepath.Join(dir, "song.mp3")
	writeFile(t, file, encoded)
	root := addRoot(t, s, dir, "stat and copy")
	scanner := testScanner(s)
	first, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || first.Imported != 1 || first.TracksCreated != 1 || first.MediaObjectsCreated != 1 || first.MetadataExtractions != 1 {
		t.Fatalf("initial import: %#v %v", first, err)
	}
	before := locationsForRoot(t, pool, root.ID)[0]
	changedTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(file, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	mtimeScan, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || mtimeScan.FilesHashed != 1 || mtimeScan.StatChangedSameBytes != 1 || mtimeScan.MetadataExtractions != 0 {
		t.Fatalf("mtime-only change: %#v %v", mtimeScan, err)
	}
	after := locationsForRoot(t, pool, root.ID)[0]
	if before.ID != after.ID || before.ObjectID != after.ObjectID || before.TrackID != after.TrackID {
		t.Fatal("mtime-only change replaced an identity")
	}
	writeFile(t, filepath.Join(dir, "copy.mp3"), encoded)
	copyScan, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || copyScan.FilesHashed != 1 || copyScan.MetadataExtractions != 0 || copyScan.LocationsAdded != 1 || copyScan.TracksCreated != 0 || copyScan.MediaObjectsCreated != 0 {
		t.Fatalf("exact copy: %#v %v", copyScan, err)
	}
	locations := locationsForRoot(t, pool, root.ID)
	if len(locations) != 2 || locations[0].ObjectID != locations[1].ObjectID || locations[0].TrackID != locations[1].TrackID {
		t.Fatalf("exact copy identities: %#v", locations)
	}
}

func TestM13RenameWithAndWithoutNativeIdentity(t *testing.T) {
	for _, withNative := range []bool{false, true} {
		name := "without_native"
		if withNative {
			name = "with_native"
		}
		t.Run(name, func(t *testing.T) {
			s, pool, _ := isolatedLibraryStore(t)
			dir := testWorkspaceDir(t)
			oldPath := filepath.Join(dir, "old.wav")
			newRelative := "new.wav"
			newPath := filepath.Join(dir, newRelative)
			if withNative {
				newRelative = filepath.Join("newdir", "new.wav")
				newPath = filepath.Join(dir, newRelative)
			}
			writeFile(t, oldPath, fixtureWAV())
			root := addRoot(t, s, dir, "rename")
			scanner := testScanner(s)
			if withNative {
				scanner.nativeIdentity = testNativeProvider([]byte{7, 1}, []byte("birth-a"))
			} else {
				scanner.nativeIdentity = noNativeIdentityProvider{}
			}
			_, err := scanner.Scan(context.Background(), root.ID)
			if err != nil {
				t.Fatal(err)
			}
			old := locationsForRoot(t, pool, root.ID)[0]
			if withNative {
				if err := os.MkdirAll(filepath.Dir(newPath), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Rename(oldPath, newPath); err != nil {
				t.Fatal(err)
			}
			moved, err := scanner.Scan(context.Background(), root.ID)
			if err != nil {
				t.Fatalf("rename scan: %#v %v", moved, err)
			}
			locations := locationsForRoot(t, pool, root.ID)
			if withNative {
				if moved.LocationsMoved != 1 || moved.LocationsAdded != 0 || moved.LocationsUnavailable != 0 || len(locations) != 1 || locations[0].ID != old.ID || filepath.FromSlash(locations[0].Path) != newRelative {
					t.Fatalf("native move did not preserve location: %#v %#v", moved, locations)
				}
			} else {
				if moved.LocationsMoved != 0 || moved.LocationsAdded != 1 || moved.LocationsUnavailable != 1 || len(locations) != 2 {
					t.Fatalf("SHA-only move reconciliation: %#v %#v", moved, locations)
				}
				var active, unavailable int
				for _, loc := range locations {
					if loc.Availability == "available" && loc.Path == "new.wav" && loc.ID != old.ID && loc.ObjectID == old.ObjectID && loc.TrackID == old.TrackID {
						active++
					}
					if loc.ID == old.ID && loc.Availability == "unavailable" && loc.Reason != nil && *loc.Reason == "not_found" {
						unavailable++
					}
				}
				if active != 1 || unavailable != 1 {
					t.Fatalf("SHA-only move identities: %#v", locations)
				}
			}
		})
	}
}

func TestM13ReplacementAndStrongNativeContinuity(t *testing.T) {
	t.Run("same_metadata_different_bytes_without_native", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		file := filepath.Join(dir, "same-title.wav")
		writeFile(t, file, fixtureWAV())
		root := addRoot(t, s, dir, "replacement")
		scanner := testScanner(s)
		scanner.nativeIdentity = noNativeIdentityProvider{}
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		old := locationsForRoot(t, pool, root.ID)[0]
		if err := os.WriteFile(file, changedWAV(1), 0600); err != nil {
			t.Fatal(err)
		}
		changedTime := time.Now().Add(3 * time.Second)
		_ = os.Chtimes(file, changedTime, changedTime)
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.ChangedBytes != 1 || result.TracksCreated != 1 || result.MediaObjectsCreated != 1 || result.LocationsAdded != 1 || result.LocationsUnavailable != 1 {
			t.Fatalf("replacement: %#v %v", result, err)
		}
		locations := locationsForRoot(t, pool, root.ID)
		if len(locations) != 2 {
			t.Fatalf("replacement occurrence count: %#v", locations)
		}
		var active, oldUnavailable int
		for _, loc := range locations {
			if loc.Availability == "available" && loc.TrackID != old.TrackID && loc.ObjectID != old.ObjectID {
				active++
			}
			if loc.ID == old.ID && loc.Availability == "unavailable" && loc.Reason != nil && *loc.Reason == "replaced" {
				oldUnavailable++
			}
		}
		if active != 1 || oldUnavailable != 1 {
			t.Fatalf("replacement identities: %#v", locations)
		}
		var titles int
		if err := pool.QueryRow(context.Background(), "SELECT count(DISTINCT title) FROM tracks WHERE title='same-title'").Scan(&titles); err != nil || titles != 1 {
			t.Fatalf("same presentation metadata was not retained as distinct identity: %d %v", titles, err)
		}
	})
	t.Run("same_native_changed_bytes", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		file := filepath.Join(dir, "retag.mp3")
		writeFile(t, file, fixtureMP3WithTitle(t, "Original Title"))
		root := addRoot(t, s, dir, "continuous edit")
		scanner := testScanner(s)
		scanner.nativeIdentity = testNativeProvider([]byte{7, 2}, []byte("birth-edit"))
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		old := locationsForRoot(t, pool, root.ID)[0]
		if err := os.WriteFile(file, fixtureMP3WithTitle(t, "Updated Title"), 0600); err != nil {
			t.Fatal(err)
		}
		changedTime := time.Now().Add(4 * time.Second)
		_ = os.Chtimes(file, changedTime, changedTime)
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.TracksCreated != 0 || result.MediaObjectsCreated != 1 || result.LocationsMoved != 0 || result.LocationsAdded != 0 || result.ChangedBytes != 1 {
			t.Fatalf("strong same-native edit: %#v %v", result, err)
		}
		locations := locationsForRoot(t, pool, root.ID)
		if len(locations) != 1 || locations[0].ID != old.ID || locations[0].TrackID != old.TrackID || locations[0].ObjectID == old.ObjectID {
			t.Fatalf("continuous edit identities: %#v", locations)
		}
		var title, source string
		if err := pool.QueryRow(context.Background(), "SELECT title,metadata_source_location_id FROM tracks WHERE id=$1", old.TrackID).Scan(&title, &source); err != nil || title != "Updated Title" || source != old.ID {
			t.Fatalf("source metadata was not updated: %q %q %v", title, source, err)
		}
	})
}

func TestM13ExistingObjectTrackWinsAndNativeIDReuseFallsBack(t *testing.T) {
	t.Run("same_sha_but_reused_native_id_creates_new_location", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		file := filepath.Join(dir, "same-bytes.wav")
		writeFile(t, file, fixtureWAV())
		root := addRoot(t, s, dir, "same bytes native reuse")
		birth := []byte("birth-one")
		scanner := testScanner(s)
		scanner.nativeIdentity = FakeNativeIdentityProvider(func(*os.File) (storage.NativeIdentity, bool, error) {
			return storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{8}, BirthToken: append([]byte(nil), birth...)}, true, nil
		})
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		old := locationsForRoot(t, pool, root.ID)[0]
		birth = []byte("birth-reused")
		changedTime := time.Now().Add(12 * time.Second)
		if err := os.Chtimes(file, changedTime, changedTime); err != nil {
			t.Fatal(err)
		}
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.FilesHashed != 1 || result.LocationsAdded != 1 || result.LocationsUnavailable != 1 || result.TracksCreated != 0 || result.MediaObjectsCreated != 0 {
			t.Fatalf("same-byte native reuse: %#v %v", result, err)
		}
		locations := locationsForRoot(t, pool, root.ID)
		var oldUnavailable, newActive int
		for _, location := range locations {
			if location.ID == old.ID && location.Availability == "unavailable" && location.Reason != nil && *location.Reason == "replaced" {
				oldUnavailable++
			}
			if location.Path == "same-bytes.wav" && location.Availability == "available" && location.ID != old.ID && location.ObjectID == old.ObjectID && location.TrackID == old.TrackID {
				newActive++
			}
		}
		if oldUnavailable != 1 || newActive != 1 {
			t.Fatalf("same SHA incorrectly preserved native-reused location: %#v", locations)
		}
	})
	t.Run("existing_object_on_another_track", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		a := filepath.Join(dir, "a.wav")
		b := filepath.Join(dir, "b.wav")
		firstBytes, secondBytes := fixtureWAV(), changedWAV(2)
		writeFile(t, a, firstBytes)
		writeFile(t, b, secondBytes)
		root := addRoot(t, s, dir, "known object wins")
		scanner := testScanner(s)
		scanner.nativeIdentity = FakeNativeIdentityProvider(func(file *os.File) (storage.NativeIdentity, bool, error) {
			id := []byte{1}
			birth := []byte("birth-a")
			if filepath.Base(file.Name()) == "b.wav" {
				id = []byte{2}
				birth = []byte("birth-b")
			}
			return storage.NativeIdentity{Kind: "test", Scope: "volume", ID: id, BirthToken: birth}, true, nil
		})
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		before := locationsForRoot(t, pool, root.ID)
		var oldA, trackB, objectB string
		for _, loc := range before {
			if loc.Path == "a.wav" {
				oldA = loc.ID
			}
			if loc.Path == "b.wav" {
				trackB, objectB = loc.TrackID, loc.ObjectID
			}
		}
		if err := os.WriteFile(a, secondBytes, 0600); err != nil {
			t.Fatal(err)
		}
		changedTime := time.Now().Add(5 * time.Second)
		_ = os.Chtimes(a, changedTime, changedTime)
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.TracksCreated != 0 || result.MediaObjectsCreated != 0 || result.LocationsAdded != 1 || result.LocationsUnavailable != 1 || result.MetadataExtractions != 0 {
			t.Fatalf("existing object reconciliation: %#v %v", result, err)
		}
		after := locationsForRoot(t, pool, root.ID)
		var oldUnavailable, activeA int
		for _, loc := range after {
			if loc.ID == oldA && loc.Availability == "unavailable" {
				oldUnavailable++
			}
			if loc.Path == "a.wav" && loc.Availability == "available" && loc.ID != oldA && loc.TrackID == trackB && loc.ObjectID == objectB {
				activeA++
			}
		}
		if oldUnavailable != 1 || activeA != 1 || countRows(t, pool, "tracks") != 2 || countRows(t, pool, "media_objects") != 2 {
			t.Fatalf("existing object Track did not win: %#v", after)
		}
	})
	t.Run("birth_token_mismatch", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		file := filepath.Join(dir, "reused.wav")
		writeFile(t, file, fixtureWAV())
		root := addRoot(t, s, dir, "native ID reuse")
		birth := []byte("birth-one")
		scanner := testScanner(s)
		scanner.nativeIdentity = FakeNativeIdentityProvider(func(*os.File) (storage.NativeIdentity, bool, error) {
			return storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{9}, BirthToken: append([]byte(nil), birth...)}, true, nil
		})
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		old := locationsForRoot(t, pool, root.ID)[0]
		birth = []byte("birth-two")
		if err := os.WriteFile(file, changedWAV(3), 0600); err != nil {
			t.Fatal(err)
		}
		changedTime := time.Now().Add(6 * time.Second)
		_ = os.Chtimes(file, changedTime, changedTime)
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.TracksCreated != 1 || result.LocationsAdded != 1 || result.LocationsUnavailable != 1 {
			t.Fatalf("native ID reuse: %#v %v", result, err)
		}
		locations := locationsForRoot(t, pool, root.ID)
		var newID string
		var oldUnavailable bool
		for _, location := range locations {
			if location.ID == old.ID && location.Availability == "unavailable" {
				oldUnavailable = true
			}
			if location.Path == "reused.wav" && location.Availability == "available" {
				newID = location.ID
			}
		}
		if len(locations) != 2 || newID == "" || newID == old.ID || !oldUnavailable {
			t.Fatalf("reused ID transferred identity: %#v", locations)
		}
	})
}

func TestM13MetadataSourceAndAmbiguousNativeIDs(t *testing.T) {
	t.Run("hardlink_is_a_separate_location", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		oldPath := filepath.Join(dir, "original.wav")
		newPath := filepath.Join(dir, "hardlink.wav")
		writeFile(t, oldPath, fixtureWAV())
		root := addRoot(t, s, dir, "hard link")
		scanner := testScanner(s)
		scanner.nativeIdentity = systemNativeIdentityProvider()
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		old := locationsForRoot(t, pool, root.ID)[0]
		if err := os.Link(oldPath, newPath); err != nil {
			t.Skipf("hard links are unavailable on this filesystem: %v", err)
		}
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.LocationsMoved != 0 || result.LocationsAdded != 1 || result.LocationsUnavailable != 0 {
			t.Fatalf("hard link reconciliation: %#v %v", result, err)
		}
		locations := locationsForRoot(t, pool, root.ID)
		if len(locations) != 2 || locations[0].ID == locations[1].ID || locations[0].ObjectID != locations[1].ObjectID || locations[0].TrackID != locations[1].TrackID || locationsForPath(locations, "original.wav").ID != old.ID {
			t.Fatalf("hard link locations/identity: %#v", locations)
		}
	})
	t.Run("editing_non_source_duplicate_keeps_shared_metadata", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		writeFile(t, filepath.Join(dir, "a-source.mp3"), fixtureMP3WithTitle(t, "Source Title"))
		writeFile(t, filepath.Join(dir, "b-copy.mp3"), fixtureMP3WithTitle(t, "Source Title"))
		root := addRoot(t, s, dir, "metadata source")
		scanner := testScanner(s)
		scanner.nativeIdentity = FakeNativeIdentityProvider(func(file *os.File) (storage.NativeIdentity, bool, error) {
			id := byte(1)
			birth := "source"
			if filepath.Base(file.Name()) == "b-copy.mp3" {
				id = 2
				birth = "copy"
			}
			return storage.NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{id}, BirthToken: []byte(birth)}, true, nil
		})
		first, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || first.TracksCreated != 1 || first.MediaObjectsCreated != 1 || first.MetadataExtractions != 1 {
			t.Fatalf("initial duplicate import: %#v %v", first, err)
		}
		before := locationsForRoot(t, pool, root.ID)
		var trackID, sourceLocation, title string
		if err := pool.QueryRow(context.Background(), "SELECT id,title,metadata_source_location_id FROM tracks").Scan(&trackID, &title, &sourceLocation); err != nil {
			t.Fatal(err)
		}
		if title != "Source Title" || locationsForPath(before, "a-source.mp3").ID != sourceLocation {
			t.Fatalf("metadata source was not deterministic: title=%q source=%q locations=%#v", title, sourceLocation, before)
		}
		if err := os.WriteFile(filepath.Join(dir, "b-copy.mp3"), fixtureMP3WithTitle(t, "Edited Copy"), 0600); err != nil {
			t.Fatal(err)
		}
		changedTime := time.Now().Add(7 * time.Second)
		_ = os.Chtimes(filepath.Join(dir, "b-copy.mp3"), changedTime, changedTime)
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.TracksCreated != 0 || result.MediaObjectsCreated != 1 || result.MetadataExtractions != 1 {
			t.Fatalf("non-source edit: %#v %v", result, err)
		}
		if err := pool.QueryRow(context.Background(), "SELECT title,metadata_source_location_id FROM tracks WHERE id=$1", trackID).Scan(&title, &sourceLocation); err != nil || title != "Source Title" || sourceLocation != locationsForPath(before, "a-source.mp3").ID {
			t.Fatalf("non-source edit replaced shared metadata: %q %q %v", title, sourceLocation, err)
		}
		if len(locationsForRoot(t, pool, root.ID)) != 2 || countRows(t, pool, "tracks") != 1 || countRows(t, pool, "media_objects") != 2 {
			t.Fatal("non-source edit changed Track identity or location count")
		}
	})
	t.Run("ambiguous_native_id_does_not_infer_move", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		writeFile(t, filepath.Join(dir, "a.wav"), fixtureWAV())
		writeFile(t, filepath.Join(dir, "b.wav"), changedWAV(4))
		root := addRoot(t, s, dir, "ambiguous native")
		scanner := testScanner(s)
		scanner.nativeIdentity = testNativeProvider([]byte{5}, []byte("same-birth"))
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		before := locationsForRoot(t, pool, root.ID)
		oldB := locationsForPath(before, "b.wav")
		if err := os.Rename(filepath.Join(dir, "b.wav"), filepath.Join(dir, "c.wav")); err != nil {
			t.Fatal(err)
		}
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.LocationsMoved != 0 || result.LocationsAdded != 1 || result.LocationsUnavailable != 1 {
			t.Fatalf("ambiguous native inference: %#v %v", result, err)
		}
		after := locationsForRoot(t, pool, root.ID)
		if locationsForPath(after, "c.wav").ID == oldB.ID || locationsForPath(after, "c.wav").ObjectID != oldB.ObjectID || locationsForPath(after, "b.wav").Availability != "unavailable" {
			t.Fatalf("ambiguous ID transferred location: %#v", after)
		}
	})
}

func TestM13UnreadableAndTransientObservationsAreConservative(t *testing.T) {
	t.Run("known_unreadable_and_reappearance", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		file := filepath.Join(dir, "known.wav")
		writeFile(t, file, fixtureWAV())
		root := addRoot(t, s, dir, "unreadable")
		scanner := testScanner(s)
		scanner.nativeIdentity = noNativeIdentityProvider{}
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		old := locationsForRoot(t, pool, root.ID)[0]
		changedTime := time.Now().Add(8 * time.Second)
		if err := os.Chtimes(file, changedTime, changedTime); err != nil {
			t.Fatal(err)
		}
		scanner.openFile = func(r *os.Root, name string) (*os.File, error) {
			if name == "known.wav" {
				return nil, os.ErrPermission
			}
			return r.Open(name)
		}
		partial, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || partial.Status != "partial" || partial.TraversalComplete || !partial.ObservationsApplied || partial.AbsenceReconciled || partial.LocationsUnavailable != 1 {
			t.Fatalf("unreadable known location: %#v %v", partial, err)
		}
		unavailable := locationsForRoot(t, pool, root.ID)[0]
		if unavailable.ID != old.ID || unavailable.Availability != "unavailable" || unavailable.Reason == nil || *unavailable.Reason != "unreadable" {
			t.Fatalf("unreadable reason/identity: %#v", unavailable)
		}
		scanner.openFile = nil
		reappeared, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || reappeared.LocationsAdded != 1 || reappeared.TracksCreated != 0 || reappeared.MediaObjectsCreated != 0 {
			t.Fatalf("unreadable bytes reappearing: %#v %v", reappeared, err)
		}
		locations := locationsForRoot(t, pool, root.ID)
		var newAvailable bool
		for _, location := range locations {
			if location.Path == "known.wav" && location.ID != old.ID && location.Availability == "available" {
				newAvailable = true
			}
		}
		if len(locations) != 2 || !newAvailable {
			t.Fatalf("reappearance reused an unavailable occurrence: %#v", locations)
		}
	})
	t.Run("disappears_before_open", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		file := filepath.Join(dir, "vanish.wav")
		writeFile(t, file, fixtureWAV())
		root := addRoot(t, s, dir, "transient disappearance")
		scanner := testScanner(s)
		scanner.nativeIdentity = noNativeIdentityProvider{}
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		old := locationsForRoot(t, pool, root.ID)[0]
		changedTime := time.Now().Add(10 * time.Second)
		if err := os.Chtimes(file, changedTime, changedTime); err != nil {
			t.Fatal(err)
		}
		scanner.openFile = func(*os.Root, string) (*os.File, error) {
			_ = os.Remove(file)
			return nil, os.ErrNotExist
		}
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.Status != "partial" || result.TraversalComplete || result.AbsenceReconciled {
			t.Fatalf("transient disappearance: %#v %v", result, err)
		}
		locations := locationsForRoot(t, pool, root.ID)
		if len(locations) != 1 || locations[0].ID != old.ID || locations[0].Availability != "available" {
			t.Fatalf("transient disappearance was treated as removal: %#v", locations)
		}
	})
	t.Run("mutation_after_enumeration", func(t *testing.T) {
		s, pool, _ := isolatedLibraryStore(t)
		dir := testWorkspaceDir(t)
		file := filepath.Join(dir, "mutated.wav")
		writeFile(t, file, fixtureWAV())
		root := addRoot(t, s, dir, "file mutation")
		scanner := testScanner(s)
		scanner.nativeIdentity = noNativeIdentityProvider{}
		if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
			t.Fatal(err)
		}
		old := locationsForRoot(t, pool, root.ID)[0]
		changedTime := time.Now().Add(11 * time.Second)
		if err := os.Chtimes(file, changedTime, changedTime); err != nil {
			t.Fatal(err)
		}
		scanner.openFile = func(r *os.Root, name string) (*os.File, error) {
			if name == "mutated.wav" {
				_ = os.WriteFile(file, changedWAV(5), 0600)
			}
			return r.Open(name)
		}
		result, err := scanner.Scan(context.Background(), root.ID)
		if err != nil || result.Status != "partial" || result.TraversalComplete || result.AbsenceReconciled || result.LocationsAdded != 0 {
			t.Fatalf("mutation candidate: %#v %v", result, err)
		}
		locations := locationsForRoot(t, pool, root.ID)
		if len(locations) != 1 || locations[0].ID != old.ID || locations[0].Availability != "available" {
			t.Fatalf("unstable file changed catalog identity: %#v", locations)
		}
	})
}

func TestM13PathReplacedAfterOpenDoesNotPublishOldHandle(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	file := filepath.Join(dir, "race.wav")
	writeFile(t, file, fixtureWAV())
	root := addRoot(t, s, dir, "path replacement during hash")
	scanner := testScanner(s)
	scanner.nativeIdentity = noNativeIdentityProvider{}
	scanner.openFile = func(r *os.Root, name string) (*os.File, error) {
		opened, err := r.Open(name)
		if err != nil {
			return nil, err
		}
		if err := os.Rename(file, filepath.Join(dir, "detached.tmp")); err != nil {
			opened.Close()
			t.Skipf("filesystem does not permit renaming an open file: %v", err)
		}
		writeFile(t, file, changedWAV(91))
		return opened, nil
	}
	result, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || result.Status != "partial" || result.TraversalComplete || result.AbsenceReconciled || result.LocationsAdded != 0 {
		t.Fatalf("path replacement was published from stale handle: %#v %v", result, err)
	}
	if countRows(t, pool, "tracks") != 0 || countRows(t, pool, "media_objects") != 0 || countRows(t, pool, "media_locations") != 0 {
		t.Fatal("stale handle created catalog identities")
	}
}

func TestM13MalformedReplacementAndRootOfflinePreserveIdentity(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	file := filepath.Join(dir, "bad-later.wav")
	writeFile(t, file, fixtureWAV())
	root := addRoot(t, s, dir, "invalid content")
	scanner := testScanner(s)
	scanner.nativeIdentity = noNativeIdentityProvider{}
	if _, err := scanner.Scan(context.Background(), root.ID); err != nil {
		t.Fatal(err)
	}
	old := locationsForRoot(t, pool, root.ID)[0]
	if err := os.WriteFile(file, []byte("not a wav"), 0600); err != nil {
		t.Fatal(err)
	}
	changedTime := time.Now().Add(9 * time.Second)
	_ = os.Chtimes(file, changedTime, changedTime)
	invalid, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || invalid.Status != "partial" || !invalid.TraversalComplete || !invalid.AbsenceReconciled || invalid.LocationsUnavailable != 1 || invalid.TracksCreated != 0 || invalid.MediaObjectsCreated != 0 {
		t.Fatalf("malformed replacement: %#v %v", invalid, err)
	}
	locations := locationsForRoot(t, pool, root.ID)
	if len(locations) != 1 || locations[0].ID != old.ID || locations[0].Availability != "unavailable" || locations[0].Reason == nil || *locations[0].Reason != "content_invalid" {
		t.Fatalf("malformed replacement identity: %#v", locations)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	offline, err := scanner.Scan(context.Background(), root.ID)
	if err == nil || offline.Status != "failed" || offline.ErrorCode != "root_unavailable" {
		t.Fatalf("offline root: %#v %v", offline, err)
	}
	after := locationsForRoot(t, pool, root.ID)
	if len(after) != 1 || after[0].ID != old.ID || after[0].Availability != "unavailable" || after[0].Reason == nil || *after[0].Reason != "content_invalid" {
		t.Fatalf("offline root changed availability: %#v", after)
	}
}

func TestM13FinalPublishRollbackIsAtomic(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	writeFile(t, filepath.Join(dir, "rollback.wav"), fixtureWAV())
	root := addRoot(t, s, dir, "atomic publish")
	if _, err := pool.Exec(context.Background(), `CREATE FUNCTION fail_location_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected publish failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `CREATE TRIGGER fail_location_insert BEFORE INSERT ON media_locations FOR EACH ROW EXECUTE FUNCTION fail_location_insert()`); err != nil {
		t.Fatal(err)
	}
	scanner := testScanner(s)
	scanner.nativeIdentity = noNativeIdentityProvider{}
	failed, err := scanner.Scan(context.Background(), root.ID)
	if err == nil || failed.Status != "failed" || failed.ObservationsApplied || failed.AbsenceReconciled {
		t.Fatalf("final publish failure: %#v %v", failed, err)
	}
	if countRows(t, pool, "tracks") != 0 || countRows(t, pool, "media_objects") != 0 || countRows(t, pool, "media_locations") != 0 {
		t.Fatal("failed final transaction left catalog identities behind")
	}
	var status, phase string
	var applied, absent bool
	if err := pool.QueryRow(context.Background(), "SELECT status,phase,observations_applied,absence_reconciled FROM scan_runs WHERE id=$1", failed.RunID).Scan(&status, &phase, &applied, &absent); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || phase != "finished" || applied || absent {
		t.Fatalf("failed run flags: %s %s %v %v", status, phase, applied, absent)
	}
	if _, err := pool.Exec(context.Background(), "DROP TRIGGER fail_location_insert ON media_locations"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "DROP FUNCTION fail_location_insert()"); err != nil {
		t.Fatal(err)
	}
	retry, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || retry.Status != "succeeded" || countRows(t, pool, "tracks") != 1 || countRows(t, pool, "media_objects") != 1 || countRows(t, pool, "media_locations") != 1 {
		t.Fatalf("retry after rolled back publish: %#v %v", retry, err)
	}
}

func TestM13LargeIncompleteDeletionSuppressesAbsence(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	mediaDir := filepath.Join(dir, "album")
	for i := 0; i < 1000; i++ {
		writeFile(t, filepath.Join(mediaDir, fmt.Sprintf("track-%04d.wav", i)), fixtureWAV())
	}
	root := addRoot(t, s, dir, "large destructive negative")
	scanner := testScanner(s)
	scanner.nativeIdentity = noNativeIdentityProvider{}
	initial, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || initial.Status != "succeeded" || initial.LocationsAdded != 1000 || countRows(t, pool, "media_locations") != 1000 {
		t.Fatalf("initial large import: %#v %v", initial, err)
	}
	initialLocations := locationsForRoot(t, pool, root.ID)
	for i := 0; i < 900; i++ {
		if err := os.Remove(filepath.Join(mediaDir, fmt.Sprintf("track-%04d.wav", i))); err != nil {
			t.Fatal(err)
		}
	}
	scanner.openDir = func(r *os.Root, name string) (*os.File, error) {
		if name == "album" {
			return nil, os.ErrPermission
		}
		return r.Open(name)
	}
	partial, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || partial.Status != "partial" || partial.TraversalComplete || !partial.ObservationsApplied || partial.AbsenceReconciled {
		t.Fatalf("incomplete destructive scan: %#v %v", partial, err)
	}
	var available, unavailable int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FILTER (WHERE availability='available'),count(*) FILTER (WHERE availability='unavailable') FROM media_locations WHERE root_id=$1", root.ID).Scan(&available, &unavailable); err != nil {
		t.Fatal(err)
	}
	if available != 1000 || unavailable != 0 {
		t.Fatalf("incomplete scan mass-marked locations: available=%d unavailable=%d", available, unavailable)
	}
	afterPartial := locationsForRoot(t, pool, root.ID)
	assertLocationIDsStable(t, initialLocations, afterPartial)
	for _, location := range afterPartial {
		if location.Availability != "available" {
			t.Fatalf("incomplete scan changed availability at %q: %#v", location.Path, location)
		}
	}
	scanner.openDir = nil
	complete, err := scanner.Scan(context.Background(), root.ID)
	if err != nil || complete.Status != "succeeded" || !complete.TraversalComplete || !complete.AbsenceReconciled || complete.LocationsUnavailable != 900 {
		t.Fatalf("complete follow-up scan: %#v %v", complete, err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FILTER (WHERE availability='available'),count(*) FILTER (WHERE availability='unavailable') FROM media_locations WHERE root_id=$1", root.ID).Scan(&available, &unavailable); err != nil {
		t.Fatal(err)
	}
	if available != 100 || unavailable != 900 {
		t.Fatalf("complete absence reconciliation: available=%d unavailable=%d", available, unavailable)
	}
	afterComplete := locationsForRoot(t, pool, root.ID)
	assertLocationIDsStable(t, initialLocations, afterComplete)
}

func changedWAV(sample byte) []byte {
	data := fixtureWAV()
	data[len(data)-1] = sample
	return data
}

func locationsForPath(locations []testLocation, path string) testLocation {
	for _, location := range locations {
		if location.Path == path {
			return location
		}
	}
	return testLocation{}
}
