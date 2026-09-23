//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"resonance/internal/library"
	"resonance/internal/storage"
)

type onFirstWrite struct {
	*httptest.ResponseRecorder
	action func()
	called bool
}

func (w *onFirstWrite) Write(p []byte) (int, error) {
	if !w.called {
		w.called = true
		w.action()
	}
	return w.ResponseRecorder.Write(p)
}

func catalogTestStore(t *testing.T) (*storage.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("RESONANCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("RESONANCE_TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	var random [8]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	schema := "catalogtest_" + hex.EncodeToString(random[:])
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	scoped := dsn + " search_path=" + schema
	store, e := storage.Open(ctx, scoped)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	pool, e := pgxpool.New(ctx, scoped)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		store.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	return store, pool
}

func testWAV() []byte {
	b := make([]byte, 44+4096)
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
	binary.LittleEndian.PutUint32(b[40:], 4096)
	return b
}

func addCatalogFixture(t *testing.T, pool *pgxpool.Pool, rootID, rootPath, name, format string, data []byte, index int) (string, string) {
	t.Helper()
	ctx := context.Background()
	relative := filepath.Join("Artist", "Album", name+"."+format)
	full := filepath.Join(rootPath, relative)
	if e := os.MkdirAll(filepath.Dir(full), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, data, 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(full)
	if e != nil {
		t.Fatal(e)
	}
	trackID := fmt.Sprintf("trk_%032x", index)
	objID := fmt.Sprintf("obj_%032x", index)
	locID := fmt.Sprintf("loc_%032x", index)
	hash := sha256.Sum256(data)
	if _, e = pool.Exec(ctx, "INSERT INTO tracks(id,title,artist_credit,album_title,album_artist_credit,track_number) VALUES($1,$2,'Artist','Album','Artist',$3)", trackID, name, index); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, "INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES($1,$2,$3,$4,$5)", objID, trackID, hash[:], format, len(data)); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size,observed_mtime_ns) VALUES($1,$2,$3,$4,$5,$6,$7)`, locID, objID, full, rootID, relative, len(data), info.ModTime().UnixNano()); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, "UPDATE tracks SET metadata_source_location_id=$2 WHERE id=$1", trackID, locID); e != nil {
		t.Fatal(e)
	}
	return trackID, locID
}

func TestM14TracksPageRepeatedAt304Tracks(t *testing.T) {
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	rootPath := catalogWorkspaceTempDir(t)
	canonicalRoot, err := library.CanonicalizeRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.AddRoot(ctx, "304-track review", canonicalRoot)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 304; i++ {
		data := testWAV()
		binary.LittleEndian.PutUint32(data[44:], uint32(i))
		addCatalogFixture(t, pool, root.ID, rootPath, fmt.Sprintf("Song %03d", i), "wav", data, i)
	}
	if err := store.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	h := newHandlerWithCatalog(filepath.Join(rootPath, "Artist", "Album", "Song 001.wav"), "Demo", io.Discard, store.Ready, store)
	server := httptest.NewServer(h)
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	var maximum time.Duration
	for i := 0; i < 100; i++ {
		started := time.Now()
		response, err := client.Get(server.URL + "/api/v1/tracks?limit=50")
		if err != nil {
			t.Fatalf("304-track Tracks page request %d: %v", i, err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("304-track Tracks page request %d: status=%d error=%v", i, response.StatusCode, readErr)
		}
		if elapsed := time.Since(started); elapsed > maximum {
			maximum = elapsed
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		url := server.URL + "/api/v1/tracks?limit=50"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		response, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Items []struct {
				ID        string `json:"id"`
				Available bool   `json:"available"`
			} `json:"items"`
			Next *string `json:"next_cursor"`
		}
		err = json.NewDecoder(response.Body).Decode(&page)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("page %d: status=%d error=%v", pageNumber, response.StatusCode, err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate Track across pages: %s", item.ID)
			}
			seen[item.ID] = true
			if item.ID == "trk_00000000000000000000000000000096" && item.Available {
				t.Fatal("page after availability change returned stale status")
			}
		}
		if pageNumber == 0 {
			if page.Next == nil {
				t.Fatal("first page has no cursor")
			}
			invalid, err := client.Get(server.URL + "/api/v1/artists?cursor=" + *page.Next)
			if err != nil {
				t.Fatal(err)
			}
			invalid.Body.Close()
			if invalid.StatusCode != http.StatusBadRequest {
				t.Fatal("Track cursor was accepted on Artist route")
			}
			if _, err := pool.Exec(ctx, `UPDATE media_locations SET availability='unavailable',unavailable_reason='not_found',unavailable_at=now() WHERE id='loc_00000000000000000000000000000096'`); err != nil {
				t.Fatal(err)
			}
		}
		if page.Next == nil {
			break
		}
		cursor = *page.Next
	}
	if len(seen) != 304 {
		t.Fatalf("keyset pagination after availability change returned %d of 304 Tracks", len(seen))
	}
	t.Logf("100 successful 304-track Tracks pages; maximum request %s", maximum)
}

func TestCatalogHTTPAndRealRanges(t *testing.T) {
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	rootPath := catalogWorkspaceTempDir(t)
	canonicalRoot, e := library.CanonicalizeRoot(rootPath)
	if e != nil {
		t.Fatal(e)
	}
	root, e := store.AddRoot(ctx, "Music", canonicalRoot)
	if e != nil {
		t.Fatal(e)
	}
	formats := []struct {
		name, format, path string
		data               []byte
	}{
		{"WAV Song", "wav", "", testWAV()},
		{"MP3 Song", "mp3", "testdata/metadata/untagged.mp3", nil},
		{"FLAC Song", "flac", "testdata/metadata/untagged.flac", nil},
	}
	trackIDs := []string{}
	locIDs := []string{}
	for i := range formats {
		f := &formats[i]
		if f.path != "" {
			f.data, e = os.ReadFile(f.path)
			if e != nil {
				t.Fatal(e)
			}
		}
		id, loc := addCatalogFixture(t, pool, root.ID, rootPath, f.name, f.format, f.data, i+1)
		trackIDs = append(trackIDs, id)
		locIDs = append(locIDs, loc)
	}
	if e = store.BackfillGrouping(ctx); e != nil {
		t.Fatal(e)
	}
	var logs bytes.Buffer
	h := newHandlerWithCatalog(filepath.Join(rootPath, "Artist", "Album", "WAV Song.wav"), "Demo", &logs, store.Ready, store)
	legacyID := "trk_ffffffffffffffffffffffffffffffff"
	legacyHash := sha256.Sum256([]byte("rootless legacy"))
	if _, e = pool.Exec(ctx, "INSERT INTO tracks(id,title) VALUES($1,'Private Legacy')", legacyID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, "INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES('obj_ffffffffffffffffffffffffffffffff',$1,$2,'mp3',10)", legacyID, legacyHash[:]); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, "INSERT INTO media_locations(id,media_object_id,local_path) VALUES('loc_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','obj_ffffffffffffffffffffffffffffffff',$1)", rootPath); e != nil {
		t.Fatal(e)
	}
	for _, route := range []string{"/api/v1/tracks?limit=0", "/api/v1/tracks?limit=201", "/api/v1/tracks?cursor=bad", "/api/v1/tracks/not-a-track"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		if w.Code < 400 || strings.Contains(w.Body.String(), rootPath) {
			t.Fatalf("unsafe validation for %s: %d", route, w.Code)
		}
	}
	for _, route := range []string{"/api/v1/tracks/" + legacyID, "/api/v1/tracks/" + legacyID + "/stream"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		if w.Code != 404 {
			t.Fatalf("rootless record became public: %s %d", route, w.Code)
		}
	}
	for _, route := range []string{"/api/v1/tracks", "/api/v1/artists", "/api/v1/albums", "/api/v1/tracks/" + trackIDs[0], "/api/v1/artists/", "/api/v1/albums/"} {
		if strings.HasSuffix(route, "/artists/") || strings.HasSuffix(route, "/albums/") {
			continue
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		if w.Code != 200 {
			t.Fatalf("%s status=%d body=%s", route, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), rootPath) || strings.Contains(w.Body.String(), "Artist/Album/") || strings.Contains(w.Body.String(), "Artist\\Album") || strings.Contains(w.Body.String(), "Private Legacy") {
			t.Fatalf("path disclosed by %s", route)
		}
	}
	var artistID, albumID string
	if e = pool.QueryRow(ctx, "SELECT artist_id FROM track_artist_memberships WHERE track_id=$1 AND role='track_credit'", trackIDs[0]).Scan(&artistID); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", trackIDs[0]).Scan(&albumID); e != nil {
		t.Fatal(e)
	}
	ordered := httptest.NewRecorder()
	h.ServeHTTP(ordered, httptest.NewRequest("GET", "/api/v1/albums/"+albumID+"/tracks", nil))
	var albumPage struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if ordered.Code != 200 || json.Unmarshal(ordered.Body.Bytes(), &albumPage) != nil || len(albumPage.Items) != 3 {
		t.Fatal("album contents missing")
	}
	for i, item := range albumPage.Items {
		if item.ID != trackIDs[i] {
			t.Fatalf("album disc/track ordering failed: %v", albumPage.Items)
		}
	}
	for _, route := range []string{"/api/v1/artists/" + artistID, "/api/v1/albums/" + albumID, "/api/v1/artists/" + artistID + "/albums", "/api/v1/artists/" + artistID + "/tracks", "/api/v1/albums/" + albumID + "/tracks", "/api/v1/tracks?limit=1"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		if w.Code != 200 {
			t.Fatalf("%s status=%d body=%s", route, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), rootPath) {
			t.Fatal("host path disclosure")
		}
		if strings.Contains(route, "limit=1") {
			var page struct {
				Next *string `json:"next_cursor"`
			}
			if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Next == nil {
				t.Fatal("pagination cursor missing")
			}
			w2 := httptest.NewRecorder()
			h.ServeHTTP(w2, httptest.NewRequest("GET", "/api/v1/tracks?limit=1&cursor="+*page.Next, nil))
			if w2.Code != 200 {
				t.Fatal("next page failed")
			}
		}
	}
	for i, f := range formats {
		url := "/api/v1/tracks/" + trackIDs[i] + "/stream"
		for _, tc := range []struct {
			method, rangeValue string
			status             int
			length             int
		}{{"GET", "", 200, len(f.data)}, {"HEAD", "", 200, 0}, {"GET", "bytes=2-9", 206, 8}, {"GET", "bytes=10-", 206, len(f.data) - 10}, {"GET", "bytes=-7", 206, 7}, {"GET", "bytes=999999999-", 416, 0}} {
			r := httptest.NewRequest(tc.method, url, nil)
			if tc.rangeValue != "" {
				r.Header.Set("Range", tc.rangeValue)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%s %s %s status=%d", f.format, tc.method, tc.rangeValue, w.Code)
			}
			if tc.status != 416 && w.Body.Len() != tc.length {
				t.Fatalf("%s length=%d want=%d", f.format, w.Body.Len(), tc.length)
			}
			if tc.status == 206 && !strings.HasPrefix(w.Header().Get("Content-Range"), "bytes ") {
				t.Fatal("missing Content-Range")
			}
			if tc.status == 206 && (w.Header().Get("Content-Length") != fmt.Sprint(tc.length) || w.Header().Get("X-Request-ID") == "") {
				t.Fatal("catalog Range length or request ID missing")
			}
		}
		r := httptest.NewRequest("GET", url, nil)
		r.Header.Set("Range", "bytes=2-9")
		r.Header.Set("If-Range", "stale")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.Len() != len(f.data) {
			t.Fatalf("%s If-Range was not ignored", f.format)
		}
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelW := &onFirstWrite{ResponseRecorder: httptest.NewRecorder(), action: cancel}
	h.ServeHTTP(cancelW, httptest.NewRequest("GET", "/api/v1/tracks/"+trackIDs[1]+"/stream", nil).WithContext(cancelCtx))
	if cancelW.Body.Len() == 0 || cancelW.Body.Len() >= len(formats[1].data) || !strings.Contains(logs.String(), `"source":"catalog"`) || !strings.Contains(logs.String(), `"error":"client_canceled"`) {
		t.Fatal("catalog cancellation or telemetry failed")
	}
	streamW := &onFirstWrite{ResponseRecorder: httptest.NewRecorder(), action: func() {
		if _, err := pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='not_found',unavailable_at=now() WHERE id=$1", locIDs[1]); err != nil {
			t.Error(err)
		}
	}}
	h.ServeHTTP(streamW, httptest.NewRequest("GET", "/api/v1/tracks/"+trackIDs[1]+"/stream", nil))
	if streamW.Code != 200 || !bytes.Equal(streamW.Body.Bytes(), formats[1].data) {
		t.Fatal("stream changed after reconciliation")
	}
	if _, e = pool.Exec(ctx, "UPDATE media_locations SET availability='available',unavailable_reason=NULL,unavailable_at=NULL WHERE id=$1", locIDs[1]); e != nil {
		t.Fatal(e)
	}
	// The first source may fail; a second current copy is tried before headers.
	second := filepath.Join(rootPath, "Artist", "Album", "copy.wav")
	if e = os.WriteFile(second, formats[0].data, 0600); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(second)
	if _, e = pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size,observed_mtime_ns) VALUES('loc_ffffffffffffffffffffffffffffffff',$1,$2,$3,$4,$5,$6)`, `obj_00000000000000000000000000000001`, second, root.ID, filepath.Join("Artist", "Album", "copy.wav"), len(formats[0].data), info.ModTime().UnixNano()); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(rootPath, "Artist", "Album", "WAV Song.wav")); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/tracks/"+trackIDs[0]+"/stream", nil))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), formats[0].data) {
		t.Fatal("alternate copy not served")
	}
	if _, e = pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='not_found',unavailable_at=now() WHERE media_object_id=$1", "obj_00000000000000000000000000000001"); e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/tracks/"+trackIDs[0]+"/stream", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "track_unavailable") {
		t.Fatal("unavailable failure missing")
	}
	if !strings.Contains(logs.String(), `"error":"track_unavailable"`) || !strings.Contains(logs.String(), `"status":503`) {
		t.Fatal("unavailable indexed stream was not logged with a safe failure code")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/tracks/"+trackIDs[0], nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatal("unavailable Track detail was not retained")
	}
	if _, e = pool.Exec(ctx, "UPDATE media_locations SET availability='available',unavailable_reason=NULL,unavailable_at=NULL WHERE id='loc_ffffffffffffffffffffffffffffffff'"); e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/tracks/"+trackIDs[0], nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatal("reappearing Track did not become available")
	}
	truncPath := filepath.Join(rootPath, "Artist", "Album", "MP3 Song.mp3")
	truncW := &truncateWriter{ResponseRecorder: httptest.NewRecorder(), path: truncPath, t: t}
	func() {
		defer func() {
			if got := recover(); got != http.ErrAbortHandler {
				t.Errorf("missing transfer abort: %v", got)
			}
		}()
		h.ServeHTTP(truncW, httptest.NewRequest("GET", "/api/v1/tracks/"+trackIDs[1]+"/stream", nil))
	}()
	if truncW.Body.Len() >= len(formats[1].data) || strings.Contains(truncW.Body.String(), "track_unavailable") {
		t.Fatal("truncated stream appended an error or claimed full content")
	}
	badRange := httptest.NewRequest("GET", "/api/v1/tracks/"+trackIDs[2]+"/stream", nil)
	badRange.Header.Set("Range", rootPath)
	h.ServeHTTP(httptest.NewRecorder(), badRange)
	if strings.Contains(logs.String(), rootPath) || strings.Contains(logs.String(), "Artist/Album/") {
		t.Fatal("catalog log disclosed a host path")
	}
}
