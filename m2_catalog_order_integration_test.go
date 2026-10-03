//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"testing"

	"resonance/internal/storage"
)

func TestM2CatalogOrderIsBoundedAndCursorScoped(t *testing.T) {
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	dir := catalogWorkspaceTempDir(t)
	root, err := store.AddRoot(ctx, "ordered", dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, title := range []string{"Alpha", "Beta", "Gamma"} {
		data := testWAV()
		data[45] = byte(i + 1)
		addCatalogFixture(t, pool, root.ID, dir, title, "wav", data, i+701)
	}
	if err = store.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	h := newHandlerWithCatalog("missing.wav", "demo", io.Discard, store.Ready, store)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	var first struct {
		Items []storage.CatalogTrack `json:"items"`
		Next  *string                `json:"next_cursor"`
	}
	w := get("/api/v1/tracks?order=title_desc&limit=2")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || len(first.Items) != 2 || first.Next == nil {
		t.Fatalf("first %d %s", w.Code, w.Body.String())
	}
	if *first.Items[0].Title != "Gamma" || *first.Items[1].Title != "Beta" {
		t.Fatalf("wrong order %#v", first.Items)
	}
	cursor := url.QueryEscape(*first.Next)
	w = get("/api/v1/tracks?order=title_desc&limit=2&cursor=" + cursor)
	var last struct {
		Items []storage.CatalogTrack `json:"items"`
		Next  *string                `json:"next_cursor"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &last) != nil || len(last.Items) != 1 || *last.Items[0].Title != "Alpha" || last.Next != nil {
		t.Fatalf("last %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/tracks?cursor=" + cursor, "/api/v1/tracks?order=bogus", "/api/v1/artists?order=bogus", "/api/v1/albums?order=bogus"} {
		if w = get(path); w.Code != 400 {
			t.Fatalf("accepted order/cursor mismatch %s %d", path, w.Code)
		}
	}
	for _, kind := range []string{"artists", "albums"} {
		w = get("/api/v1/" + kind + "?order=title_desc&limit=1")
		if w.Code != 200 {
			t.Fatalf("%s order failed %d %s", kind, w.Code, w.Body.String())
		}
	}
}
