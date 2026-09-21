package storage

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const migrationLockKey int64 = 0x7265736f6e616e63

type migration struct {
	version int
	name    string
	sql     string
	hash    [32]byte
}

func migrations() ([]migration, error) {
	return loadMigrations(migrationFS)
}

func loadMigrations(source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, "migrations")
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil || version != len(result)+1 {
			return nil, fmt.Errorf("invalid migration ordering: %s", entry.Name())
		}
		body, err := fs.ReadFile(source, "migrations/"+entry.Name())
		if err != nil {
			return nil, err
		}
		result = append(result, migration{version, entry.Name(), string(body), sha256.Sum256(body)})
	}
	return result, nil
}

// Migrate applies pending SQL files one transaction at a time. A session lock
// serializes concurrent migrators; no migration is silently modified or skipped.
func (s *Store) Migrate(ctx context.Context) error { return s.migrateTo(ctx, 0) }

func (s *Store) migrateTo(ctx context.Context, target int) error {
	all, err := migrations()
	if err != nil {
		return err
	}
	if target == 0 {
		target = len(all)
	}
	if target < 1 || target > len(all) {
		return errors.New("invalid migration target")
	}
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return errors.New("database unavailable")
	}
	// Session locks must never return to a pool. Close this dedicated physical
	// session on every exit; PostgreSQL releases its advisory lock with it.
	conn := pooled.Hijack()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return errors.New("migration lock failed")
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version integer PRIMARY KEY,
		name text NOT NULL,
		checksum bytea NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return errors.New("migration ledger initialization failed")
	}
	applied, err := readApplied(ctx, conn)
	if err != nil {
		return err
	}
	if err := validateApplied(all, applied); err != nil {
		return err
	}
	for _, m := range all {
		if m.version > target {
			break
		}
		if _, ok := applied[m.version]; ok {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return errors.New("migration transaction failed")
		}
		if _, err = tx.Exec(ctx, m.sql, pgx.QueryExecModeSimpleProtocol); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version, name, checksum) VALUES($1, $2, $3)", m.version, m.name, m.hash[:])
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %d failed: %w", m.version, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("migration %d commit failed: %w", m.version, err)
		}
	}
	if target == len(all) {
		return validateSchemaContract(ctx, conn)
	}
	return nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}
type appliedMigration struct {
	name     string
	checksum []byte
}

func readApplied(ctx context.Context, q queryer) (map[int]appliedMigration, error) {
	rows, err := q.Query(ctx, "SELECT version, name, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, ErrSchemaMismatch
	}
	defer rows.Close()
	result := make(map[int]appliedMigration)
	for rows.Next() {
		var version int
		var item appliedMigration
		if err := rows.Scan(&version, &item.name, &item.checksum); err != nil {
			return nil, ErrSchemaMismatch
		}
		result[version] = item
	}
	if rows.Err() != nil {
		return nil, ErrSchemaMismatch
	}
	return result, nil
}

func validateApplied(all []migration, applied map[int]appliedMigration) error {
	for version, got := range applied {
		if version < 1 || version > len(all) {
			return ErrSchemaMismatch
		}
		want := all[version-1]
		if got.name != want.name || string(got.checksum) != string(want.hash[:]) {
			return ErrSchemaMismatch
		}
	}
	for version := 1; version <= len(applied); version++ {
		if _, ok := applied[version]; !ok {
			return ErrSchemaMismatch
		}
	}
	return nil
}

func (s *Store) ValidateSchema(ctx context.Context) error {
	all, err := migrations()
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ErrSchemaMismatch
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock_shared($1)", migrationLockKey).Scan(&locked); err != nil || !locked {
		return ErrSchemaMismatch
	}
	applied, err := readApplied(ctx, tx)
	if err != nil {
		return err
	}
	if err := validateApplied(all, applied); err != nil {
		return err
	}
	if len(applied) != len(all) {
		return ErrSchemaMismatch
	}
	if err := validateSchemaContract(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrSchemaMismatch
	}
	return nil
}
