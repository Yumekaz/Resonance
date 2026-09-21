//go:build integration

package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func isolatedStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("RESONANCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("RESONANCE_TEST_DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(random[:])
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{pool: pool}
	t.Cleanup(func() {
		store.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	return store, admin
}

func TestEmptyMigrationAndIdempotence(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 2 {
		t.Fatalf("migrations=%d error=%v", count, err)
	}
}

func TestPendingMigrationIsNotReady(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("pending migration appeared ready: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPopulatedMigrationPreservesIdentities(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	title := "夜の歌 ♫"
	if err := s.InsertTrack(ctx, Track{ID: "trk-1", Title: &title}); err != nil {
		t.Fatal(err)
	}
	var hash [32]byte
	hash[0] = 42
	if err := s.InsertMediaObject(ctx, MediaObject{ID: "obj-1", TrackID: "trk-1", SHA256: hash, Format: "flac", ByteLength: 1024}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertLocation(ctx, MediaLocation{ID: "loc-1", MediaObjectID: "obj-1", LocalPath: `C:\private\music\track.flac`}); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var gotTitle, gotPath string
	if err := s.pool.QueryRow(ctx, "SELECT title FROM tracks WHERE id='trk-1'").Scan(&gotTitle); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT local_path FROM media_locations WHERE id='loc-1'").Scan(&gotPath); err != nil {
		t.Fatal(err)
	}
	if gotTitle != title || gotPath != `C:\private\music\track.flac` {
		t.Fatalf("data changed: %q %q", gotTitle, gotPath)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM media_objects WHERE id='obj-1' AND sha256=$1", hash[:]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("media object lost: %d %v", count, err)
	}
}

func TestMigrationFailureIsAtomicAndRecoverable(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, "CREATE TABLE tracks(id text)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("expected migration conflict")
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial ledger: %d %v", count, err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT to_regclass('media_objects') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("partial schema: %v %v", exists, err)
	}
	if _, err := s.pool.Exec(ctx, "DROP TABLE tracks"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestIncompatibleSchemaRejected(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE schema_migrations SET checksum=$1 WHERE version=1", []byte("wrong")); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if err := s.Migrate(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("migration should reject changed file: %v", err)
	}
}

func TestMissingCoreTableIsNotReady(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "DROP TABLE media_locations"); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("missing table appeared ready: %v", err)
	}
}

func TestConnectionRecovery(t *testing.T) {
	s, admin := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var pid int32
	if err := s.pool.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := s.Ready(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pool did not reconnect")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestNoDatabaseURL(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("empty URL accepted")
	}
	if _, err := Open(context.Background(), "postgres://%gh"); err == nil {
		t.Fatal("invalid URL accepted")
	}
}
