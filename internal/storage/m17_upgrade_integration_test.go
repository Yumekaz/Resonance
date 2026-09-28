//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Each fixture is populated using only columns present at its historical
// version. Migrate, including its grouping backfill, is the production path.
func TestM17PopulatedHistoricalUpgradeMatrix(t *testing.T) {
	for _, version := range []int{2, 4, 5, 6, 7} {
		t.Run(fmt.Sprintf("v%d_to_v8", version), func(t *testing.T) {
			s, _ := isolatedStore(t)
			ctx := context.Background()
			if err := s.migrateTo(ctx, version); err != nil {
				t.Fatal(err)
			}
			const root = "00000000-0000-4000-8000-000000000017"
			const run = "00000000-0000-4000-8000-000000000018"
			if _, err := s.pool.Exec(ctx, `INSERT INTO tracks(id,title) VALUES('trk_00000000000000000000000000000017','Historical 夜');
    INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES('obj_17','trk_00000000000000000000000000000017',decode(repeat('17',32),'hex'),'mp3',100);
    INSERT INTO media_locations(id,media_object_id,local_path) VALUES('loc_17','obj_17','C:\m17-private-canary\Artist\Album\track.mp3')`); err != nil {
				t.Fatal(err)
			}
			if version >= 4 {
				if _, err := s.pool.Exec(ctx, `INSERT INTO library_roots(id,name,canonical_path,path_key) VALUES($1,'Historical','C:\m17-private-canary','m17-private-canary');
     UPDATE tracks SET artist_credit='Historical Artist',album_title='Historical Album',album_artist_credit='Historical Artist',release_year=2024;
     UPDATE media_locations SET root_id=$1,relative_path='Artist/Album/track.mp3';
     INSERT INTO scan_runs(id,root_id,status,finished_at) VALUES($2,$1,'succeeded',now())`, pgx.QueryExecModeSimpleProtocol, root, run); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 5 {
				if _, err := s.pool.Exec(ctx, `UPDATE media_locations SET observed_size=100;
     UPDATE tracks SET metadata_source_location_id='loc_17';
     UPDATE scan_runs SET phase='finished'`); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.Ready(ctx); err != nil {
				t.Fatal(err)
			}
			var title, path string
			var bytes int64
			if err := s.pool.QueryRow(ctx, `SELECT t.title,ml.local_path,mo.byte_length FROM tracks t JOIN media_objects mo ON mo.track_id=t.id JOIN media_locations ml ON ml.media_object_id=mo.id WHERE ml.id='loc_17'`).Scan(&title, &path, &bytes); err != nil {
				t.Fatal(err)
			}
			if title != "Historical 夜" || path != `C:\m17-private-canary\Artist\Album\track.mp3` || bytes != 100 {
				t.Fatal("identity/raw data changed")
			}
			var ledger int
			if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&ledger); err != nil || ledger != 8 {
				t.Fatal("ledger mismatch", err)
			}
			if version >= 4 {
				var state, phase string
				var authority bool
				if err := s.pool.QueryRow(ctx, "SELECT verification_state FROM library_roots WHERE id=$1", root).Scan(&state); err != nil || state != "unverified" {
					t.Fatal("historical root gained authority", state, err)
				}
				if err := s.pool.QueryRow(ctx, "SELECT phase,absence_reconciled FROM scan_runs WHERE id=$1", run).Scan(&phase, &authority); err != nil || phase != "finished" || authority {
					t.Fatal("historical scan invented authority", phase, authority, err)
				}
				var groups int
				if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM track_album_memberships WHERE track_id='trk_00000000000000000000000000000017'").Scan(&groups); err != nil || groups != 1 {
					t.Fatal("grouping missing", groups, err)
				}
			}
		})
	}
}

func m17TerminateBlockedBackend(t *testing.T, s *Store, key int) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		_ = s.pool.QueryRow(ctx, "SELECT pid FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid=$1 LIMIT 1", key).Scan(&pid)
		if pid != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("database interruption barrier not reached")
	}
	if _, err := s.pool.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
}

