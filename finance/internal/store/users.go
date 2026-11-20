package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
)

// MaxUsers is how many people the app is built for (the split UI assumes two).
const MaxUsers = 2

var userName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// User is a person who can log in.
type User struct {
	ID          ledger.UserID
	Name        string // login handle used by the CLI, e.g. "kari"
	DisplayName string // shown in the UI, e.g. "Kari"
	CreatedAt   time.Time
}

// cleanDisplayName trims whitespace and quote marks around a display name. Copying
// a command from a chat or a note often turns "Kari" into “Kari”, which the shell
// then passes through literally instead of treating as quoting.
func cleanDisplayName(displayName string) (string, error) {
	displayName = strings.TrimSpace(strings.Trim(strings.TrimSpace(displayName), "\"'“”‘’«»„‚"))
	if displayName == "" || utf8.RuneCountInString(displayName) > 64 {
		return "", invalid("display name must be 1–64 characters")
	}
	return displayName, nil
}

// CreateUser adds a user. Users are created from the CLI, never from the web UI.
func (s *Store) CreateUser(ctx context.Context, name, displayName string) (User, error) {
	if !userName.MatchString(name) {
		return User{}, invalid("user name %q: use 1–32 lowercase letters, digits or '-', starting with a letter", name)
	}
	displayName, err := cleanDisplayName(displayName)
	if err != nil {
		return User{}, err
	}
	u := User{Name: name, DisplayName: displayName}
	err = s.write(ctx, func(tx *sql.Tx) error {
		var count, taken int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE name = ?) FROM users`, name).Scan(&count, &taken); err != nil {
			return err
		}
		if taken > 0 {
			return invalid("user %q already exists", name)
		}
		if count >= MaxUsers {
			return invalid("this app supports at most %d users", MaxUsers)
		}
		now := s.Now()
		res, err := tx.ExecContext(ctx, `INSERT INTO users (name, display_name, created_at) VALUES (?, ?, ?)`,
			name, displayName, formatTime(now))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		u.ID, u.CreatedAt = ledger.UserID(id), now.UTC().Truncate(time.Second)
		return err
	})
	return u, err
}

// RenameUser changes a user's display name (the login handle stays).
func (s *Store) RenameUser(ctx context.Context, name, displayName string) (User, error) {
	displayName, err := cleanDisplayName(displayName)
	if err != nil {
		return User{}, err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET display_name = ? WHERE name = ?`, displayName, name)
	if err := requireOneRow(res, err); err != nil {
		return User{}, err
	}
	return s.UserByName(ctx, name)
}

// Users returns all users in creation order.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	return users(ctx, s.DB)
}

func users(ctx context.Context, q querier) ([]User, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, display_name, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func userIDs(ctx context.Context, q querier) ([]ledger.UserID, error) {
	us, err := users(ctx, q)
	ids := make([]ledger.UserID, len(us))
	for i, u := range us {
		ids[i] = u.ID
	}
	return ids, err
}

// UserByName looks a user up by login handle.
func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	return scanUser(s.DB.QueryRowContext(ctx,
		`SELECT id, name, display_name, created_at FROM users WHERE name = ?`, name))
}

// UserByID looks a user up by ID.
func (s *Store) UserByID(ctx context.Context, id ledger.UserID) (User, error) {
	return scanUser(s.DB.QueryRowContext(ctx,
		`SELECT id, name, display_name, created_at FROM users WHERE id = ?`, int64(id)))
}

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (User, error) {
	var u User
	var created string
	if err := row.Scan(&u.ID, &u.Name, &u.DisplayName, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	var err error
	u.CreatedAt, err = parseTime(created)
	return u, err
}
