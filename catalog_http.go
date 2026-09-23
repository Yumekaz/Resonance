package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"resonance/internal/storage"
)

type pageCursor struct {
	Kind   string `json:"k"`
	Filter string `json:"f"`
	Key    string `json:"s"`
	ID     string `json:"i"`
}
type pageInfo struct {
	Limit  int
	Key    string
	ID     string
	Filter string
	Kind   string
}

func catalogError(w http.ResponseWriter, status int, code string) {
	jsonResponseStatus(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}

func parsePage(r *http.Request, kind, filter string) (pageInfo, error) {
	p := pageInfo{Limit: 50, Filter: filter, Kind: kind}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 200 {
			return p, errors.New("invalid limit")
		}
		p.Limit = n
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if len(raw) > 4096 {
			return p, errors.New("invalid cursor")
		}
		body, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil {
			return p, e
		}
		var c pageCursor
		if json.Unmarshal(body, &c) != nil || c.Kind != kind || c.Filter != filter || c.ID == "" || len(c.Key) > 2048 || !utf8.ValidString(c.Key) {
			return p, errors.New("invalid cursor")
		}
		idKind := strings.TrimSuffix(kind, "s")
		if kind == "artist_albums" {
			idKind = "album"
		}
		if kind == "artist_tracks" || kind == "album_tracks" {
			idKind = "track"
		}
		if !storage.ValidCatalogID(c.ID, idKind) {
			return p, errors.New("invalid cursor")
		}
		p.Key, p.ID = c.Key, c.ID
	}
	return p, nil
}

