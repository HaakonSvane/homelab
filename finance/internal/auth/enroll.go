package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/store"
)

// ErrBadLink means an enrollment token is unknown, used or expired.
var ErrBadLink = errors.New("auth: enrollment link invalid, used or expired")

// EnrollmentLink creates a one-time link (valid for EnrollmentLifetime) that lets the
// named user register a passkey. Only the token's hash is stored; the link is shown
// once by the CLI and can't be recovered.
func (a *Auth) EnrollmentLink(ctx context.Context, userName string) (link string, expires time.Time, err error) {
	u, err := a.store.UserByName(ctx, userName)
	if errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, fmt.Errorf("no user named %q (create it first with `finance add-user`)", userName)
	}
	if err != nil {
		return "", time.Time{}, err
	}
	raw, hash := newToken()
	expires = a.now().Add(EnrollmentLifetime)
	if err := a.store.CreateEnrollmentToken(ctx, u.ID, hash, expires); err != nil {
		return "", time.Time{}, err
	}
	return a.origin + "/registrer?token=" + raw, expires, nil
}

// EnrollmentUser returns the user a still-valid enrollment token belongs to.
func (a *Auth) EnrollmentUser(ctx context.Context, raw string) (store.User, error) {
	tok, err := a.store.LookupEnrollmentToken(ctx, hashToken(raw))
	if errors.Is(err, store.ErrNotFound) || (err == nil && (tok.Used || !a.now().Before(tok.ExpiresAt))) {
		return store.User{}, ErrBadLink
	}
	if err != nil {
		return store.User{}, err
	}
	return a.store.UserByID(ctx, tok.User)
}
