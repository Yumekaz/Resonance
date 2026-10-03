//go:build integration

package main

import (
	"context"
	"encoding/json"
	"testing"

	"resonance/internal/storage"
)

func TestM2DisabledFoldersLeaveDiscoveryButRetainSavedTracks(t *testing.T) {
	s, pool := catalogTestStore(t)
	ctx := context.Background()
	dirA, dirB := catalogWorkspaceTempDir(t), catalogWorkspaceTempDir(t)
	a, err := s.AddRoot(ctx, "Selected music", dirA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddRoot(ctx, "Other music", dirB)
	if err != nil {
		t.Fatal(err)
	}
	trackA, _ := addCatalogFixture(t, pool, a.ID, dirA, "Selected song", "wav", testWAV(), 7001)
	bytesB := testWAV()
	bytesB[45] = 1
	trackB, _ := addCatalogFixture(t, pool, b.ID, dirB, "Other song", "wav", bytesB, 7002)
	if _, err = pool.Exec(ctx, "UPDATE tracks SET artist_credit='Other artist',album_artist_credit='Other artist',album_title='Other album' WHERE id=$1", trackB); err != nil {
		t.Fatal(err)
	}
	if err = s.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetCatalogTrack(ctx, trackB)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetFavorite(ctx, trackB, true); err != nil {
		t.Fatal(err)
	}
	playlist, err := s.CreatePlaylist(ctx, "00000000-0000-4000-8000-000000007001", storage.PlaylistCreateRequest{Name: "Retained", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var change storage.PlaylistChange
	if err = json.Unmarshal(playlist.Body, &change); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddPlaylistItem(ctx, "00000000-0000-4000-8000-000000007002", storage.PlaylistAddRequest{PlaylistID: change.ID, TrackID: trackB, ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	if err = s.DisableRoot(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	tracks, err := s.ListCatalogTracks(ctx, 50, "", "", "", "", nil)
	if err != nil || len(tracks) != 1 || tracks[0].ID != trackA {
		t.Fatalf("disabled folder leaked into Library: %v %v", tracks, err)
	}
	artists, err := s.ListCatalogArtists(ctx, 50, "", "")
	if err != nil || len(artists) != 1 || artists[0].TrackCount != 1 {
		t.Fatalf("artist discovery: %v %v", artists, err)
	}
	albums, err := s.ListCatalogAlbums(ctx, 50, "", "", "")
	if err != nil || len(albums) != 1 || albums[0].TrackCount != 1 {
		t.Fatalf("album discovery: %v %v", albums, err)
	}
	results, err := s.SearchCatalog(ctx, "Other", 50)
	if err != nil || len(results.Tracks)+len(results.Artists)+len(results.Albums) != 0 {
		t.Fatalf("disabled folder leaked into Search: %v %v", results, err)
	}
	groupTracks, err := s.ListAlbumTracks(ctx, 50, "", "", *before.AlbumID)
	if err != nil || len(groupTracks) != 0 {
		t.Fatalf("disabled album tracks leaked: %v %v", groupTracks, err)
	}
	retained, err := s.GetCatalogTrack(ctx, trackB)
	if err != nil || retained.Available {
		t.Fatalf("saved track lost or still playable: %v %v", retained, err)
	}
	favorites, err := s.LookupFavorites(ctx, []string{trackB})
	if err != nil || len(favorites) != 1 {
		t.Fatalf("favorite lost: %v %v", favorites, err)
	}
	detail, err := s.ReadPlaylist(ctx, change.ID)
	if err != nil || len(detail.Items) != 1 || detail.Items[0].TrackID != trackB || detail.Items[0].Available {
		t.Fatalf("saved playlist lost: %v %v", detail, err)
	}
	// A second enabled copy keeps a musical identity discoverable. Count once.
	if _, err = pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size,observed_mtime_ns) SELECT 'loc_00000000000000000000000000007003',media_object_id,'private',$1,'copy.wav',observed_size,observed_mtime_ns FROM media_locations WHERE root_id=$2`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	tracks, err = s.ListCatalogTracks(ctx, 50, "", "", "", "", nil)
	if err != nil || len(tracks) != 2 {
		t.Fatalf("enabled alternate copy hidden: %v %v", tracks, err)
	}
	// A missing file in a still-enabled folder remains visible for recovery.
	if _, err = pool.Exec(ctx, "UPDATE media_locations SET availability='unavailable',unavailable_reason='missing',unavailable_at=now() WHERE root_id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	tracks, err = s.ListCatalogTracks(ctx, 50, "", "", "", "", nil)
	if err != nil || len(tracks) != 2 || tracks[0].Available || tracks[1].Available {
		t.Fatalf("missing enabled-folder tracks lost: %v %v", tracks, err)
	}
}
