// Package store is the SQLite persistence layer: connection setup, schema
// migrations, and queries.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"

	_ "modernc.org/sqlite"
)

//go:embed migrations
var migrationsDir embed.FS

// Migrations is the embedded set of schema migrations applied by Open.
var Migrations, _ = fs.Sub(migrationsDir, "migrations")

// Store wraps the database handle.
type Store struct {
	DB *sql.DB
}

// Open opens (creating if needed) the SQLite database at path with WAL, foreign keys
// and a busy timeout on every connection, then applies pending migrations.
// BEGIN IMMEDIATE (_txlock) makes write transactions take the write lock up front,
// so the CLI and the server can share the file without SQLITE_BUSY upgrade failures.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := Migrate(ctx, db, Migrations); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// Ping checks that the database answers a query.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.DB.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}
