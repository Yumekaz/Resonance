//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestM2BulkOccurrenceEditsAreAtomicAndPreserveCurrentToken(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Bulk", "C:\\bulk-fixture")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 82001)
	b, _ := userFixtureTrack(t, s, root, 82002)
	key := func(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }
	if _, err = s.AddQueueCollection(ctx, key(1), QueueCollectionRequest{TrackIDs: []string{a, b, a, b}, Placement: "replace", ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	q, _ := s.ReadQueue(ctx)
	before := q
	req := BulkCollectionRequest{Source: CollectionRef{Kind: "queue"}, ItemIDs: []string{q.Items[2].ID, q.Items[3].ID}, Action: "next", ExpectedVersion: q.Revision}
	result, err := s.EditCollection(ctx, key(2), req)
	if err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if q.Items[1].ID != before.Items[2].ID || q.Items[2].ID != before.Items[3].ID || *q.CurrentItemID != *before.CurrentItemID || *q.SelectionToken != *before.SelectionToken {
		t.Fatal("group move altered current or occurrences")
	}
	replay, err := s.EditCollection(ctx, key(2), req)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal("bulk move replay altered result")
	}
	bad := BulkCollectionRequest{Source: req.Source, ItemIDs: []string{q.Items[1].ID, "qi_00000000000000000000000000000000"}, Action: "remove", ExpectedVersion: q.Revision}
	if _, err = s.EditCollection(ctx, key(3), bad); !errors.Is(err, ErrUserNotFound) {
		t.Fatal("unknown occurrence accepted")
	}
	same, _ := s.ReadQueue(ctx)
	if same.Revision != q.Revision || len(same.Items) != len(q.Items) {
		t.Fatal("partial group removal")
	}
	bad.ItemIDs = []string{q.Items[1].ID, q.Items[1].ID}
	if _, err = s.EditCollection(ctx, key(4), bad); !errors.Is(err, ErrUserInvalid) {
		t.Fatal("duplicate occurrence identifiers accepted")
	}
	removed := BulkCollectionRequest{Source: req.Source, ItemIDs: []string{q.Items[0].ID, q.Items[1].ID}, Action: "remove", ExpectedVersion: q.Revision}
	if _, err = s.EditCollection(ctx, key(5), removed); err != nil {
		t.Fatal(err)
	}
	q, _ = s.ReadQueue(ctx)
	if len(q.Items) != 2 || *q.SelectionToken == *before.SelectionToken {
		t.Fatal("current removal did not fence old decoder")
	}
}

