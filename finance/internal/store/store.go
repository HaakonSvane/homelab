// Package store is the SQLite persistence layer: connection setup, schema
// migrations, and queries. Effective dating itself (which version applies in a month)
// is decided by the ledger package; the store loads and saves the version history.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"time"

	_ "modernc.org/sqlite"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

//go:embed migrations
var migrationsDir embed.FS

// Migrations is the embedded set of schema migrations applied by Open.
var Migrations, _ = fs.Sub(migrationsDir, "migrations")

var (
	// ErrNotFound is returned when a looked-up row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrInvalid wraps every validation failure on write. The web layer validates
	// forms first and shows Norwegian messages; this is the backstop.
	ErrInvalid = errors.New("store: invalid")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Store wraps the database handle.
type Store struct {
	DB *sql.DB
	// Now is the clock used for timestamps and expiry; tests replace it.
	Now func() time.Time
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
	return &Store{DB: db, Now: time.Now}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// Ping checks that the database answers a query.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.DB.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

// write runs fn in a write transaction (BEGIN IMMEDIATE) and commits if it succeeds.
func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// read runs fn in a read-only transaction (plain deferred BEGIN), so multi-query
// loads see one consistent snapshot without taking the write lock.
func (s *Store) read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) timestamp() string { return formatTime(s.Now()) }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// nullUser maps the zero UserID (felleskonto / no owner) to SQL NULL.
func nullUser(id ledger.UserID) any {
	if id == 0 {
		return nil
	}
	return int64(id)
}

// nullMonth maps the zero Month (no end) to SQL NULL.
func nullMonth(m month.Month) any {
	if m.IsZero() {
		return nil
	}
	return m.String()
}

func scanNullMonth(ns sql.NullString) (month.Month, error) {
	if !ns.Valid {
		return 0, nil
	}
	return month.Parse(ns.String)
}
