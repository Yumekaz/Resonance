//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestM2BoundedFavoriteLookupAndLiteralPlaylistSearch(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "M2 lookup", "C:\\m2-lookup")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 8001)
	b, _ := userFixtureTrack(t, s, root, 8002)
	if err = s.SetFavorite(ctx, a, true); err != nil {
		t.Fatal(err)
	}
	found, err := s.LookupFavorites(ctx, []string{a, b, a})
	if err != nil || len(found) != 1 || found[0] != a {
		t.Fatalf("lookup %v %v", found, err)
	}
	if _, err = s.LookupFavorites(ctx, []string{"private path"}); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("invalid ID %v", err)
	}
	if _, err = s.LookupFavorites(ctx, make([]string, 201)); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("unbounded lookup %v", err)
	}
	for i, name := range []string{"Quiet 100%_雪", "Quiet second", "Unrelated"} {
		if _, err = s.CreatePlaylist(ctx, fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), PlaylistCreateRequest{Name: name, ExpectedVersion: 0}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		q     string
		count int
	}{{"quiet", 2}, {"100%_", 1}, {"雪", 1}, {"%", 1}, {"no match", 0}} {
		result, e := s.ListPlaylists(ctx, 50, nil, "", tc.q)
		if e != nil || len(result) != tc.count {
			t.Fatalf("%q %v %v", tc.q, result, e)
		}
	}
	first, err := s.ListPlaylists(ctx, 1, nil, "", "quiet")
	if err != nil || len(first) != 1 {
		t.Fatal(err)
	}
	next, err := s.ListPlaylists(ctx, 1, &first[0].CreatedAt, first[0].ID, "quiet")
	if err != nil || len(next) != 1 || first[0].ID == next[0].ID {
		t.Fatalf("pagination %v %v", next, err)
	}
	if _, err = s.ListPlaylists(ctx, 1, nil, "", strings.Repeat("x", 121)); !errors.Is(err, ErrUserInvalid) {
		t.Fatalf("query bound %v", err)
	}
}
