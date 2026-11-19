package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func openRaw(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "test.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func versions(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestMigrateAppliesInOrderAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	fsys := fstest.MapFS{
		// Listed out of order on purpose; 0002 depends on 0001.
		"0002_add_b.sql": {Data: []byte(`ALTER TABLE a ADD COLUMN b TEXT; INSERT INTO a (id, b) VALUES (1, 'x');`)},
		"0001_create_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER PRIMARY KEY);`)},
		"README.md":         {Data: []byte("ignored")},
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, db, fsys); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if got := versions(t, db); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("versions = %v, want [1 2]", got)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM a`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows in a = %d (err %v), want 1 (second run must not re-apply)", n, err)
	}
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	fsys := fstest.MapFS{
		"0001_ok.sql":     {Data: []byte(`CREATE TABLE a (id INTEGER PRIMARY KEY);`)},
		"0002_broken.sql": {Data: []byte(`CREATE TABLE b (id INTEGER); THIS IS NOT SQL;`)},
	}
	err := Migrate(ctx, db, fsys)
	if err == nil || !strings.Contains(err.Error(), "0002_broken.sql") {
		t.Fatalf("err = %v, want failure naming 0002_broken.sql", err)
	}
	if got := versions(t, db); len(got) != 1 || got[0] != 1 {
		t.Fatalf("versions = %v, want [1]", got)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'b'`).Scan(&n)
	if n != 0 {
		t.Fatal("table b exists: failed migration was not rolled back")
	}
}

func TestMigrateRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	cases := map[string]fstest.MapFS{
		"bad name": {"1_x.sql": {Data: []byte(`SELECT 1;`)}},
		"duplicate version": {
			"0001_a.sql": {Data: []byte(`SELECT 1;`)},
			"0001_b.sql": {Data: []byte(`SELECT 1;`)},
		},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Migrate(ctx, openRaw(t), fsys); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestMigrateRefusesUnknownAppliedVersion(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	newer := fstest.MapFS{
		"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)},
		"0002_b.sql": {Data: []byte(`CREATE TABLE b (id INTEGER);`)},
	}
	if err := Migrate(ctx, db, newer); err != nil {
		t.Fatal(err)
	}
	older := fstest.MapFS{"0001_a.sql": newer["0001_a.sql"]}
	if err := Migrate(ctx, db, older); err == nil || !strings.Contains(err.Error(), "0002") {
		t.Fatalf("err = %v, want refusal mentioning 0002", err)
	}
}

func TestOpenSetsPragmas(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mode string
	var fk int
	s.DB.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	s.DB.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if mode != "wal" || fk != 1 {
		t.Fatalf("journal_mode=%q foreign_keys=%d, want wal/1", mode, fk)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}
