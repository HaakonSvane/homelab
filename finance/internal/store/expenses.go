package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// Expense is an expense with its full version history and who recorded what.
type Expense struct {
	ledger.Expense // Versions are sorted by ValidFrom
	CreatedBy      ledger.UserID
	CreatedAt      time.Time
	VersionMeta    []VersionMeta // VersionMeta[i] describes Versions[i]
}

// VersionMeta is the bookkeeping for one expense version.
type VersionMeta struct {
	ID        int64
	CreatedBy ledger.UserID
	CreatedAt time.Time
}

// NewExpense is a new expense together with its first version.
type NewExpense struct {
	Name, Category string
	Kind           ledger.Kind
	Owner          ledger.UserID // private only
	First          ledger.ExpenseVersion
}

func cleanLabels(name, category string) (string, string, error) {
	name, category = strings.TrimSpace(name), strings.TrimSpace(category)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", "", invalid("expense name must be 1–100 characters")
	}
	if utf8.RuneCountInString(category) > 50 {
		return "", "", invalid("category longer than 50 characters")
	}
	return name, category, nil
}

// CreateExpense adds an expense and its first version. Kind and owner are fixed for
// the expense's lifetime: changing them would change the meaning of its history.
func (s *Store) CreateExpense(ctx context.Context, e NewExpense, by ledger.UserID) (int64, error) {
	name, category, err := cleanLabels(e.Name, e.Category)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.write(ctx, func(tx *sql.Tx) error {
		ids, err := userIDs(ctx, tx)
		if err != nil {
			return err
		}
		candidate := ledger.Expense{Name: name, Category: category, Kind: e.Kind, Owner: e.Owner,
			Versions: []ledger.ExpenseVersion{e.First}}
		if err := ledger.ValidateExpense(candidate, ids); err != nil {
			return invalid("%v", err)
		}
		if err := requireUsers(ctx, tx, by); err != nil {
			return err
		}
		now := s.timestamp()
		res, err := tx.ExecContext(ctx, `
			INSERT INTO expenses (name, category, kind, owner_id, created_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			name, category, string(e.Kind), nullUser(e.Owner), int64(by), now)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return upsertVersion(ctx, tx, id, e.First, by, now)
	})
	return id, err
}

// SaveExpenseVersion records new terms for an expense from v.ValidFrom. A version
// already starting in that same month is replaced, shares included (a correction);
// versions for other months are never touched.
func (s *Store) SaveExpenseVersion(ctx context.Context, id int64, v ledger.ExpenseVersion, by ledger.UserID) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, err := loadExpense(ctx, tx, id)
		if err != nil {
			return err
		}
		if !e.End.IsZero() && v.ValidFrom > e.End {
			return invalid("version from %v starts after the expense ended in %v", v.ValidFrom, e.End)
		}
		candidate := e.Expense
		candidate.Versions = append([]ledger.ExpenseVersion(nil), e.Versions...)
		replaced := false
		for i := range candidate.Versions {
			if candidate.Versions[i].ValidFrom == v.ValidFrom {
				candidate.Versions[i], replaced = v, true
			}
		}
		if !replaced {
			candidate.Versions = append(candidate.Versions, v)
		}
		ids, err := userIDs(ctx, tx)
		if err != nil {
			return err
		}
		if err := ledger.ValidateExpense(candidate, ids); err != nil {
			return invalid("%v", err)
		}
		if err := requireUsers(ctx, tx, by); err != nil {
			return err
		}
		return upsertVersion(ctx, tx, id, v, by, s.timestamp())
	})
}

func upsertVersion(ctx context.Context, tx *sql.Tx, expenseID int64, v ledger.ExpenseVersion, by ledger.UserID, now string) error {
	var versionID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO expense_versions (expense_id, valid_from, amount_ore, due_months, payer_id, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (expense_id, valid_from) DO UPDATE SET
			amount_ore = excluded.amount_ore,
			due_months = excluded.due_months,
			payer_id   = excluded.payer_id,
			created_by = excluded.created_by,
			created_at = excluded.created_at
		RETURNING id`,
		expenseID, v.ValidFrom, int64(v.Amount), int64(v.Due), nullUser(v.Payer), int64(by), now,
	).Scan(&versionID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM expense_version_shares WHERE version_id = ?`, versionID); err != nil {
		return err
	}
	for _, sh := range v.Shares {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO expense_version_shares (version_id, user_id, share_bp) VALUES (?, ?, ?)`,
			versionID, int64(sh.User), sh.BP); err != nil {
			return err
		}
	}
	return nil
}

// RenameExpense changes an expense's name and category. These are labels, not
// terms, so they are not versioned.
func (s *Store) RenameExpense(ctx context.Context, id int64, name, category string) error {
	name, category, err := cleanLabels(name, category)
	if err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE expenses SET name = ?, category = ? WHERE id = ?`, name, category, id)
		return requireOneRow(res, err)
	})
}

// SetExpenseEnd sets the last month (inclusive) an expense applies; the zero Month
// clears it. The end may not come before the first version or the latest version.
func (s *Store) SetExpenseEnd(ctx context.Context, id int64, end month.Month) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, err := loadExpense(ctx, tx, id)
		if err != nil {
			return err
		}
		if !end.IsZero() {
			if last := e.Versions[len(e.Versions)-1].ValidFrom; end < last {
				return invalid("end %v is before the latest version (%v)", end, last)
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE expenses SET end_month = ? WHERE id = ?`, nullMonth(end), id)
		return requireOneRow(res, err)
	})
}

