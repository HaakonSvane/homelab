package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return testNow }
	t.Cleanup(func() { s.Close() })
	return s
}

// The database itself rejects bad rows, independently of the Go validation, so a
// bug or a manual sqlite3 session cannot store nonsense.
func TestSchemaConstraints(t *testing.T) {
	s := newStore(t)
	const ts = "'2026-11-19T12:00:00Z'"
	mustExec := func(q string) {
		t.Helper()
		if _, err := s.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO users (id, name, display_name, created_at) VALUES (1, 'kari', 'Kari', ` + ts + `)`)
	mustExec(`INSERT INTO expenses (id, name, kind, created_by, created_at) VALUES (1, 'Husleie', 'shared', 1, ` + ts + `)`)
	mustExec(`INSERT INTO expense_versions (id, expense_id, valid_from, amount_ore, created_by, created_at) VALUES (1, 1, '2026-01', 100, 1, ` + ts + `)`)

	bad := map[string]string{
		"income for unknown user (FK)": `INSERT INTO incomes (user_id, valid_from, amount_ore, created_by, created_at) VALUES (99, '2026-01', 1, 1, ` + ts + `)`,
		"negative income":              `INSERT INTO incomes (user_id, valid_from, amount_ore, created_by, created_at) VALUES (1, '2026-01', -1, 1, ` + ts + `)`,
		"month 13":                     `INSERT INTO incomes (user_id, valid_from, amount_ore, created_by, created_at) VALUES (1, '2026-13', 1, 1, ` + ts + `)`,
		"month 00":                     `INSERT INTO incomes (user_id, valid_from, amount_ore, created_by, created_at) VALUES (1, '2026-00', 1, 1, ` + ts + `)`,
		"short month":                  `INSERT INTO incomes (user_id, valid_from, amount_ore, created_by, created_at) VALUES (1, '2026-1', 1, 1, ` + ts + `)`,
		"private without owner":        `INSERT INTO expenses (name, kind, created_by, created_at) VALUES ('x', 'private', 1, ` + ts + `)`,
		"shared with owner":            `INSERT INTO expenses (name, kind, owner_id, created_by, created_at) VALUES ('x', 'shared', 1, 1, ` + ts + `)`,
		"unknown kind":                 `INSERT INTO expenses (name, kind, created_by, created_at) VALUES ('x', 'felles', 1, ` + ts + `)`,
		"bad end_month":                `INSERT INTO expenses (name, kind, end_month, created_by, created_at) VALUES ('x', 'shared', '2026', 1, ` + ts + `)`,
		"due mask 0":                   `INSERT INTO expense_versions (expense_id, valid_from, amount_ore, due_months, created_by, created_at) VALUES (1, '2026-02', 1, 0, 1, ` + ts + `)`,
		"due mask 4096":                `INSERT INTO expense_versions (expense_id, valid_from, amount_ore, due_months, created_by, created_at) VALUES (1, '2026-02', 1, 4096, 1, ` + ts + `)`,
		"negative amount":              `INSERT INTO expense_versions (expense_id, valid_from, amount_ore, created_by, created_at) VALUES (1, '2026-02', -1, 1, ` + ts + `)`,
		"duplicate version month":      `INSERT INTO expense_versions (expense_id, valid_from, amount_ore, created_by, created_at) VALUES (1, '2026-01', 1, 1, ` + ts + `)`,
		"payer unknown (FK)":           `INSERT INTO expense_versions (expense_id, valid_from, amount_ore, payer_id, created_by, created_at) VALUES (1, '2026-02', 1, 99, 1, ` + ts + `)`,
		"share over 100 %":             `INSERT INTO expense_version_shares (version_id, user_id, share_bp) VALUES (1, 1, 10001)`,
		"share unknown version (FK)":   `INSERT INTO expense_version_shares (version_id, user_id, share_bp) VALUES (99, 1, 5000)`,
		"duplicate user name":          `INSERT INTO users (name, display_name, created_at) VALUES ('kari', 'K', ` + ts + `)`,
	}
	for name, q := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := s.DB.Exec(q); err == nil {
				t.Fatalf("accepted: %s", q)
			}
		})
	}
}
