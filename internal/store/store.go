// Package store owns the PostgreSQL connection pool and the data-access layer
// built on top of it. It is written against the standard database/sql
// interface, so the concrete driver (registered by a blank import in main)
// can be sqapped withiout touching this package.
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pipeprobe/pipeprobe/internal/config"
)

// Store wraps the connection pool.
type Store struct {
	db *sql.DB
}

// Open configures the connection pool from config. It does NOT establish a
// connection: database/sql connects lazily, so a wrong host or a down database
// is not detected here. Call ping to actually verify reachability.
func Open(cfg config.DB) (*Store, error) {
	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		// Only fails on an unknown driver or an unpareable DSN.
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	return &Store{db: db}, nil
}

// Ping verifies the database is reachable, bounded by ctx. This is the actual
// availability check - it forces a real connection from the pool.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	return nil
}

// DB exposes the underlying pool for query layers built on top of the store.
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the pool. Safe to call via defer in main.
func (s *Store) Close() error { return s.db.Close() }
