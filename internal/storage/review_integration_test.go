//go:build integration

package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReviewSchemaDrift(t *testing.T) {
	for _, ddl := range []string{
		"ALTER TABLE tracks DROP COLUMN title",
		"ALTER TABLE media_objects DROP CONSTRAINT media_objects_hash_length",
		"DROP INDEX media_objects_track_id_idx",
		"ALTER TABLE media_locations ALTER COLUMN local_path DROP NOT NULL",
		"ALTER TABLE media_objects DROP CONSTRAINT media_objects_track_id_fkey",
		"ALTER TABLE scan_errors DROP CONSTRAINT scan_errors_code_check, ADD CONSTRAINT scan_errors_code_check CHECK (length(code) > 0)",
		"ALTER TABLE library_roots DROP CONSTRAINT library_roots_path_key_key, ADD CONSTRAINT library_roots_path_key_key UNIQUE (canonical_path)",
		"ALTER TABLE media_locations DROP COLUMN observed_mtime_ns",
		"ALTER TABLE media_locations DROP CONSTRAINT media_locations_native_identity_check",
		"ALTER TABLE scan_runs DROP CONSTRAINT scan_runs_counters_nonnegative",
		"ALTER TABLE scan_runs DROP CONSTRAINT scan_runs_root_id_id_key CASCADE",
		"ALTER TABLE library_roots DROP CONSTRAINT library_roots_last_successful_scan_fkey",
		"ALTER TABLE tracks DROP CONSTRAINT tracks_metadata_source_location_fkey",
		"DROP INDEX media_locations_active_root_relative_idx",
		"DROP INDEX media_locations_root_availability_idx",
		"DROP INDEX media_locations_native_lookup_idx",
	} {
		t.Run(ddl, func(t *testing.T) {
			s, _ := isolatedStore(t)
			ctx := context.Background()
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, ddl); err != nil {
				t.Fatal(err)
			}
			if err := s.Ready(ctx); !errors.Is(err, ErrSchemaMismatch) {
				t.Fatalf("damaged schema ready: %v", err)
			}
		})
	}
}

func TestReviewM12WrongSameNamedIndexIsRejected(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "DROP INDEX scan_errors_run_idx"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "CREATE INDEX scan_errors_run_idx ON scan_errors(code)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("wrong same-named index accepted: %v", err)
	}
}

func TestReviewCanceledLockWaitAndPoolCleanup(t *testing.T) {
	s, admin := isolatedStore(t)
	ctx := context.Background()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	if err := s.Migrate(deadline); err == nil {
		t.Fatal("lock wait ignored deadline")
	}
	cancel()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := s.Ready(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if s.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("validation leaked a pool connection")
	}
}

func TestReviewIdentityConstraints(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO tracks(id) VALUES ('track'); INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES ('object','track',decode(repeat('01',32),'hex'),'MP3',123)`); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO tracks(id) VALUES ('')`,
		`INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES ('bad','missing',decode(repeat('02',32),'hex'),'MP3',123)`,
		`INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES ('bad','track',decode('02','hex'),'MP3',123)`,
		`INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES ('bad','track',decode(repeat('02',32),'hex'),'MP3',-1)`,
		`INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES ('bad','track',decode(repeat('01',32),'hex'),'MP3',123)`,
		`INSERT INTO media_locations(id,media_object_id,local_path) VALUES ('bad','missing','private')`,
		`DELETE FROM tracks WHERE id='track'`,
	} {
		if _, err := s.pool.Exec(ctx, sql); err == nil {
			t.Fatalf("constraint missing: %s", sql)
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path) VALUES ('copy1','object','private/one'),('copy2','object','private/two')`); err != nil {
		t.Fatal("multiple physical copies rejected:", err)
	}
}

func TestReviewConcurrentMigrators(t *testing.T) {
	s, admin := isolatedStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.Migrate(ctx) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var available bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", migrationLockKey).Scan(&available); err != nil || !available {
		t.Fatalf("lock leaked: %v %v", available, err)
	}
	conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey)
}

func TestReviewReadinessDuringMigrationLock(t *testing.T) {
	s, admin := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey)
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := s.Ready(deadline); err == nil {
		t.Fatal("ready while migration lock held")
	}
}

func TestReviewFailureAfterFirstDDLIsAtomic(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "CREATE INDEX media_locations_media_object_id_idx ON media_locations(media_object_id)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("expected second-statement failure")
	}
	var exists bool
	s.pool.QueryRow(ctx, "SELECT to_regclass('media_objects_track_id_idx') IS NOT NULL").Scan(&exists)
	if exists {
		t.Fatal("first DDL was not rolled back")
	}
	var count int
	s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count)
	if count != 1 {
		t.Fatal("failed migration recorded")
	}
	if _, err := s.pool.Exec(ctx, "DROP INDEX media_locations_media_object_id_idx"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReviewUnknownAndGappedVersions(t *testing.T) {
	for _, sql := range []string{"UPDATE schema_migrations SET version=99 WHERE version=2", "DELETE FROM schema_migrations WHERE version=1", "UPDATE schema_migrations SET name='changed' WHERE version=1"} {
		t.Run(sql, func(t *testing.T) {
			s, _ := isolatedStore(t)
			ctx := context.Background()
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
			if err := s.Ready(ctx); !errors.Is(err, ErrSchemaMismatch) {
				t.Fatalf("accepted: %v", err)
			}
			if err := s.Migrate(ctx); !errors.Is(err, ErrSchemaMismatch) {
				t.Fatalf("migrated: %v", err)
			}
		})
	}
}
