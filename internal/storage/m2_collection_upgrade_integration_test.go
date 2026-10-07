//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestM2CollectionUpgradePreservesPopulatedV8AndRejectsSchemaDrift(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Upgrade fixture", `C:\upgrade-fixture`)
	if err != nil {
		t.Fatal(err)
	}
	track, _ := userFixtureTrack(t, s, root, 88001)
	key := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	created, err := s.CreatePlaylist(ctx, key(1), PlaylistCreateRequest{Name: "夜の collection", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	var playlist PlaylistChange
	if err = json.Unmarshal(created.Body, &playlist); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddPlaylistItem(ctx, key(2), PlaylistAddRequest{PlaylistID: playlist.ID, TrackID: track, ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO queue_items(id,track_id,position) VALUES('qi_00000000000000000000000000000001',$1,0),('qi_00000000000000000000000000000002',$1,1);UPDATE active_queue SET revision=1,current_item_id='qi_00000000000000000000000000000001',selection_token='00000000-0000-4000-8000-000000000003',selection_state='selected'`, pgx.QueryExecModeSimpleProtocol, track); err != nil {
		t.Fatal(err)
	}
	if err = s.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename<>'schema_migrations' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		out := map[string]string{}
		for _, table := range tables {
			var value string
			row := "to_jsonb(t)"
			// Migrate refreshes the existing grouping projection's completion
			// timestamp. Every other field, including identity and user revisions,
			// is compared exactly; the timestamp is not user-owned source data.
			if table == "catalog_grouping_state" {
				row += "-'completed_at'"
			}
			query := `SELECT coalesce(jsonb_agg(` + row + ` ORDER BY ` + row + `::text),'[]'::jsonb)::text FROM ` + pgx.Identifier{table}.Sanitize() + ` t`
			if e := s.pool.QueryRow(ctx, query).Scan(&value); e != nil {
				t.Fatal(e)
			}
			out[table] = value
		}
		return out
	}
	before := snapshot()
	// Fail after DDL, at the ledger write. The migration transaction must leave
	// neither the new tables nor a partially advanced schema version behind.
	if _, err = s.pool.Exec(ctx, `CREATE FUNCTION m2_migration_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.version=9 THEN RAISE EXCEPTION 'injected migration failure';END IF;RETURN NEW;END $$;CREATE TRIGGER m2_migration_fault BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION m2_migration_fault()`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("faulted migration succeeded")
	}
	var version int
	var exists bool
	if err = s.pool.QueryRow(ctx, `SELECT max(version),to_regclass('queue_playback_context') IS NOT NULL FROM schema_migrations`).Scan(&version, &exists); err != nil || version != 8 || exists {
		t.Fatal("partial migration", version, exists, err)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("failed upgrade changed existing rows")
	}
	if _, err = s.pool.Exec(ctx, `DROP TRIGGER m2_migration_fault ON schema_migrations;DROP FUNCTION m2_migration_fault()`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	if !reflect.DeepEqual(before, after) {
		for table, value := range before {
			if value != after[table] {
				t.Errorf("upgrade changed %s: before=%s after=%s", table, value, after[table])
			}
		}
		t.Fatal("upgrade changed existing catalog/user rows")
	}
	if err = s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `DROP INDEX queue_context_tracks_pending_idx`); err != nil {
		t.Fatal(err)
	}
	if err = s.Ready(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatal("ledger concealed missing context index", err)
	}
	if _, err = s.pool.Exec(ctx, `CREATE INDEX queue_context_tracks_pending_idx ON queue_context_tracks(play_rank) WHERE NOT seen AND NOT excluded AND NOT failed`); err != nil {
		t.Fatal(err)
	}
	if err = s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("Populated v8 to v9: exact rows preserved across %d existing tables; late migration rollback and schema drift verified", len(tables))
}
