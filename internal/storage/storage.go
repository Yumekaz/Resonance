package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrSchemaMismatch = errors.New("database schema is incompatible; run migrations or restore a compatible backup")

type Store struct{ pool *pgxpool.Pool }

// Open configures a small pool; callers must Ping and ValidateSchema before use.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if databaseURL == "" {
		return nil, errors.New("database URL is required")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.Tracer = QueryWorkTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("database pool initialization failed")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("database unavailable: %w", err)
	}
	return nil
}

func (s *Store) Ready(ctx context.Context) error {
	if err := s.Ping(ctx); err != nil {
		return err
	}
	if err := s.ValidateSchema(ctx); err != nil {
		return err
	}
	return s.GroupingReady(ctx)
}
