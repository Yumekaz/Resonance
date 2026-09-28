//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resonance/internal/library"
	"resonance/internal/storage"
)

func TestM17AllPublicRoutesPrivacyAndConfinementCanaries(t *testing.T) {
	store, pool := catalogTestStore(t)
	ctx := context.Background()
	dir, err := os.MkdirTemp("data/m17-closure", "M17_CANONICAL_CANARY-")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.Abs(dir)
	canonical, err := library.CanonicalizeRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	identity, ok, err := library.CaptureRootIdentity(canonical)
	if err != nil || !ok {
		t.Fatal(err)
	}
	root, err := store.AddRootWithIdentity(ctx, "Privacy fixture", canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	track, loc := addCatalogFixture(t, pool, root.ID, canonical, "Safe Track", "wav", testWAV(), 17)
	old := filepath.Join(canonical, "Artist", "Album", "Safe Track.wav")
	rel := "Artist/Album/M17_RELATIVE_CANARY.wav"
	media := filepath.Join(canonical, filepath.FromSlash(rel))
	if err := os.Rename(old, media); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE media_locations SET relative_path=$1,local_path=$2 WHERE id=$3", rel, media, loc); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,availability,unavailable_reason,unavailable_at,native_id_kind,native_id_scope,native_id) VALUES('loc_00000000000000000000000000000018','obj_00000000000000000000000000000011','C:\M17_NATIVE_IDENTITY_CANARY',$1,'private-native-copy.wav','unavailable','not_found',now(),'windows-file-id','M17_NATIVE_IDENTITY_CANARY',decode(repeat('17',16),'hex'))`, root.ID); err != nil {
		t.Fatal(err)
	}
	// Root native identity is a private persisted fact; never serialize it.
	if err := store.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	var artist, album string
	if err := pool.QueryRow(ctx, "SELECT artist_id FROM track_artist_memberships WHERE track_id=$1 AND role='track_credit'", track).Scan(&artist); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", track).Scan(&album); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := newHandlerWithCatalog(media, "Demo", &logs, store.Ready, store)
	checks := []map[string]any{}
	keyCount := 0
	key := func() string { keyCount++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", keyCount) }
	private := []string{canonical, rel, "M17_CANONICAL_CANARY", "M17_RELATIVE_CANARY", "M17_NATIVE_IDENTITY_CANARY", "M17_CREDENTIAL_CANARY", "postgresql://private"}
	inspect := func(label string, w *httptest.ResponseRecorder) {
		for _, canary := range private {
			if bytes.Contains(bytes.ToLower(w.Body.Bytes()), []byte(strings.ToLower(canary))) {
				t.Fatalf("private canary leaked in %s", label)
			}
			for _, values := range w.Header() {
				for _, value := range values {
					if strings.Contains(strings.ToLower(value), strings.ToLower(canary)) {
						t.Fatalf("private canary leaked in %s header", label)
					}
				}
			}
		}
		checks = append(checks, map[string]any{"case": label, "status": w.Code, "request_id": w.Header().Get("X-Request-ID")})
	}
	call := func(method, path, k string, body any, want int) *httptest.ResponseRecorder {
		w := userRequest(t, h, method, path, k, body)
		inspect(method+" "+path, w)
		if w.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	for _, path := range []string{"/health", "/ready", "/api/v1/demo-track", "/api/v1/tracks", "/api/v1/tracks/" + track, "/api/v1/artists", "/api/v1/artists/" + artist, "/api/v1/albums", "/api/v1/albums/" + album, "/api/v1/artists/" + artist + "/albums", "/api/v1/artists/" + artist + "/tracks", "/api/v1/albums/" + album + "/tracks", "/api/v1/queue", "/api/v1/playlists", "/api/v1/favorites", "/api/v1/history", "/", "/demo", "/app.js", "/library.js", "/user-library.js"} {
		call("GET", path, "", nil, 200)
	}
	call("GET", "/api/v1/library/status", "", nil, 503)
	call("GET", "/api/v1/tracks/"+track+"/artwork", "", nil, 404)
	call("HEAD", "/api/v1/tracks/"+track+"/stream", "", nil, 200)
	for _, path := range []string{"/media/demo-track", "/api/v1/tracks/" + track + "/stream"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Range", "bytes=0-1023")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		inspect("valid range", w)
		if w.Code != 206 || w.Body.Len() != 1024 {
			t.Fatal("valid confined range failed")
		}
	}
	addKey := key()
	addBody := map[string]any{"track_id": track, "placement": "now", "expected_version": 0}
	added := call("POST", "/api/v1/queue/items", addKey, addBody, 201)
	call("POST", "/api/v1/queue/items", addKey, addBody, 201)
	call("POST", "/api/v1/queue/items", addKey, map[string]any{"track_id": track, "placement": "end", "expected_version": 0}, 409)
	var selection storage.QueueChange
	_ = json.Unmarshal(added.Body.Bytes(), &selection)
	call("PUT", "/api/v1/queue/order", key(), map[string]any{"item_ids": []string{*selection.ItemID}, "expected_version": 1}, 200)
	call("PUT", "/api/v1/queue/order", key(), map[string]any{"item_ids": []string{*selection.ItemID, *selection.ItemID}, "expected_version": 2}, 400)
	call("POST", "/api/v1/queue/advance", key(), map[string]any{"direction": "next", "expected_version": 2, "expected_current_item_id": selection.CurrentItemID, "selection_token": "00000000-0000-4000-8000-ffffffffffff"}, 409)
	call("POST", "/api/v1/queue/advance", key(), map[string]any{"direction": "next", "expected_version": 2, "expected_current_item_id": selection.CurrentItemID, "selection_token": selection.SelectionToken}, 200)
	call("DELETE", "/api/v1/queue/items/"+*selection.ItemID, key(), map[string]any{"expected_version": 3}, 200)
	call("DELETE", "/api/v1/queue", key(), map[string]any{"expected_version": 4}, 200)
	playlist := call("POST", "/api/v1/playlists", key(), map[string]any{"name": "Privacy playlist", "expected_version": 0}, 201)
	var pl storage.PlaylistChange
	_ = json.Unmarshal(playlist.Body.Bytes(), &pl)
	call("GET", "/api/v1/playlists/"+pl.ID, "", nil, 200)
	entry := call("POST", "/api/v1/playlists/"+pl.ID+"/items", key(), map[string]any{"track_id": track, "expected_version": 0}, 201)
	var item storage.PlaylistChange
	_ = json.Unmarshal(entry.Body.Bytes(), &item)
	call("PUT", "/api/v1/playlists/"+pl.ID+"/order", key(), map[string]any{"item_ids": []string{*item.ItemID}, "expected_version": 1}, 200)
	call("PATCH", "/api/v1/playlists/"+pl.ID, key(), map[string]any{"name": "Renamed", "expected_version": 2}, 200)
	call("DELETE", "/api/v1/playlists/"+pl.ID+"/items/"+*item.ItemID, key(), map[string]any{"expected_version": 3}, 200)
	call("DELETE", "/api/v1/playlists/"+pl.ID, "", map[string]any{"expected_version": 4}, 204)
	call("PUT", "/api/v1/favorites/tracks/"+track, "", nil, 204)
	call("DELETE", "/api/v1/favorites/tracks/"+track, "", nil, 204)
	session := "00000000-0000-4000-8000-000000000117"
	call("POST", "/api/v1/listening-sessions", "", map[string]any{"id": session, "track_id": track, "client_instance_id": "00000000-0000-4000-8000-000000000118"}, 201)
	_, _ = pool.Exec(ctx, "UPDATE playback_sessions SET started_at=now()-interval '5 seconds' WHERE id=$1", session)
	call("PUT", "/api/v1/listening-sessions/"+session+"/report", "", map[string]any{"sequence": 1, "listened_ms": 2000, "position_ms": 2000, "duration_ms": 2000, "terminal_reason": "ended"}, 200)
	call("GET", "/api/v1/history", "", nil, 200)
	// Redirects may echo the submitted request path. Use attacker-controlled
	// names here; private canaries are held only in server-side fixture state.
	for _, path := range []string{"/api/v1/tracks?cursor=bogus", "/api/v1/playlists?cursor=bogus", "/api/v1/history?cursor=bogus", "/api/v1/artists?limit=-1", "/api/v1/albums?limit=999999", "/api/v1/tracks/../../attacker", "/api/v1/tracks/C:%5cattacker/stream", "/api/v1/tracks/NUL/stream", "/api/v1/tracks/foo:secret/stream", "/media/C:%5cattacker", "/api/v1/tracks/trk_%00/stream"} {
		w := userRequest(t, h, "GET", path, "", nil)
		inspect("malformed/confined request", w)
		if w.Code < 300 {
			t.Fatalf("path/input attack succeeded %s", path)
		}
	}
	for _, body := range []string{"{", strings.Repeat("x", 262145), `{"track_id":"M17_CANONICAL_CANARY","placement":"end","expected_version":5}`, `{"track_id":"` + track + `","placement":"end","expected_version":5,"local_path":"M17_RELATIVE_CANARY"}`} {
		r := httptest.NewRequest("POST", "/api/v1/queue/items", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", key())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		inspect("invalid/oversized JSON", w)
		if w.Code != 400 {
			t.Fatal("invalid JSON accepted", w.Code)
		}
	}
	call("POST", "/api/v1/queue/items", "M17_CREDENTIAL_CANARY", map[string]any{"track_id": track, "placement": "end", "expected_version": 5}, 400)
	unavailable := func(context.Context) error {
		return errors.New("postgresql://private M17_CREDENTIAL_CANARY M17_NATIVE_IDENTITY_CANARY " + canonical)
	}
	store.Close()
	outage := newHandlerWithCatalog(media, "Demo", &logs, unavailable, store)
	for _, path := range []string{"/ready", "/api/v1/tracks", "/api/v1/queue", "/api/v1/playlists", "/api/v1/favorites", "/api/v1/history", "/api/v1/tracks/" + track + "/stream"} {
		w := httptest.NewRecorder()
		outage.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		inspect("private dependency failure", w)
		if w.Code != 503 {
			t.Fatal("dependency failure not typed 503")
		}
	}
	rows, err := pool.Query(ctx, "SELECT canonical_request,response_body FROM mutation_receipts")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var canonical, response []byte
		if err := rows.Scan(&canonical, &response); err != nil {
			t.Fatal(err)
		}
		for _, canary := range private {
			if bytes.Contains(canonical, []byte(canary)) || bytes.Contains(response, []byte(canary)) {
				t.Fatal("receipt leaked private state")
			}
		}
	}
	rows.Close()
	for _, canary := range private {
		if bytes.Contains(logs.Bytes(), []byte(canary)) {
			t.Fatal("telemetry leaked private state")
		}
	}
	if out := os.Getenv("RESONANCE_M17_ATTEMPT_OUTPUT"); out != "" {
		body, _ := json.MarshalIndent(map[string]any{"checks": checks, "response_header_body_canaries_absent": true, "receipt_canaries_absent": true, "telemetry_canaries_absent": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(out, "security-result.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
