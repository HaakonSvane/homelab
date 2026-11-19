package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
)

// Passkey is a registered WebAuthn credential.
type Passkey struct {
	ID           int64
	User         ledger.UserID
	CredentialID []byte
	PublicKey    []byte
	SignCount    uint32
	Transports   []string
	Flags        uint8 // raw authenticator flags (backup eligible/state etc.)
	AAGUID       []byte
	Label        string
	CreatedAt    time.Time
	LastUsedAt   time.Time // zero if never used to log in
}

const passkeyCols = `id, user_id, credential_id, public_key, sign_count, transports, flags, aaguid, label, created_at, last_used_at`

func scanPasskey(row scanner) (Passkey, error) {
	var p Passkey
	var transports, created string
	var lastUsed sql.NullString
	if err := row.Scan(&p.ID, &p.User, &p.CredentialID, &p.PublicKey, &p.SignCount, &transports, &p.Flags, &p.AAGUID,
		&p.Label, &created, &lastUsed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Passkey{}, ErrNotFound
		}
		return Passkey{}, err
	}
	if err := json.Unmarshal([]byte(transports), &p.Transports); err != nil {
		return Passkey{}, err
	}
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return Passkey{}, err
	}
	if lastUsed.Valid {
		p.LastUsedAt, err = parseTime(lastUsed.String)
	}
	return p, err
}

// Passkeys returns a user's passkeys, oldest first.
func (s *Store) Passkeys(ctx context.Context, user ledger.UserID) ([]Passkey, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE user_id = ? ORDER BY id`, int64(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PasskeyByCredentialID finds the passkey a login assertion was made with.
func (s *Store) PasskeyByCredentialID(ctx context.Context, credentialID []byte) (Passkey, error) {
	return scanPasskey(s.DB.QueryRowContext(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE credential_id = ?`, credentialID))
}

func cleanLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if utf8.RuneCountInString(label) > 60 {
		return "", invalid("passkey label longer than 60 characters")
	}
	return label, nil
}

// AddPasskey stores a newly registered passkey for an already logged-in user.
// If enrollmentTokenHash is non-nil, the enrollment token is consumed in the same
// transaction: the passkey is only stored if the token is still unused and unexpired.
func (s *Store) AddPasskey(ctx context.Context, p Passkey, enrollmentTokenHash []byte) (int64, error) {
	label, err := cleanLabel(p.Label)
	if err != nil {
		return 0, err
	}
	transports, err := json.Marshal(nonNil(p.Transports))
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.write(ctx, func(tx *sql.Tx) error {
		now := s.timestamp()
		if enrollmentTokenHash != nil {
			res, err := tx.ExecContext(ctx, `
				UPDATE enrollment_tokens SET used_at = ?
				WHERE token_hash = ? AND user_id = ? AND used_at IS NULL AND expires_at > ?`,
				now, enrollmentTokenHash, int64(p.User), now)
			if err := requireOneRow(res, err); errors.Is(err, ErrNotFound) {
				return invalid("enrollment link is expired, used, or for another user")
			} else if err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO passkeys (user_id, credential_id, public_key, sign_count, transports, flags, aaguid, label, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			int64(p.User), p.CredentialID, p.PublicKey, int64(p.SignCount), string(transports), int64(p.Flags), p.AAGUID, label, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// PasskeyUsed records a successful login: the authenticator's new signature counter
// and flags, and the time.
func (s *Store) PasskeyUsed(ctx context.Context, id int64, signCount uint32, flags uint8) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE passkeys SET sign_count = ?, flags = ?, last_used_at = ? WHERE id = ?`,
		int64(signCount), int64(flags), s.timestamp(), id)
	return requireOneRow(res, err)
}

// RenamePasskey changes the label of one of user's own passkeys.
func (s *Store) RenamePasskey(ctx context.Context, user ledger.UserID, id int64, label string) error {
	label, err := cleanLabel(label)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE passkeys SET label = ? WHERE id = ? AND user_id = ?`, label, id, int64(user))
	return requireOneRow(res, err)
}

// ResetPasskeys removes all of a user's passkeys, sessions and unused enrollment
// links (account recovery). It returns how many passkeys were removed.
func (s *Store) ResetPasskeys(ctx context.Context, user ledger.UserID) (int, error) {
	var n int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM passkeys WHERE user_id = ?`, int64(user))
		if err != nil {
			return err
		}
		if n, err = res.RowsAffected(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, int64(user)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM enrollment_tokens WHERE user_id = ? AND used_at IS NULL`, int64(user))
		return err
	})
	return int(n), err
}

// EnrollmentToken is a one-time link to register a passkey.
type EnrollmentToken struct {
	User      ledger.UserID
	ExpiresAt time.Time
	Used      bool
}

// CreateEnrollmentToken stores the hash of a new enrollment token, and clears out
// expired tokens and sessions while it is at it.
func (s *Store) CreateEnrollmentToken(ctx context.Context, user ledger.UserID, hash []byte, expires time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := deleteExpired(ctx, tx, s.timestamp()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO enrollment_tokens (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
			hash, int64(user), formatTime(expires))
		return err
	})
}

// LookupEnrollmentToken returns the token with this hash, or ErrNotFound.
func (s *Store) LookupEnrollmentToken(ctx context.Context, hash []byte) (EnrollmentToken, error) {
	var t EnrollmentToken
	var expires string
	var used sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT user_id, expires_at, used_at FROM enrollment_tokens WHERE token_hash = ?`, hash).
		Scan(&t.User, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	t.Used = used.Valid
	t.ExpiresAt, err = parseTime(expires)
	return t, err
}

// Session is a login session.
type Session struct {
	User      ledger.UserID
	ExpiresAt time.Time
}

// CreateSession stores the hash of a new session token, clearing expired sessions
// and tokens first.
func (s *Store) CreateSession(ctx context.Context, user ledger.UserID, hash []byte, expires time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		now := s.timestamp()
		if err := deleteExpired(ctx, tx, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
			hash, int64(user), now, formatTime(expires))
		return err
	})
}

// LookupSession returns the unexpired session with this hash, or ErrNotFound.
func (s *Store) LookupSession(ctx context.Context, hash []byte) (Session, error) {
	var sess Session
	var expires string
	err := s.DB.QueryRowContext(ctx, `SELECT user_id, expires_at FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		hash, s.timestamp()).Scan(&sess.User, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, ErrNotFound
	}
	if err != nil {
		return sess, err
	}
	sess.ExpiresAt, err = parseTime(expires)
	return sess, err
}

// ExtendSession moves a session's expiry (sliding expiration).
func (s *Store) ExtendSession(ctx context.Context, hash []byte, expires time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, formatTime(expires), hash)
	return requireOneRow(res, err)
}

// DeleteSession ends a session (logout). Deleting a missing session is not an error.
func (s *Store) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash)
	return err
}

func deleteExpired(ctx context.Context, tx *sql.Tx, now string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM enrollment_tokens WHERE expires_at <= ?`, now)
	return err
}