func nextPage(kind, filter, key, id string, hasMore bool) *string {
	if !hasMore {
		return nil
	}
	b, _ := json.Marshal(pageCursor{kind, filter, key, id})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func requireID(w http.ResponseWriter, id, kind string) bool {
	if storage.ValidCatalogID(id, kind) {
		return true
	}
	catalogError(w, http.StatusBadRequest, "invalid_request")
	return false
}

func (a *app) catalogReady(w http.ResponseWriter, r *http.Request) bool {
	if a.store == nil {
		catalogError(w, http.StatusServiceUnavailable, "catalog_unavailable")
		return false
	}
	if err := a.store.Ping(r.Context()); err != nil {
		catalogError(w, http.StatusServiceUnavailable, "catalog_unavailable")
		return false
	}
	if err := a.store.GroupingReady(r.Context()); err != nil {
		catalogError(w, http.StatusServiceUnavailable, "catalog_unavailable")
		return false
	}
	return true
}

func catalogQueryError(w http.ResponseWriter, err error) {
	if storage.IsCatalogNotFound(err) {
		catalogError(w, http.StatusNotFound, "not_found")
		return
	}
	if errors.Is(err, storage.ErrCatalogInvalid) {
		catalogError(w, http.StatusInternalServerError, "catalog_invalid")
		return
	}
	catalogError(w, http.StatusServiceUnavailable, "catalog_unavailable")
}

func (a *app) tracksList(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	q := r.URL.Query()
	artistID, albumID := q.Get("artist_id"), q.Get("album_id")
	if artistID != "" && !storage.ValidCatalogID(artistID, "artist") || albumID != "" && !storage.ValidCatalogID(albumID, "album") {
		catalogError(w, 400, "invalid_request")
		return
	}
	var available *bool
	if raw := q.Get("available"); raw != "" {
		v, e := strconv.ParseBool(raw)
		if e != nil {
			catalogError(w, 400, "invalid_request")
			return
		}
		available = &v
	}
	filter := artistID + "|" + albumID + "|" + q.Get("available")
	p, e := parsePage(r, "tracks", filter)
	if e != nil {
		catalogError(w, 400, "invalid_request")
		return
	}
	items, e := a.store.ListCatalogTracks(r.Context(), p.Limit+1, p.Key, p.ID, artistID, albumID, available)
	if e != nil {
		catalogQueryError(w, e)
		return
	}
	more := len(items) > p.Limit
	if more {
		items = items[:p.Limit]
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = nextPage(p.Kind, p.Filter, last.SortKey, last.ID, more)
	}
	jsonResponse(w, map[string]any{"items": items, "next_cursor": next})
}

func (a *app) trackDetail(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	id := r.PathValue("id")
	if !requireID(w, id, "track") {
		return
	}
	item, e := a.store.GetCatalogTrack(r.Context(), id)
	if e != nil {
		catalogQueryError(w, e)
		return
	}
	jsonResponse(w, item)
}

func (a *app) artistsList(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	p, e := parsePage(r, "artists", "")
	if e != nil {
		catalogError(w, 400, "invalid_request")
		return
	}
	items, e := a.store.ListCatalogArtists(r.Context(), p.Limit+1, p.Key, p.ID)
	if e != nil {
		catalogQueryError(w, e)
		return
	}
	more := len(items) > p.Limit
	if more {
		items = items[:p.Limit]
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = nextPage(p.Kind, "", last.SortKey, last.ID, more)
	}
	jsonResponse(w, map[string]any{"items": items, "next_cursor": next})
}

func (a *app) artistDetail(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	id := r.PathValue("id")
	if !requireID(w, id, "artist") {
		return
	}
	item, e := a.store.GetCatalogArtist(r.Context(), id)
	if e != nil {
		catalogQueryError(w, e)
		return
	}
	jsonResponse(w, item)
}

func (a *app) albumsList(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	artistID := r.URL.Query().Get("artist_id")
	if artistID != "" && !storage.ValidCatalogID(artistID, "artist") {
		catalogError(w, 400, "invalid_request")
		return
	}
	p, e := parsePage(r, "albums", artistID)
	if e != nil {
		catalogError(w, 400, "invalid_request")
		return
	}
	items, e := a.store.ListCatalogAlbums(r.Context(), p.Limit+1, p.Key, p.ID, artistID)
	if e != nil {
		catalogQueryError(w, e)
		return
	}
	more := len(items) > p.Limit
	if more {
		items = items[:p.Limit]
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = nextPage(p.Kind, p.Filter, last.SortKey, last.ID, more)
	}
	jsonResponse(w, map[string]any{"items": items, "next_cursor": next})
}

func (a *app) albumDetail(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	id := r.PathValue("id")
	if !requireID(w, id, "album") {
		return
	}
	item, e := a.store.GetCatalogAlbum(r.Context(), id)
	if e != nil {
		catalogQueryError(w, e)
		return
	}
	jsonResponse(w, item)
}

func (a *app) artistAlbums(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	id := r.PathValue("id")
	if !requireID(w, id, "artist") {
		return
	}
	if _, e := a.store.GetCatalogArtist(r.Context(), id); e != nil {
		catalogQueryError(w, e)
		return
	}
	p, e := parsePage(r, "artist_albums", id)
	if e != nil {
		catalogError(w, 400, "invalid_request")
		return
	}
	items, e := a.store.ListCatalogAlbums(r.Context(), p.Limit+1, p.Key, p.ID, id)
	if e != nil {
		catalogQueryError(w, e)
		return
	}
	more := len(items) > p.Limit
	if more {
		items = items[:p.Limit]
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = nextPage(p.Kind, id, last.SortKey, last.ID, more)
	}
	jsonResponse(w, map[string]any{"items": items, "next_cursor": next})
}

func (a *app) groupedTracks(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	id := r.PathValue("id")
	kind := "artist"
	artistID, albumID := id, ""
	cursorKind := "artist_tracks"
	if strings.Contains(r.URL.Path, "/albums/") {
		kind = "album"
		albumID = id
		artistID = ""
		cursorKind = "album_tracks"
	}
	if !requireID(w, id, kind) {
		return
	}
	if kind == "artist" {
		if _, e := a.store.GetCatalogArtist(r.Context(), id); e != nil {
			catalogQueryError(w, e)
			return
		}
	} else {
		if _, e := a.store.GetCatalogAlbum(r.Context(), id); e != nil {
			catalogQueryError(w, e)
			return
		}
	}
	p, e := parsePage(r, cursorKind, id)
	if e != nil {
		catalogError(w, 400, "invalid_request")
		return
	}
	var items []storage.CatalogTrack
	if kind == "album" {
		items, e = a.store.ListAlbumTracks(r.Context(), p.Limit+1, p.Key, p.ID, albumID)
	} else {
		items, e = a.store.ListCatalogTracks(r.Context(), p.Limit+1, p.Key, p.ID, artistID, albumID, nil)
	}
	if e != nil {
		catalogQueryError(w, e)
		return
	}
	more := len(items) > p.Limit
	if more {
		items = items[:p.Limit]
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = nextPage(p.Kind, id, last.SortKey, last.ID, more)
	}
	jsonResponse(w, map[string]any{"items": items, "next_cursor": next})
}