func TestM2BulkMoveBetweenPlaylistsRollsBackBothSides(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Transfer", "C:\\transfer-fixture")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := userFixtureTrack(t, s, root, 83001)
	key := func(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }
	makePlaylist := func(n int, name string) PlaylistDetail {
		result, e := s.CreatePlaylist(ctx, key(n), PlaylistCreateRequest{Name: name, ExpectedVersion: 0})
		if e != nil {
			t.Fatal(e)
		}
		var changed PlaylistChange
		if e = json.Unmarshal(result.Body, &changed); e != nil {
			t.Fatal(e)
		}
		p, e := s.ReadPlaylist(ctx, changed.ID)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	source, target := makePlaylist(1, "Source"), makePlaylist(2, "Target")
	for i := 0; i < 3; i++ {
		if _, err = s.AddPlaylistItem(ctx, key(10+i), PlaylistAddRequest{PlaylistID: source.ID, TrackID: a, ExpectedVersion: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	source, _ = s.ReadPlaylist(ctx, source.ID)
	version := target.Revision
	req := BulkCollectionRequest{Source: CollectionRef{Kind: "playlist", ID: source.ID}, ItemIDs: []string{source.Items[0].ID, source.Items[2].ID}, Action: "move", ExpectedVersion: source.Revision, Target: &CollectionRef{Kind: "playlist", ID: target.ID}, TargetVersion: &version}
	if _, err = s.pool.Exec(ctx, `CREATE FUNCTION bulk_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'bulk fault';END$$;CREATE TRIGGER bulk_fault BEFORE DELETE ON playlist_items FOR EACH ROW EXECUTE FUNCTION bulk_fault()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditCollection(ctx, key(20), req); err == nil {
		t.Fatal("injected cross-collection failure committed")
	}
	still, _ := s.ReadPlaylist(ctx, source.ID)
	empty, _ := s.ReadPlaylist(ctx, target.ID)
	if still.Revision != source.Revision || len(still.Items) != 3 || empty.Revision != target.Revision || len(empty.Items) != 0 {
		t.Fatal("cross-collection move partially committed")
	}
	if _, err = s.pool.Exec(ctx, `DROP TRIGGER bulk_fault ON playlist_items;DROP FUNCTION bulk_fault()`); err != nil {
		t.Fatal(err)
	}
	result, err := s.EditCollection(ctx, key(20), req)
	if err != nil {
		t.Fatal(err)
	}
	still, _ = s.ReadPlaylist(ctx, source.ID)
	filled, _ := s.ReadPlaylist(ctx, target.ID)
	if len(still.Items) != 1 || len(filled.Items) != 2 || filled.Items[0].ID == filled.Items[1].ID || filled.Items[0].TrackID != a || filled.Items[1].TrackID != a {
		t.Fatal("duplicate music occurrences lost in transfer")
	}
	replay, err := s.EditCollection(ctx, key(20), req)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal("cross-collection replay duplicated data")
	}
}

func TestM2BulkLargePlaylistCopyAndCapacityFailureAreAtomic(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Bulk scale", `C:\bulk-scale`)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := userFixtureTrack(t, s, root, 87601)
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	makePlaylist := func(n int) string {
		result, e := s.CreatePlaylist(ctx, key(n), PlaylistCreateRequest{Name: fmt.Sprintf("Scale %d", n), ExpectedVersion: 0})
		if e != nil {
			t.Fatal(e)
		}
		var change PlaylistChange
		if e = json.Unmarshal(result.Body, &change); e != nil {
			t.Fatal(e)
		}
		return change.ID
	}
	source, target := makePlaylist(1), makePlaylist(2)
	ids := make([]string, 2000)
	for n := range ids {
		ids[n] = fmt.Sprintf("pi_%032x", 200000+n)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO playlist_items(id,playlist_id,track_id,position) SELECT id,$2,$3,ordinality-1 FROM unnest($1::text[]) WITH ORDINALITY AS items(id,ordinality)`, ids, source, track); err != nil {
		t.Fatal(err)
	}
	version := int64(0)
	req := BulkCollectionRequest{Source: CollectionRef{Kind: "playlist", ID: source}, ItemIDs: ids, Action: "copy", ExpectedVersion: 0, Target: &CollectionRef{Kind: "playlist", ID: target}, TargetVersion: &version}
	result, err := s.EditCollection(ctx, key(3), req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.EditCollection(ctx, key(3), req)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal("large copy replay changed result", err)
	}
	p, err := s.ReadPlaylist(ctx, target)
	if err != nil || len(p.Items) != 2000 || p.Revision != 1 {
		t.Fatal("large copy lost occurrences", err)
	}
	seen := map[string]bool{}
	for _, item := range p.Items {
		if seen[item.ID] || item.TrackID != track {
			t.Fatal("large copy lost duplicate identity")
		}
		seen[item.ID] = true
	}
	more := make([]string, 1001)
	for n := range more {
		more[n] = fmt.Sprintf("pi_%032x", 300000+n)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO playlist_items(id,playlist_id,track_id,position) SELECT id,$2,$3,1999+ordinality FROM unnest($1::text[]) WITH ORDINALITY AS items(id,ordinality)`, more, target, track); err != nil {
		t.Fatal(err)
	}
	version = 1
	req.Action = "move"
	if _, err = s.EditCollection(ctx, key(4), req); !errors.Is(err, ErrUserLimit) {
		t.Fatal("oversized destination accepted", err)
	}
	original, _ := s.ReadPlaylist(ctx, source)
	unchanged, _ := s.ReadPlaylist(ctx, target)
	if len(original.Items) != 2000 || original.Revision != 0 || len(unchanged.Items) != 3001 || unchanged.Revision != 1 {
		t.Fatal("capacity failure partially moved songs")
	}
}
