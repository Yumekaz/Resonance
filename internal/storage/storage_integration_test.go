//go:build integration

package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
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
	cfg.ConnConfig.Tracer = QueryWorkTracer()
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
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 9 {
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

func TestM12MigrationOnPopulatedCore(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO tracks(id,title) VALUES('legacy-track','Existing');
		INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES('legacy-object','legacy-track',decode(repeat('ba',32),'hex'),'wav',100);
		INSERT INTO media_locations(id,media_object_id,local_path) VALUES('legacy-location','legacy-object','fixture://existing.wav')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM media_locations WHERE id='legacy-location' AND root_id IS NULL").Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy row lost: %d %v", count, err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 9 {
		t.Fatalf("migration count: %d %v", count, err)
	}
}

func TestM16PopulatedSevenToEightMigrationRequiresRootVerification(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	rootID := "00000000-0000-4000-8000-000000000081"
	if _, err := s.pool.Exec(ctx, `INSERT INTO library_roots(id,name,canonical_path,path_key) VALUES($1,'existing','private/music','private/music')`, rootID); err != nil {
		t.Fatal(err)
	}
	title := "Preserved"
	if err := s.InsertTrack(ctx, Track{ID: "m16-track", Title: &title}); err != nil {
		t.Fatal(err)
	}
	var hash [32]byte
	hash[0] = 16
	if err := s.InsertMediaObject(ctx, MediaObject{ID: "m16-object", TrackID: "m16-track", SHA256: hash, Format: "wav", ByteLength: 24}); err != nil {
		t.Fatal(err)
	}
	relative := "song.wav"
	if _, err := s.pool.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path) VALUES('m16-location','m16-object',$1,$2,$3)`, `private\music\song.wav`, rootID, relative); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	var verifiedAt *time.Time
	var identityKind *string
	if err := s.pool.QueryRow(ctx, `SELECT verification_state,verified_at,root_identity_kind FROM library_roots WHERE id=$1`, rootID).Scan(&state, &verifiedAt, &identityKind); err != nil {
		t.Fatal(err)
	}
	if state != "unverified" || verifiedAt != nil || identityKind != nil {
		t.Fatalf("migration fabricated root verification evidence: state=%q verified_at=%v identity=%v", state, verifiedAt, identityKind)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM media_locations WHERE id='m16-location' AND availability='available'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("populated catalog row changed during 0008: %d %v", count, err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatalf("ready rejected complete 0008 schema: %v", err)
	}
}

func TestM16RootVerificationEnableMismatchAndExplicitRebind(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := s.AddRoot(ctx, "verified root", "private/root")
	if err != nil || root.VerificationState != "unverified" {
		t.Fatalf("new root without evidence was treated as verified: %#v %v", root, err)
	}
	if _, err := s.VerifyRootIdentity(ctx, root.ID, NativeIdentity{}); !errors.Is(err, ErrRootIdentityUnavailable) {
		t.Fatalf("empty identity evidence was accepted: %v", err)
	}
	first := NativeIdentity{Kind: "test", Scope: "volume", ID: []byte{1}, BirthToken: []byte{9}}
	verified, err := s.VerifyRootIdentity(ctx, root.ID, first)
	if err != nil || verified.VerificationState != "verified" {
		t.Fatalf("host verification failed: %#v %v", verified, err)
	}
	if err := s.DisableRoot(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	second := NativeIdentity{Kind: "test", Scope: "another-volume", ID: []byte{2}, BirthToken: []byte{8}}
	if _, err := s.EnableRoot(ctx, root.ID, second); !errors.Is(err, ErrRootIdentityMismatch) {
		t.Fatalf("different root object was enabled: %v", err)
	}
	state, err := s.GetRoot(ctx, root.ID)
	if err != nil || state.VerificationState != "quarantined" || state.Enabled {
		t.Fatalf("root mismatch did not quarantine: %#v %v", state, err)
	}
	if _, err := s.EnableRoot(ctx, root.ID, first); !errors.Is(err, ErrRootIdentityMismatch) {
		t.Fatalf("quarantined root was silently re-enabled: %v", err)
	}
	if _, err := s.VerifyRootIdentity(ctx, root.ID, second); err != nil {
		t.Fatal(err)
	}
	enabled, err := s.EnableRoot(ctx, root.ID, second)
	if err != nil || !enabled.Enabled || enabled.VerificationState != "verified" {
		t.Fatalf("explicit rebind did not restore root: %#v %v", enabled, err)
	}
	public, err := s.ListRoots(ctx)
	if err != nil || len(public) != 1 {
		t.Fatalf("public root list failed: %#v %v", public, err)
	}
	encoded, err := json.Marshal(public[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private/root") || strings.Contains(string(encoded), "root_identity") || strings.Contains(string(encoded), "another-volume") {
		t.Fatalf("root listing disclosed private path or identity: %s", encoded)
	}
}

func TestM16EmptySevenToEightMigration(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var roots, versions int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM library_roots").Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if roots != 0 || versions != 9 {
		t.Fatalf("empty 0007 to 0008 migration: roots=%d versions=%d", roots, versions)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestM16MigrationRollbackAndRetryAfterPartialDDL(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "CREATE INDEX library_roots_enabled_verification_idx ON library_roots(id)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("expected 0008 index conflict")
	}
	var columns, constraints, version int
	if err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='library_roots' AND column_name IN ('root_identity_kind','verification_state')),
		(SELECT count(*) FROM pg_constraint WHERE conname='library_roots_identity_kind_scope_check' AND conrelid='library_roots'::regclass),
		(SELECT count(*) FROM schema_migrations WHERE version=8)`).Scan(&columns, &constraints, &version); err != nil {
		t.Fatal(err)
	}
	if columns != 0 || constraints != 0 || version != 0 {
		t.Fatalf("partial 0008 DDL survived failure: columns=%d constraints=%d version=%d", columns, constraints, version)
	}
	if _, err := s.pool.Exec(ctx, "DROP INDEX library_roots_enabled_verification_idx"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("0008 retry failed: %v", err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestM16SchemaContractRejectsRootFenceDrift(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "ALTER TABLE library_roots DROP CONSTRAINT library_roots_verification_evidence_check"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Ready(ctx), ErrSchemaMismatch) {
		t.Fatal("readiness accepted missing root-verification constraint")
	}
}

func TestM12GroupingCorrectionPreservesObservedMetadata(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO artists(id,display_name) VALUES('art_old','Shared Name');
		INSERT INTO albums(id,title,album_artist_credit) VALUES('alb_old','Observed Album','Album Credit');
		INSERT INTO tracks(id,title,artist_id,album_id,artist_credit,album_artist_credit) VALUES('trk_old','Song','art_old','alb_old','Shared Name','Album Credit')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var title, artist, albumArtist string
	if err := s.pool.QueryRow(ctx, "SELECT album_title,artist_credit,album_artist_credit FROM tracks WHERE id='trk_old'").Scan(&title, &artist, &albumArtist); err != nil {
		t.Fatal(err)
	}
	if title != "Observed Album" || artist != "Shared Name" || albumArtist != "Album Credit" {
		t.Fatalf("metadata not preserved: %q %q %q", title, artist, albumArtist)
	}
	var oldTables int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('artists'),to_regclass('albums'))").Scan(&oldTables); err != nil || oldTables != 0 {
		t.Fatalf("premature grouping tables remain: %d %v", oldTables, err)
	}
}