func TestM17MigrationBackendInterruptionRollbackAndRetry(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	lock, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	_, err = lock.Exec(ctx, "SELECT pg_advisory_lock(170918)")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Exec(ctx, "SELECT pg_advisory_unlock(170918)")
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION m17_ledger_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.version=8 THEN PERFORM pg_advisory_xact_lock(170918); END IF; RETURN NEW; END $$; CREATE TRIGGER m17_ledger_barrier BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION m17_ledger_barrier()`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Migrate(ctx) }()
	m17TerminateBlockedBackend(t, s, 170918)
	if err := <-done; err == nil {
		t.Fatal("terminated migration reported success")
	}
	var version int
	var column bool
	_ = s.pool.QueryRow(ctx, "SELECT max(version) FROM schema_migrations").Scan(&version)
	_ = s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='library_roots' AND column_name='root_identity_kind')").Scan(&column)
	if version != 7 || column {
		t.Fatal("partial migration DDL/ledger committed", version, column)
	}
	if _, err := s.pool.Exec(ctx, "DROP TRIGGER m17_ledger_barrier ON schema_migrations; DROP FUNCTION m17_ledger_barrier()"); err != nil {
		t.Fatal(err)
	}
	_, _ = lock.Exec(ctx, "SELECT pg_advisory_unlock(170918)")
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestM17BackfillBackendInterruptionAndRetry(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "Backfill", `C:\private-backfill`)
	if err != nil {
		t.Fatal(err)
	}
	track, loc := userFixtureTrack(t, s, root, 117)
	if _, err := s.pool.Exec(ctx, "UPDATE tracks SET artist_credit='Backfill Artist',album_title='Backfill Album',album_artist_credit='Backfill Artist' WHERE id=$1", track); err != nil {
		t.Fatal(err)
	}
	_ = loc
	if err := s.BackfillGrouping(ctx); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", track).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE catalog_grouping_state SET completed=false"); err != nil {
		t.Fatal(err)
	}
	lock, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	_, _ = lock.Exec(ctx, "SELECT pg_advisory_lock(170915)")
	defer lock.Exec(ctx, "SELECT pg_advisory_unlock(170915)")
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION m17_group_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.completed THEN PERFORM pg_advisory_xact_lock(170915); END IF; RETURN NEW; END $$; CREATE TRIGGER m17_group_barrier BEFORE UPDATE ON catalog_grouping_state FOR EACH ROW EXECUTE FUNCTION m17_group_barrier()`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.BackfillGrouping(ctx) }()
	m17TerminateBlockedBackend(t, s, 170915)
	if err := <-done; err == nil {
		t.Fatal("terminated backfill reported success")
	}
	if !errors.Is(s.Ready(ctx), ErrGroupingIncomplete) {
		t.Fatal("interrupted backfill reported ready")
	}
	var after string
	_ = s.pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", track).Scan(&after)
	if before != after {
		t.Fatal("backfill interruption changed IDs")
	}
	if _, err := s.pool.Exec(ctx, "DROP TRIGGER m17_group_barrier ON catalog_grouping_state; DROP FUNCTION m17_group_barrier()"); err != nil {
		t.Fatal(err)
	}
	_, _ = lock.Exec(ctx, "SELECT pg_advisory_unlock(170915)")
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.pool.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", track).Scan(&after)
	if before != after {
		t.Fatal("retry recycled grouping identity")
	}
}

