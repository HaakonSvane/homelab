package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
)

// Income is one version of a user's net monthly income, with who recorded it.
type Income struct {
	ID   int64
	User ledger.UserID
	ledger.IncomeVersion
	Note      string
	CreatedBy ledger.UserID
	CreatedAt time.Time
}

// SetIncome records user's net monthly income from v.ValidFrom. A version already
// starting in that same month is replaced (a correction); other months are untouched.
func (s *Store) SetIncome(ctx context.Context, user ledger.UserID, v ledger.IncomeVersion, note string, by ledger.UserID) error {
	note = strings.TrimSpace(note)
	switch {
	case v.ValidFrom.IsZero():
		return invalid("income needs a start month")
	case v.Amount < 0 || v.Amount > money.Max:
		return invalid("income %d øre out of range", v.Amount)
	case v.Payday < 0 || v.Payday > 31:
		return invalid("payday %d out of range", v.Payday)
	case utf8.RuneCountInString(note) > 200:
		return invalid("note longer than 200 characters")
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := requireUsers(ctx, tx, user, by); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO incomes (user_id, valid_from, amount_ore, payday, note, created_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id, valid_from) DO UPDATE SET
				amount_ore = excluded.amount_ore,
				payday     = excluded.payday,
				note       = excluded.note,
				created_by = excluded.created_by,
				created_at = excluded.created_at`,
			int64(user), v.ValidFrom, int64(v.Amount), nullDay(v.Payday), note, int64(by), s.timestamp())
		return err
	})
}

// Incomes returns every income version, by user and then by start month.
func (s *Store) Incomes(ctx context.Context) ([]Income, error) {
	return incomes(ctx, s.DB)
}

func incomes(ctx context.Context, q querier) ([]Income, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, user_id, valid_from, amount_ore, payday, note, created_by, created_at
		FROM incomes ORDER BY user_id, valid_from`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Income
	for rows.Next() {
		var in Income
		var created string
		var payday sql.NullInt64
		if err := rows.Scan(&in.ID, &in.User, &in.ValidFrom, &in.Amount, &payday, &in.Note, &in.CreatedBy, &created); err != nil {
			return nil, err
		}
		in.Payday = int(payday.Int64)
		if in.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// nullDay maps payday 0 (not set) to SQL NULL.
func nullDay(day int) any {
	if day == 0 {
		return nil
	}
	return day
}

// requireUsers returns ErrInvalid unless every id is an existing user.
func requireUsers(ctx context.Context, q querier, ids ...ledger.UserID) error {
	known, err := userIDs(ctx, q)
	if err != nil {
		return err
	}
	for _, id := range ids {
		found := false
		for _, k := range known {
			found = found || k == id
		}
		if !found {
			return invalid("unknown user %d", id)
		}
	}
	return nil
}
