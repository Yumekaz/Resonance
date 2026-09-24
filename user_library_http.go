package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"resonance/internal/storage"
)

func userError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrUserInvalid):
		catalogError(w, 400, "invalid_request")
	case errors.Is(err, storage.ErrUserNotFound):
		catalogError(w, 404, "not_found")
	case errors.Is(err, storage.ErrStaleVersion):
		catalogError(w, 409, "stale_version")
	case errors.Is(err, storage.ErrStaleSelection):
		catalogError(w, 409, "stale_selection")
	case errors.Is(err, storage.ErrIdempotencyConflict):
		catalogError(w, 409, "idempotency_conflict")
	case errors.Is(err, storage.ErrUserLimit):
		catalogError(w, 409, "limit_exceeded")
	default:
		catalogError(w, 503, "catalog_unavailable")
	}
}

func decodeUserJSON(r *http.Request, target any, version bool) error {
	if r.ContentLength > 262144 {
		return storage.ErrUserInvalid
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 262145))
	if err != nil || len(body) == 0 || len(body) > 262144 {
		return storage.ErrUserInvalid
	}
	if version {
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || fields["expected_version"] == nil || bytes.Equal(bytes.TrimSpace(fields["expected_version"]), []byte("null")) {
			return storage.ErrUserInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return storage.ErrUserInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return storage.ErrUserInvalid
	}
	return nil
}
func mutationKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !storage.ValidUUIDString(values[0]) {
		return "", storage.ErrUserInvalid
	}
	return values[0], nil
}
func sendMutation(w http.ResponseWriter, result storage.MutationResult) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

func (a *app) queueRead(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	state, err := a.store.ReadQueue(r.Context())
	if err != nil {
		userError(w, err)
		return
	}
	jsonResponse(w, state)
}
func (a *app) queueAdd(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.QueueAddRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	result, err := a.store.AddQueueItem(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) queueRemove(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.QueueRemoveRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	req.ItemID = r.PathValue("item_id")
	result, err := a.store.RemoveQueueItem(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) queueOrder(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.QueueOrderRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	result, err := a.store.ReorderQueue(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) queueClear(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.QueueClearRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	result, err := a.store.ClearQueue(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) queueAdvance(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.QueueAdvanceRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	result, err := a.store.AdvanceQueue(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}

type timedCursor struct {
	Kind string    `json:"k"`
	Time time.Time `json:"t"`
	ID   string    `json:"i"`
}

func parseTimedPage(r *http.Request, kind string, idCheck func(string) bool) (int, *time.Time, string, error) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 200 {
			return 0, nil, "", storage.ErrUserInvalid
		}
		limit = n
	}
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return limit, nil, "", nil
	}
	if len(raw) > 4096 {
		return 0, nil, "", storage.ErrUserInvalid
	}
	body, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return 0, nil, "", storage.ErrUserInvalid
	}
	var c timedCursor
	if json.Unmarshal(body, &c) != nil || c.Kind != kind || c.Time.IsZero() || !idCheck(c.ID) {
		return 0, nil, "", storage.ErrUserInvalid
	}
	return limit, &c.Time, c.ID, nil
}
func makeTimedCursor(kind string, t time.Time, id string, more bool) *string {
	if !more {
		return nil
	}
	body, _ := json.Marshal(timedCursor{kind, t, id})
	value := base64.RawURLEncoding.EncodeToString(body)
	return &value
}
func playlistID(id string) bool {
	if !strings.HasPrefix(id, "pl_") || len(id) != 35 {
		return false
	}
	for _, c := range id[3:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func (a *app) playlistsList(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	limit, t, id, err := parseTimedPage(r, "playlists", playlistID)
	if err != nil {
		userError(w, err)
		return
	}
	items, err := a.store.ListPlaylists(r.Context(), limit+1, t, id)
	if err != nil {
		userError(w, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = makeTimedCursor("playlists", last.CreatedAt, last.ID, more)
	}
	jsonResponse(w, map[string]any{"items": items, "next_cursor": next})
}
func (a *app) playlistCreate(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.PlaylistCreateRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	result, err := a.store.CreatePlaylist(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) playlistRead(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	item, err := a.store.ReadPlaylist(r.Context(), r.PathValue("id"))
	if err != nil {
		userError(w, err)
		return
	}
	jsonResponse(w, item)
}
func (a *app) playlistRename(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.PlaylistRenameRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	req.ID = r.PathValue("id")
	result, err := a.store.RenamePlaylist(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) playlistDelete(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	var req struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	if err := decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	if err := a.store.DeletePlaylist(r.Context(), r.PathValue("id"), req.ExpectedVersion); err != nil {
		userError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *app) playlistAdd(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.PlaylistAddRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	req.PlaylistID = r.PathValue("id")
	result, err := a.store.AddPlaylistItem(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) playlistRemove(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.PlaylistRemoveRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	req.PlaylistID = r.PathValue("id")
	req.ItemID = r.PathValue("item_id")
	result, err := a.store.RemovePlaylistItem(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) playlistOrder(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.PlaylistOrderRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	req.PlaylistID = r.PathValue("id")
	result, err := a.store.ReorderPlaylist(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}

func (a *app) favoritesList(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	limit, t, id, err := parseTimedPage(r, "favorites", func(id string) bool { return storage.ValidCatalogID(id, "track") })
	if err != nil {
		userError(w, err)
		return
	}
	items, err := a.store.ListFavorites(r.Context(), limit+1, t, id)
	if err != nil {
		userError(w, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = makeTimedCursor("favorites", last.CreatedAt, last.TrackID, more)
	}
	jsonResponse(w, map[string]any{"items": items, "next_cursor": next})
}
func (a *app) favoriteSet(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	if err := a.store.SetFavorite(r.Context(), r.PathValue("track_id"), r.Method == "PUT"); err != nil {
		userError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (a *app) sessionStart(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	var req storage.SessionStartRequest
	if err := decodeUserJSON(r, &req, false); err != nil {
		userError(w, err)
		return
	}
	item, err := a.store.StartListeningSession(r.Context(), req)
	if err != nil {
		userError(w, err)
		return
	}
	jsonResponseStatus(w, 201, item)
}
func (a *app) sessionReport(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	var req storage.SessionReportRequest
	if err := decodeUserJSON(r, &req, false); err != nil {
		userError(w, err)
		return
	}
	item, err := a.store.ReportListeningSession(r.Context(), r.PathValue("id"), req)
	if err != nil {
		userError(w, err)
		return
	}
	jsonResponse(w, item)
}
func (a *app) historyList(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	limit, t, id, err := parseTimedPage(r, "history", storage.ValidUUIDString)
	if err != nil {
		userError(w, err)
		return
	}
	items, err := a.store.ListHistory(r.Context(), limit+1, t, id)
	if err != nil {
		userError(w, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = makeTimedCursor("history", last.StartedAt, last.ID, more)
	}
	jsonResponse(w, map[string]any{"items": items, "next_cursor": next})
}