func TestM13MigrationBackfillsObservationAndMetadataSource(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO library_roots(id,name,canonical_path,path_key) VALUES
		('00000000-0000-4000-8000-000000000001','root','private/root','private/root');
		INSERT INTO scan_runs(id,root_id,status) VALUES
		('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','succeeded');
		INSERT INTO tracks(id,title) VALUES ('trk_m13','existing');
		INSERT INTO media_objects(id,track_id,sha256,format,byte_length) VALUES
		('obj_m13','trk_m13',decode(repeat('a1',32),'hex'),'wav',1234);
		INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,created_at) VALUES
		('loc_later','obj_m13','private/later','00000000-0000-4000-8000-000000000001','later.wav','2026-09-22T00:00:00Z'),
		('loc_earlier','obj_m13','private/earlier','00000000-0000-4000-8000-000000000001','earlier.wav','2026-09-21T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var available, source string
	var reason *string
	var size int64
	var mtime *int64
	if err := s.pool.QueryRow(ctx, `SELECT availability,unavailable_reason,observed_size,observed_mtime_ns FROM media_locations WHERE id='loc_later'`).Scan(&available, &reason, &size, &mtime); err != nil {
		t.Fatal(err)
	}
	if available != "available" || reason != nil || size != 1234 || mtime != nil {
		t.Fatalf("location backfill: availability=%q reason=%v size=%d mtime=%v", available, reason, size, mtime)
	}
	if err := s.pool.QueryRow(ctx, "SELECT metadata_source_location_id FROM tracks WHERE id='trk_m13'").Scan(&source); err != nil || source != "loc_earlier" {
		t.Fatalf("metadata source backfill: %q %v", source, err)
	}
	var status, phase string
	var traversalComplete, observationsApplied, absenceReconciled bool
	if err := s.pool.QueryRow(ctx, `SELECT status,phase,traversal_complete,observations_applied,absence_reconciled FROM scan_runs WHERE id='00000000-0000-4000-8000-000000000002'`).Scan(&status, &phase, &traversalComplete, &observationsApplied, &absenceReconciled); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || phase != "finished" || traversalComplete || observationsApplied || absenceReconciled {
		t.Fatalf("M1.2 terminal scan history was treated as an M1.3A run: %s %s %v %v %v", status, phase, traversalComplete, observationsApplied, absenceReconciled)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestM13MigrationFailureIsAtomicAndRecoverable(t *testing.T) {
	s, _ := isolatedStore(t)
	ctx := context.Background()
	if err := s.migrateTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "CREATE INDEX media_locations_native_lookup_idx ON media_locations(root_id, relative_path)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("expected M1.3 migration failure")
	}
	var oldIndex, newColumn bool
	if err := s.pool.QueryRow(ctx, "SELECT to_regclass('media_locations_root_relative_idx') IS NOT NULL, EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='media_locations' AND column_name='availability')").Scan(&oldIndex, &newColumn); err != nil {
		t.Fatal(err)
	}
	if !oldIndex || newColumn {
		t.Fatalf("failed migration was partially applied: old_index=%v new_column=%v", oldIndex, newColumn)
	}
	if _, err := s.pool.Exec(ctx, "DROP INDEX media_locations_native_lookup_idx"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
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
	if _, err := s.pool.Exec(ctx, "DROP TABLE media_locations CASCADE"); err != nil {
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
