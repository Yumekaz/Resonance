//go:build integration

package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

func TestM14PopulatedUpgradeBackfillRetryAndGrouping(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	rootID, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO library_roots(id,name,canonical_path,path_key) VALUES($1,'Test','C:\test-m14',$2)`, rootID, rootID); err != nil {
		t.Fatal(err)
	}
	type sample struct {
		artist, album, albumArtist, path string
		year                             *int
	}
	year := func(v int) *int { return &v }
	samples := []sample{
		{"Björk", "Debut", "Björk", "Björk/Debut/one.mp3", year(1993)},
		{"BJÖRK", "debut", "BJÖRK", "Björk/Debut/two.mp3", year(1993)},
		{"Björk", "Another", "Björk", "Other/Björk/Another/three.mp3", nil},
		{"Singer A", "Mix", "Various Artists", "Comp/Mix/four.mp3", year(2020)},
		{"Singer B", "Mix", "Various Artists", "Comp/Mix/five.mp3", year(2020)},
		{"Singer A & Singer B", "Mix", "", "Comp/Mix/six.mp3", year(2020)},
		{"Singer A", "Mix", "Various Artists", "Comp/Mix/seven.mp3", year(2021)},
		{"", "", "", "Loose/untagged.mp3", nil},
		{"Björk", "Debut", "Björk", "Björk/Debut/eight.mp3", nil},
		{"Singer A", "Mix", "Various Artists", "Comp/Mix/nine.mp3", nil},
		{"Disc Artist", "Disc Album", "Disc Artist", "Disc Artist/Disc Album/CD1/ten.mp3", nil},
		{"Disc Artist", "Disc Album", "Disc Artist", "Disc Artist/Disc Album/CD2/eleven.mp3", nil},
		{"Alex", "Album One", "Alex", "Collections/Album One/twelve.mp3", nil},
		{"Alex", "Album Two", "Alex", "Collections/Album Two/thirteen.mp3", nil},
		{"Alex", "Album Three", "Alex", "Alex/Album Three/fourteen.mp3", nil},
		{"Alex", "Album Four", "Alex", "Alex/Album Four/fifteen.mp3", nil},
	}
	trackIDs := []string{}
	for i, x := range samples {
		trackID := fmt.Sprintf("trk_%032x", i+1)
		objectID := fmt.Sprintf("obj_%032x", i+1)
		locationID := fmt.Sprintf("loc_%032x", i+1)
		hash := sha256.Sum256([]byte(objectID))
		if _, err := s.pool.Exec(ctx, `INSERT INTO tracks(id,title,artist_credit,album_title,album_artist_credit,release_year) VALUES($1,$2,$3,$4,NULLIF($5,''),$6)`, trackID, fmt.Sprintf("Song %d", i), x.artist, x.album, x.albumArtist, x.year); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES($1,$2,$3,'mp3',10)`, objectID, trackID, hash[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size) VALUES($1,$2,'private',$3,$4,10)`, locationID, objectID, rootID, x.path); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, "UPDATE tracks SET metadata_source_location_id=$2 WHERE id=$1", trackID, locationID); err != nil {
			t.Fatal(err)
		}
		trackIDs = append(trackIDs, trackID)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(trackIDs))
	for i, id := range trackIDs[:7] {
		if err := s.pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", id).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	var ungrouped int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM track_album_memberships WHERE track_id=$1)+(SELECT count(*) FROM track_artist_memberships WHERE track_id=$1)`, trackIDs[7]).Scan(&ungrouped); err != nil || ungrouped != 0 {
		t.Fatalf("missing tags fabricated catalog rows: %d %v", ungrouped, err)
	}
	if ids[0] != ids[1] {
		t.Fatal("NFC/case equivalent album evidence did not group")
	}
	if ids[3] != ids[4] {
		t.Fatal("compilation with common album artist did not group")
	}
	if ids[5] == ids[3] || ids[6] == ids[3] {
		t.Fatal("conflicting credit or year merged")
	}
	var missingYearAlbum string
	if err := s.pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", trackIDs[8]).Scan(&missingYearAlbum); err != nil || missingYearAlbum != ids[0] {
		t.Fatalf("unambiguous missing year did not join: %q %v", missingYearAlbum, err)
	}
	var ambiguousCount int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM track_album_memberships WHERE track_id=$1", trackIDs[9]).Scan(&ambiguousCount); err != nil || ambiguousCount != 0 {
		t.Fatalf("ambiguous missing year was grouped: %d %v", ambiguousCount, err)
	}
	var discOne, discTwo string
	if err := s.pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", trackIDs[10]).Scan(&discOne); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", trackIDs[11]).Scan(&discTwo); err != nil || discOne != discTwo {
		t.Fatalf("recognized disc folders did not share album: %q %q %v", discOne, discTwo, err)
	}
	var artistA, artistB string
	for i, dst := range []*string{&artistA, &artistB} {
		if err := s.pool.QueryRow(ctx, "SELECT artist_id FROM track_artist_memberships WHERE track_id=$1 AND role='track_credit'", trackIDs[i*2]).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if artistA == artistB {
		t.Fatal("same-name unrelated artist merged")
	}
	var genericA, genericB, folderA, folderB string
	for i, dst := range map[int]*string{12: &genericA, 13: &genericB, 14: &folderA, 15: &folderB} {
		if err := s.pool.QueryRow(ctx, "SELECT artist_id FROM track_artist_memberships WHERE track_id=$1 AND role='track_credit'", trackIDs[i]).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if genericA == genericB {
		t.Fatal("same-name Artists under a generic parent were merged")
	}
	if folderA != folderB {
		t.Fatal("matching Artist folder did not provide cross-album evidence")
	}
	var compositeCredit string
	if err := s.pool.QueryRow(ctx, "SELECT raw_credit FROM track_artist_memberships WHERE track_id=$1 AND role='track_credit'", trackIDs[5]).Scan(&compositeCredit); err != nil || compositeCredit != "Singer A & Singer B" {
		t.Fatalf("composite credit split or lost: %q %v", compositeCredit, err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE catalog_grouping_state SET completed=false,completed_at=NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM track_album_memberships WHERE track_id=$1", trackIDs[0]); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Ready(ctx), ErrGroupingIncomplete) {
		t.Fatal("incomplete grouping appeared ready")
	}
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION fail_backfill_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected interrupted backfill'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE TRIGGER fail_backfill_insert BEFORE INSERT ON track_album_memberships FOR EACH ROW EXECUTE FUNCTION fail_backfill_insert()`); err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillGrouping(ctx); err == nil {
		t.Fatal("injected backfill failure was ignored")
	}
	if !errors.Is(s.Ready(ctx), ErrGroupingIncomplete) {
		t.Fatal("failed backfill reported ready")
	}
	if _, err := s.pool.Exec(ctx, "DROP TRIGGER fail_backfill_insert ON track_album_memberships"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "DROP FUNCTION fail_backfill_insert()"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := s.pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", trackIDs[0]).Scan(&restored); err != nil || restored != ids[0] {
		t.Fatalf("retry changed stable ID: %q %v", restored, err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	// Model a catalog grouped by the former generic-parent rule. A new binary
	// must reject its completed marker until a retry separates the two credits.
	if _, err := s.pool.Exec(ctx, "UPDATE catalog_artists SET rule_version=1,evidence_scope=$2,evidence_code='artist_folder' WHERE id=$1", genericA, rootID+"/Collections"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE track_artist_memberships SET artist_id=$2,rule_version=1 WHERE track_id=$1", trackIDs[13], genericA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE catalog_albums SET album_artist_id=$2 WHERE id=(SELECT album_id FROM track_album_memberships WHERE track_id=$1)", trackIDs[13], genericA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE catalog_grouping_state SET rule_version=1,completed=true"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Ready(ctx), ErrGroupingIncomplete) {
		t.Fatal("outdated grouping rule appeared ready")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var retiredCount int
	var repairedAID, repairedBID string
	if err := s.pool.QueryRow(ctx, "SELECT artist_id FROM track_artist_memberships WHERE track_id=$1 AND role='track_credit'", trackIDs[12]).Scan(&repairedAID); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT artist_id FROM track_artist_memberships WHERE track_id=$1 AND role='track_credit'", trackIDs[13]).Scan(&repairedBID); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM catalog_artists WHERE id=$1", genericA).Scan(&retiredCount); err != nil {
		t.Fatal(err)
	}
	if repairedAID == repairedBID || retiredCount != 1 {
		t.Fatalf("old same-name merge was not split without recycling its ID: %s %s old=%d", repairedAID, repairedBID, retiredCount)
	}
}

func TestM14SchemaDriftRejected(t *testing.T) {
	for _, drift := range []string{"DROP INDEX catalog_albums_browse_idx", "DROP INDEX catalog_albums_identity_idx; CREATE UNIQUE INDEX catalog_albums_identity_idx ON catalog_albums(root_id,evidence_scope)", "ALTER TABLE track_artist_memberships DROP CONSTRAINT track_artist_memberships_role_check, ADD CONSTRAINT track_artist_memberships_role_check CHECK (length(role)>0)"} {
		t.Run(drift, func(t *testing.T) {
			s, _ := isolatedStore(t)
			ctx := context.Background()
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, drift); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(s.ValidateSchema(ctx), ErrSchemaMismatch) {
				t.Fatal("grouping schema drift passed exact contract")
			}
		})
	}
}

func TestM14SchemaFailureIsAtomicAndRetryable(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "CREATE TABLE catalog_albums(blocker integer)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("conflicting 0006 schema did not fail")
	}
	var versions int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&versions); err != nil || versions != 5 {
		t.Fatalf("failed migration ledger=%d %v", versions, err)
	}
	var stateExists bool
	if err := s.pool.QueryRow(ctx, "SELECT to_regclass('catalog_grouping_state') IS NOT NULL").Scan(&stateExists); err != nil || stateExists {
		t.Fatalf("partial schema committed: %v %v", stateExists, err)
	}
	if _, err := s.pool.Exec(ctx, "DROP TABLE catalog_albums"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}