func TestM17MigrationCLIAtTenThousandTracks(t *testing.T) {
	exe := os.Getenv("RESONANCE_M17_SERVER_EXE")
	if exe == "" {
		t.Skip("explicit production migration CLI scale campaign")
	}
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	const root = "00000000-0000-4000-8000-000000000017"
	if _, err := s.pool.Exec(ctx, `INSERT INTO library_roots(id,name,canonical_path,path_key) VALUES($1,'Historical scale','C:\m17-historical-scale','m17-historical-scale');
  INSERT INTO tracks(id,title,artist_credit,album_title,album_artist_credit,release_year) SELECT 'trk_'||lpad(to_hex(n),32,'0'),'Historical 夜 '||n,'Artist '||(n/100),'Album '||((n%100)/10),'Artist '||(n/100),2024 FROM generate_series(1,10000) n;
  INSERT INTO media_objects(id,track_id,sha256,format,byte_length) SELECT 'obj_'||lpad(to_hex(n),32,'0'),'trk_'||lpad(to_hex(n),32,'0'),decode(lpad(to_hex(n),64,'0'),'hex'),'mp3',4000 FROM generate_series(1,10000) n;
  INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,observed_size) SELECT 'loc_'||lpad(to_hex(n),32,'0'),'obj_'||lpad(to_hex(n),32,'0'),'C:\m17-historical-scale\'||n,$1,'Artist '||(n/100)||'/Album '||((n%100)/10)||'/'||n||'.mp3',4000 FROM generate_series(1,10000) n;
  UPDATE tracks t SET metadata_source_location_id=ml.id FROM media_locations ml JOIN media_objects mo ON mo.id=ml.media_object_id WHERE mo.track_id=t.id`, pgx.QueryExecModeSimpleProtocol, root); err != nil {
		t.Fatal(err)
	}
	digest := func() string {
		var value string
		if err := s.pool.QueryRow(ctx, `SELECT md5(string_agg(t.id||coalesce(t.title,'')||coalesce(t.artist_credit,'')||coalesce(t.album_title,'')||mo.id||encode(mo.sha256,'hex')||ml.id||ml.local_path||ml.relative_path,'' ORDER BY t.id)) FROM tracks t JOIN media_objects mo ON mo.track_id=t.id JOIN media_locations ml ON ml.media_object_id=mo.id`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := digest()
	dsn := os.Getenv("RESONANCE_TEST_DATABASE_URL") + " search_path=" + s.pool.Config().ConnConfig.RuntimeParams["search_path"]
	attempts := []map[string]any{}
	success := false
	for n := 1; n <= 2; n++ {
		started := time.Now()
		cmd := exec.Command(exe, "-migrate-only")
		cmd.Env = append(os.Environ(), "RESONANCE_DATABASE_URL="+dsn)
		body, err := cmd.CombinedOutput()
		attempts = append(attempts, map[string]any{"index": n, "started_utc": started.UTC(), "finished_utc": time.Now().UTC(), "duration_ms": float64(time.Since(started).Microseconds()) / 1000, "success": err == nil, "log": string(body)})
		if err == nil {
			success = true
			break
		}
	}
	versions := 0
	_ = s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&versions)
	var grouped int
	_ = s.pool.QueryRow(ctx, "SELECT count(*) FROM track_album_memberships").Scan(&grouped)
	result := map[string]any{"dataset": "10k synthetic faithful historical v5 catalog rows; grouping evidence in 100-track Artist folders and ten-track Albums; no playback claim", "attempts": attempts, "ledger_versions": versions, "album_memberships": grouped, "raw_identity_metadata_digest_unchanged": digest() == before, "ready": s.Ready(ctx) == nil}
	if output := os.Getenv("RESONANCE_M17_ATTEMPT_OUTPUT"); output != "" {
		body, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(filepath.Join(output, "migration-scale-result.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !success || s.Ready(ctx) != nil || versions != 8 || grouped != 10000 || digest() != before {
		t.Fatal("production migration CLI could not converge populated 10k upgrade after retry")
	}
}

func TestM17DamagedSchemaMatrix(t *testing.T) {
	for _, damage := range []struct{ name, sql string }{
		{"missing_receipt_index", "DROP INDEX mutation_receipts_expiry_idx"},
		{"missing_queue_singleton", "DELETE FROM active_queue"},
		{"wrong_ledger_checksum", "UPDATE schema_migrations SET checksum=decode(repeat('00',32),'hex') WHERE version=8"},
		{"missing_root_fence", "ALTER TABLE library_roots DROP COLUMN root_identity_kind"},
		{"incomplete_grouping", "UPDATE catalog_grouping_state SET completed=false"},
	} {
		t.Run(damage.name, func(t *testing.T) {
			s, _ := isolatedStore(t)
			ctx := context.Background()
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, damage.sql); err != nil {
				t.Fatal(err)
			}
			err := s.Ready(ctx)
			if !errors.Is(err, ErrSchemaMismatch) && !errors.Is(err, ErrGroupingIncomplete) {
				t.Fatal("damaged schema reported ready", err)
			}
		})
	}
}