// DeleteExpense removes an expense with its whole version history (the versions and
// their shares go with it through ON DELETE CASCADE). This is the one deliberate
// exception to "history is never rewritten": the web UI asks for explicit
// confirmation, and ending an expense (SetExpenseEnd) is the normal way to stop one.
func (s *Store) DeleteExpense(ctx context.Context, id int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM expenses WHERE id = ?`, id)
		return requireOneRow(res, err)
	})
}

func requireOneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Expenses returns every expense with its full history, by ID.
func (s *Store) Expenses(ctx context.Context) ([]Expense, error) {
	var out []Expense
	err := s.read(ctx, func(tx *sql.Tx) (err error) {
		out, err = loadExpenses(ctx, tx, 0)
		return err
	})
	return out, err
}

// Expense returns one expense with its full history.
func (s *Store) Expense(ctx context.Context, id int64) (Expense, error) {
	var out Expense
	err := s.read(ctx, func(tx *sql.Tx) (err error) {
		out, err = loadExpense(ctx, tx, id)
		return err
	})
	return out, err
}

func loadExpense(ctx context.Context, q querier, id int64) (Expense, error) {
	es, err := loadExpenses(ctx, q, id)
	if err != nil {
		return Expense{}, err
	}
	if len(es) == 0 {
		return Expense{}, ErrNotFound
	}
	return es[0], nil
}

// loadExpenses loads one expense (id > 0) or all of them (id == 0) in three queries.
func loadExpenses(ctx context.Context, q querier, id int64) ([]Expense, error) {
	filter, args := "", []any{}
	if id > 0 {
		filter, args = " WHERE id = ?", []any{id}
	}
	rows, err := q.QueryContext(ctx, `
		SELECT id, name, category, kind, owner_id, end_month, created_by, created_at
		FROM expenses`+filter+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	var out []Expense
	index := map[int64]int{}
	for rows.Next() {
		var e Expense
		var owner sql.NullInt64
		var end sql.NullString
		var kind, created string
		if err := rows.Scan(&e.ID, &e.Name, &e.Category, &kind, &owner, &end, &e.CreatedBy, &created); err != nil {
			rows.Close()
			return nil, err
		}
		e.Kind, e.Owner = ledger.Kind(kind), ledger.UserID(owner.Int64)
		if e.End, err = scanNullMonth(end); err == nil {
			e.CreatedAt, err = parseTime(created)
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
		index[e.ID] = len(out)
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	vfilter := ""
	if id > 0 {
		vfilter = " WHERE expense_id = ?"
	}
	rows, err = q.QueryContext(ctx, `
		SELECT id, expense_id, valid_from, amount_ore, due_months, payer_id, created_by, created_at
		FROM expense_versions`+vfilter+` ORDER BY expense_id, valid_from`, args...)
	if err != nil {
		return nil, err
	}
	type pos struct{ expense, version int }
	versionAt := map[int64]pos{}
	for rows.Next() {
		var v ledger.ExpenseVersion
		var meta VersionMeta
		var expenseID int64
		var payer sql.NullInt64
		var created string
		if err := rows.Scan(&meta.ID, &expenseID, &v.ValidFrom, &v.Amount, &v.Due, &payer, &meta.CreatedBy, &created); err != nil {
			rows.Close()
			return nil, err
		}
		v.Payer = ledger.UserID(payer.Int64)
		if meta.CreatedAt, err = parseTime(created); err != nil {
			rows.Close()
			return nil, err
		}
		i := index[expenseID]
		versionAt[meta.ID] = pos{i, len(out[i].Versions)}
		out[i].Versions = append(out[i].Versions, v)
		out[i].VersionMeta = append(out[i].VersionMeta, meta)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sfilter := ""
	if id > 0 {
		sfilter = " WHERE v.expense_id = ?"
	}
	rows, err = q.QueryContext(ctx, `
		SELECT s.version_id, s.user_id, s.share_bp
		FROM expense_version_shares s JOIN expense_versions v ON v.id = s.version_id`+sfilter+`
		ORDER BY s.version_id, s.user_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var versionID int64
		var sh ledger.Share
		if err := rows.Scan(&versionID, &sh.User, &sh.BP); err != nil {
			return nil, err
		}
		p, ok := versionAt[versionID]
		if !ok {
			return nil, errors.New("store: share for unknown expense version")
		}
		v := &out[p.expense].Versions[p.version]
		v.Shares = append(v.Shares, sh)
	}
	return out, rows.Err()
}

// LedgerInput loads everything the ledger needs, from one consistent snapshot.
func (s *Store) LedgerInput(ctx context.Context) (ledger.Input, error) {
	in := ledger.Input{Incomes: map[ledger.UserID][]ledger.IncomeVersion{}}
	err := s.read(ctx, func(tx *sql.Tx) error {
		var err error
		if in.Users, err = userIDs(ctx, tx); err != nil {
			return err
		}
		incs, err := incomes(ctx, tx)
		if err != nil {
			return err
		}
		for _, inc := range incs {
			in.Incomes[inc.User] = append(in.Incomes[inc.User], inc.IncomeVersion)
		}
		exps, err := loadExpenses(ctx, tx, 0)
		if err != nil {
			return err
		}
		for _, e := range exps {
			in.Expenses = append(in.Expenses, e.Expense)
		}
		return nil
	})
	return in, err
}
