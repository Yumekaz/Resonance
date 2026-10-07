//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func TestM2PlaylistSourceMatchesLiteralViewOrderAndDuplicateOccurrences(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "View source", `C:\view-source`)
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{"z", "A", "Écho", "\ue000", "𐀀", ""}
	ids := make([]string, len(titles))
	for n, title := range titles {
		ids[n], _ = userFixtureTrack(t, s, root, 89000+n)
		if _, err = s.pool.Exec(ctx, "UPDATE tracks SET title=$2,artist_credit=$3,album_title=$4 WHERE id=$1", ids[n], title, fmt.Sprintf("Artist %d", len(titles)-n), fmt.Sprintf("Album %d", n)); err != nil {
			t.Fatal(err)
		}
	}
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	created, err := s.CreatePlaylist(ctx, key(1), PlaylistCreateRequest{Name: "View source", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var change PlaylistChange
	if err = json.Unmarshal(created.Body, &change); err != nil {
		t.Fatal(err)
	}
	members := append(slices.Clone(ids), ids[1])
	for n, id := range members {
		if _, err = s.AddPlaylistItem(ctx, key(2+n), PlaylistAddRequest{PlaylistID: change.ID, TrackID: id, ExpectedVersion: int64(n)}); err != nil {
			t.Fatal(err)
		}
	}
	version := int64(len(members))
	cases := []struct {
		order, query string
		want         []string
	}{
		{"original", "", members},
		{"title", "", []string{ids[1], ids[1], ids[5], ids[0], ids[2], ids[3], ids[4]}},
		{"title_desc", "", []string{ids[4], ids[3], ids[2], ids[0], ids[5], ids[1], ids[1]}},
		{"artist", "", []string{ids[5], ids[4], ids[3], ids[2], ids[1], ids[1], ids[0]}},
		{"album", "", []string{ids[0], ids[1], ids[1], ids[2], ids[3], ids[4], ids[5]}},
		{"original", "E\u0301cho", []string{ids[2]}},
		{"original", "Untitled", []string{ids[5]}},
	}
	for n, tc := range cases {
		t.Run(tc.order+tc.query, func(t *testing.T) {
			q, err := s.ReadQueue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.StartQueueContext(ctx, key(20+n), ContextStartRequest{Source: PlaybackSource{Kind: "playlist", ID: change.ID, Order: tc.order, Query: tc.query}, SourceVersion: &version, ExpectedVersion: q.Revision}); err != nil {
				t.Fatal(err)
			}
			q, err = s.ReadQueue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(q.Items))
			for i, item := range q.Items {
				got[i] = item.TrackID
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("view/source diverged: got %v want %v", got, tc.want)
			}
		})
	}
}
