//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resonance/internal/library"
	"resonance/internal/storage"
)

func TestM2SearchRealCatalog(t *testing.T) {
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	dir := catalogWorkspaceTempDir(t)
	root, err := store.AddRoot(ctx, "search", dir)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := addCatalogFixture(t, pool, root.ID, dir, "Quiet 100%_雪", "wav", testWAV(), 1)
	data := testWAV()
	data[45] = 1
	two, _ := addCatalogFixture(t, pool, root.ID, dir, "Quiet second", "wav", data, 2)
	if err = store.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	h := newHandlerWithCatalog("missing.wav", "demo", io.Discard, store.Ready, store)
	for _, tc := range []struct {
		query                   string
		tracks, artists, albums int
	}{{"quiet", 2, 0, 0}, {"ARTIST", 2, 1, 0}, {"Album", 2, 0, 1}, {"100%_", 1, 0, 0}, {"雪", 1, 0, 0}, {"%", 1, 0, 0}, {"no match", 0, 0, 0}, {"", 0, 0, 0}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/search?q="+url.QueryEscape(tc.query), nil))
		if w.Code != 200 {
			t.Fatalf("%q %d %s", tc.query, w.Code, w.Body.String())
		}
		var result storage.SearchResults
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("JSON")
		}
		if len(result.Tracks) != tc.tracks || len(result.Artists) != tc.artists || len(result.Albums) != tc.albums {
			t.Fatalf("%q sizes %d/%d/%d", tc.query, len(result.Tracks), len(result.Artists), len(result.Albums))
		}
		if strings.Contains(w.Body.String(), dir) || strings.Contains(w.Body.String(), "relative_path") {
			t.Fatal("path leak")
		}
	}
	first, err := store.SearchCatalog(ctx, "quiet", 1)
	if err != nil || len(first.Tracks) != 1 || first.Tracks[0].ID != one {
		t.Fatalf("bounded order %v %v", first, err)
	}
	grouped, err := store.SearchCatalog(ctx, "Artist", 20)
	if err != nil || len(grouped.Artists) != 1 || grouped.Artists[0].TrackCount != 2 || grouped.Artists[0].ArtworkURL != nil {
		t.Fatalf("artist presentation must count distinct Tracks and avoid invented artwork: %#v %v", grouped.Artists, err)
	}
	albums, err := store.SearchCatalog(ctx, "Album", 20)
	if err != nil || len(albums.Albums) != 1 || albums.Albums[0].TrackCount != 2 || albums.Albums[0].ArtistCredit == nil {
		t.Fatalf("album presentation must retain credit and count: %#v %v", albums.Albums, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='missing',unavailable_at=now() WHERE media_object_id IN (SELECT id FROM media_objects WHERE track_id=$1)", two); err != nil {
		t.Fatal(err)
	}
	result, err := store.SearchCatalog(ctx, "second", 20)
	if err != nil || len(result.Tracks) != 1 || result.Tracks[0].Available {
		t.Fatal("unavailable Track disappeared")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = store.SearchCatalog(canceled, "quiet", 20); err == nil {
		t.Fatal("canceled search succeeded")
	}
}

func TestM2HostRootOperations(t *testing.T) {
	store, _ := catalogTestStore(t)
	dir := catalogWorkspaceTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "song.wav"), testWAV(), 0600); err != nil {
		t.Fatal(err)
	}
	h := newAdminHandler(store, store.Ready, nil, io.Discard)
	call := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := localAdminRequest("POST", path, string(data))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call("/api/v1/admin/roots", map[string]string{"name": "Host test", "path": dir})
	if w.Code != 201 {
		t.Fatalf("add %d %s", w.Code, w.Body.String())
	}
	var root adminRoot
	_ = json.Unmarshal(w.Body.Bytes(), &root)
	if root.Path == "" {
		t.Fatal("host path omitted")
	}
	prefix := "/api/v1/admin/roots/" + root.ID + "/"
	if w = call("/api/v1/admin/roots", map[string]string{"name": "Duplicate", "path": dir}); w.Code != 409 {
		t.Fatalf("overlap %d", w.Code)
	}
	if w = call(prefix+"verify", map[string]bool{"confirm": true}); w.Code != 200 {
		t.Fatalf("verify %d %s", w.Code, w.Body.String())
	}
	if w = call(prefix+"disable", map[string]bool{}); w.Code != 200 {
		t.Fatal("disable")
	}
	if w = call(prefix+"scan", map[string]bool{}); w.Code != 409 {
		t.Fatalf("disabled scan %d", w.Code)
	}
	if w = call(prefix+"enable", map[string]bool{}); w.Code != 200 {
		t.Fatal("enable")
	}
	if w = call(prefix+"scan", map[string]bool{}); w.Code != 200 {
		t.Fatalf("scan %d %s", w.Code, w.Body.String())
	}
	bound, err := store.GetRoot(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.QuarantineRootIfIdentity(context.Background(), root.ID, *bound.Identity); err != nil {
		t.Fatal(err)
	}
	if w = call(prefix+"scan", map[string]bool{}); w.Code != 409 {
		t.Fatal("quarantine bypass")
	}
	if w = call(prefix+"verify", map[string]bool{}); w.Code != 400 {
		t.Fatal("rebind without explicit confirmation")
	}
	if _, err = library.CanonicalizeRoot(dir); err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(newHandlerWithCatalog("missing.wav", "demo", io.Discard, store.Ready, store))
	defer public.Close()
	response, err := http.Get(public.URL + "/api/v1/tracks")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if bytes.Contains(body, []byte(dir)) {
		t.Fatal("listener path leak")
	}
	if _, err = os.Stat(filepath.Join(dir, "song.wav")); err != nil {
		t.Fatal("media removed")
	}
}
