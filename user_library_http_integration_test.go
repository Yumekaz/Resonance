//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"resonance/internal/storage"
)

func userRequest(t *testing.T, h http.Handler, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestM15HTTPContractsAndPathBoundary(t *testing.T) {
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	rootPath := t.TempDir()
	root, err := store.AddRoot(ctx, "User test", rootPath)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := addCatalogFixture(t, pool, root.ID, rootPath, "Queue Song", "wav", testWAV(), 81)
	legacyID := "trk_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	legacyTitle := "Rootless"
	if err := store.InsertTrack(ctx, storage.Track{ID: legacyID, Title: &legacyTitle}); err != nil {
		t.Fatal(err)
	}
	var legacyHash [32]byte
	legacyHash[0] = 87
	if err := store.InsertMediaObject(ctx, storage.MediaObject{ID: "obj_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", TrackID: legacyID, SHA256: legacyHash, Format: "mp3", ByteLength: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertLocation(ctx, storage.MediaLocation{ID: "loc_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", MediaObjectID: "obj_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", LocalPath: rootPath}); err != nil {
		t.Fatal(err)
	}
	if err := store.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := newHandlerWithCatalog(filepath.Join(rootPath, "Artist", "Album", "Queue Song.wav"), "Demo", &logs, store.Ready, store)
	key := func(i int) string { return fmt.Sprintf("10000000-0000-4000-8000-%012d", i) }
	add := map[string]any{"track_id": track, "placement": "end", "expected_version": 0}
	w := userRequest(t, h, "POST", "/api/v1/queue/items", key(1), add)
	if w.Code != 201 {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	replay := userRequest(t, h, "POST", "/api/v1/queue/items", key(1), add)
	if replay.Code != 201 || replay.Header().Get("Idempotency-Replayed") != "true" || replay.Body.String() != w.Body.String() {
		t.Fatalf("receipt replay: %d %s", replay.Code, replay.Body.String())
	}
	conflict := userRequest(t, h, "POST", "/api/v1/queue/items", key(1), map[string]any{"track_id": track, "placement": "now", "expected_version": 0})
	if conflict.Code != 409 || !strings.Contains(conflict.Body.String(), "idempotency_conflict") {
		t.Fatal("same key changed request was accepted")
	}
	stale := userRequest(t, h, "POST", "/api/v1/queue/items", key(2), add)
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), "stale_version") {
		t.Fatal("stale version was accepted")
	}
	missingKey := userRequest(t, h, "POST", "/api/v1/queue/items", "", add)
	if missingKey.Code != 400 {
		t.Fatal("missing idempotency key accepted")
	}
	malformedKey := userRequest(t, h, "POST", "/api/v1/queue/items", "not-a-uuid", add)
	if malformedKey.Code != 400 {
		t.Fatal("malformed idempotency key accepted")
	}
	malformedTrack := userRequest(t, h, "POST", "/api/v1/queue/items", key(93), map[string]any{"track_id": "../../media", "placement": "end", "expected_version": 1})
	if malformedTrack.Code != 400 {
		t.Fatal("malformed Track ID accepted")
	}
	nullVersion := userRequest(t, h, "POST", "/api/v1/queue/items", key(92), map[string]any{"track_id": track, "placement": "end", "expected_version": nil})
	if nullVersion.Code != 400 {
		t.Fatal("null expected_version accepted")
	}
	rootless := userRequest(t, h, "POST", "/api/v1/queue/items", key(90), map[string]any{"track_id": legacyID, "placement": "end", "expected_version": 1})
	if rootless.Code != 404 {
		t.Fatalf("rootless Track became queueable: %d", rootless.Code)
	}
	tooLarge := userRequest(t, h, "POST", "/api/v1/playlists", key(91), map[string]any{"name": strings.Repeat("x", 263000), "expected_version": 0})
	if tooLarge.Code != 400 {
		t.Fatalf("oversized JSON accepted: %d", tooLarge.Code)
	}
	now := userRequest(t, h, "POST", "/api/v1/queue/items", key(3), map[string]any{"track_id": track, "placement": "now", "expected_version": 1})
	if now.Code != 201 {
		t.Fatalf("play now %d %s", now.Code, now.Body.String())
	}
	var selected storage.QueueChange
	if json.Unmarshal(now.Body.Bytes(), &selected) != nil || selected.CurrentItemID == nil || selected.SelectionToken == nil {
		t.Fatal("selection response missing")
	}
	queue := userRequest(t, h, "GET", "/api/v1/queue", "", nil)
	if queue.Code != 200 || !strings.Contains(queue.Body.String(), `"revision":2`) {
		t.Fatal("queue read failed")
	}
	sessionID := key(20)
	clientID := key(21)
	start := userRequest(t, h, "POST", "/api/v1/listening-sessions", "", map[string]any{"id": sessionID, "track_id": track, "client_instance_id": clientID, "queue_item_id": selected.CurrentItemID, "selection_token": selected.SelectionToken})
	if start.Code != 201 {
		t.Fatalf("session start %d %s", start.Code, start.Body.String())
	}
	ended := "ended"
	duration := int64(1000)
	report := storage.SessionReportRequest{Sequence: 1, ListenedMS: 1000, PositionMS: 1000, DurationMS: &duration, TerminalReason: &ended}
	advance := userRequest(t, h, "POST", "/api/v1/queue/advance", key(22), storage.QueueAdvanceRequest{Direction: "ended", ExpectedVersion: 2, ExpectedCurrentItemID: selected.CurrentItemID, SelectionToken: selected.SelectionToken, SessionID: &sessionID, FinalReport: &report})
	if advance.Code != 200 {
		t.Fatalf("ended advance %d %s", advance.Code, advance.Body.String())
	}
	staleEnd := userRequest(t, h, "POST", "/api/v1/queue/advance", key(23), storage.QueueAdvanceRequest{Direction: "ended", ExpectedVersion: 2, ExpectedCurrentItemID: selected.CurrentItemID, SelectionToken: selected.SelectionToken, SessionID: &sessionID, FinalReport: &report})
	if staleEnd.Code != 409 || !strings.Contains(staleEnd.Body.String(), "stale_selection") {
		t.Fatal("stale ended token was accepted")
	}
	if got := userRequest(t, h, "DELETE", "/api/v1/queue/items/not-an-id", key(24), map[string]any{"expected_version": 3}); got.Code != 400 {
		t.Fatalf("malformed queue item ID status %d", got.Code)
	}
	missingQueueItem := "qi_" + strings.Repeat("f", 32)
	if got := userRequest(t, h, "DELETE", "/api/v1/queue/items/"+missingQueueItem, key(25), map[string]any{"expected_version": 3}); got.Code != 404 {
		t.Fatalf("unknown queue item ID status %d", got.Code)
	}
	history := userRequest(t, h, "GET", "/api/v1/history", "", nil)
	if history.Code != 200 || !strings.Contains(history.Body.String(), sessionID) {
		t.Fatalf("history %d %s", history.Code, history.Body.String())
	}
	fav := userRequest(t, h, "PUT", "/api/v1/favorites/tracks/"+track, "", nil)
	if fav.Code != 204 {
		t.Fatal("favorite PUT failed")
	}
	fav = userRequest(t, h, "PUT", "/api/v1/favorites/tracks/"+track, "", nil)
	if fav.Code != 204 {
		t.Fatal("favorite repeat failed")
	}
	favorites := userRequest(t, h, "GET", "/api/v1/favorites", "", nil)
	if favorites.Code != 200 || !strings.Contains(favorites.Body.String(), track) {
		t.Fatal("favorite list failed")
	}
	created := userRequest(t, h, "POST", "/api/v1/playlists", key(30), map[string]any{"name": "HTTP Mix", "expected_version": 0})
	if created.Code != 201 {
		t.Fatalf("playlist create %d %s", created.Code, created.Body.String())
	}
	var p storage.PlaylistChange
	_ = json.Unmarshal(created.Body.Bytes(), &p)
	secondPlaylist := userRequest(t, h, "POST", "/api/v1/playlists", key(32), map[string]any{"name": "Another Mix", "expected_version": 0})
	if secondPlaylist.Code != 201 {
		t.Fatalf("second playlist %d", secondPlaylist.Code)
	}
	page := userRequest(t, h, "GET", "/api/v1/playlists?limit=1", "", nil)
	var listPage struct {
		Next *string `json:"next_cursor"`
	}
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &listPage) != nil || listPage.Next == nil {
		t.Fatalf("playlist pagination %d %s", page.Code, page.Body.String())
	}
	if next := userRequest(t, h, "GET", "/api/v1/playlists?limit=1&cursor="+*listPage.Next, "", nil); next.Code != 200 {
		t.Fatalf("playlist cursor %d", next.Code)
	}
	badPlaylistID := "pl_" + strings.Repeat("f", 31) + "z"
	badPlaylistCursor := base64.RawURLEncoding.EncodeToString([]byte(`{"k":"playlists","t":"2026-09-24T00:00:00Z","i":"` + badPlaylistID + `"}`))
	for _, path := range []string{"/api/v1/playlists?limit=0", "/api/v1/playlists?limit=201", "/api/v1/playlists?cursor=bad", "/api/v1/playlists?cursor=" + badPlaylistCursor, "/api/v1/favorites?limit=0", "/api/v1/history?cursor=bad", "/api/v1/playlists/not-valid"} {
		got := userRequest(t, h, "GET", path, "", nil)
		if got.Code != 400 {
			t.Fatalf("invalid page/id accepted %s %d", path, got.Code)
		}
	}
	entry := userRequest(t, h, "POST", "/api/v1/playlists/"+p.ID+"/items", key(31), map[string]any{"track_id": track, "expected_version": 0})
	if entry.Code != 201 {
		t.Fatalf("playlist add %d %s", entry.Code, entry.Body.String())
	}
	if got := userRequest(t, h, "DELETE", "/api/v1/playlists/"+p.ID+"/items/not-an-id", key(33), map[string]any{"expected_version": 1}); got.Code != 400 {
		t.Fatalf("malformed playlist item ID status %d", got.Code)
	}
	missingPlaylistItem := "pi_" + strings.Repeat("f", 32)
	if got := userRequest(t, h, "DELETE", "/api/v1/playlists/"+p.ID+"/items/"+missingPlaylistItem, key(34), map[string]any{"expected_version": 1}); got.Code != 404 {
		t.Fatalf("unknown playlist item ID status %d", got.Code)
	}
	for _, path := range []string{"/api/v1/playlists", "/api/v1/playlists/" + p.ID, "/api/v1/queue", "/api/v1/favorites", "/api/v1/history"} {
		got := userRequest(t, h, "GET", path, "", nil)
		if got.Code != 200 || strings.Contains(got.Body.String(), rootPath) || strings.Contains(got.Body.String(), "Artist/Album/") {
			t.Fatalf("route %s disclosed path or failed: %d", path, got.Code)
		}
	}
	deleted := userRequest(t, h, "DELETE", "/api/v1/playlists/"+p.ID, "", map[string]any{"expected_version": 1})
	if deleted.Code != 204 {
		t.Fatalf("playlist delete %d %s", deleted.Code, deleted.Body.String())
	}
	if strings.Contains(logs.String(), rootPath) {
		t.Fatal("user-state log disclosed host path")
	}
	store.Close()
	for _, path := range []string{"/api/v1/queue", "/api/v1/playlists", "/api/v1/favorites", "/api/v1/history"} {
		got := userRequest(t, h, "GET", path, "", nil)
		if got.Code != 503 || !strings.Contains(got.Body.String(), "catalog_unavailable") {
			t.Fatalf("database outage route %s: %d", path, got.Code)
		}
	}
	mutating := userRequest(t, h, "POST", "/api/v1/queue/items", key(99), map[string]any{"track_id": track, "placement": "end", "expected_version": 3})
	if mutating.Code != 503 {
		t.Fatalf("database outage mutation: %d", mutating.Code)
	}
	for _, request := range []struct {
		method string
		path   string
		key    string
		body   any
	}{
		{"POST", "/api/v1/playlists", key(100), map[string]any{"name": "Outage", "expected_version": 0}},
		{"PUT", "/api/v1/favorites/tracks/" + track, "", nil},
		{"POST", "/api/v1/listening-sessions", "", map[string]any{"id": key(101), "track_id": track, "client_instance_id": key(102)}},
		{"PUT", "/api/v1/listening-sessions/" + sessionID + "/report", "", storage.SessionReportRequest{Sequence: 2, ListenedMS: 1000, PositionMS: 1000}},
	} {
		got := userRequest(t, h, request.method, request.path, request.key, request.body)
		if got.Code != 503 || !strings.Contains(got.Body.String(), "catalog_unavailable") {
			t.Fatalf("database outage %s %s: %d %s", request.method, request.path, got.Code, got.Body.String())
		}
	}
	if got := userRequest(t, h, "GET", "/health", "", nil); got.Code != 200 {
		t.Fatal("health failed with catalog")
	}
	if got := userRequest(t, h, "GET", "/api/v1/demo-track", "", nil); got.Code != 200 {
		t.Fatal("demo failed with catalog")
	}
}
