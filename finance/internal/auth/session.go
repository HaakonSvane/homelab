package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/store"
)

type userKey struct{}

// UserFrom returns the logged-in user, if any.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey{}).(store.User)
	return u, ok
}

// WithUser returns ctx carrying u as the logged-in user (for handlers and tests).
func WithUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

func setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge / time.Second),
		HttpOnly: true,
		Secure:   true,
		SameSite: sameSite,
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// startSession logs u in on this browser.
func (a *Auth) startSession(ctx context.Context, w http.ResponseWriter, u store.User) error {
	raw, hash := newToken()
	if err := a.store.CreateSession(ctx, u.ID, hash, a.now().Add(SessionLifetime)); err != nil {
		return err
	}
	setCookie(w, sessionCookie, raw, SessionLifetime, http.SameSiteLaxMode)
	return nil
}

// Middleware resolves the session cookie to a user and puts it on the request
// context. Requests without a valid session pass through anonymously; RequireUser
// decides what needs a login. A session used more than a day after its last refresh
// is pushed out to a full SessionLifetime again.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		hash := hashToken(c.Value)
		sess, err := a.store.LookupSession(ctx, hash)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				slog.Error("session lookup", "err", err)
			}
			clearCookie(w, sessionCookie)
			next.ServeHTTP(w, r)
			return
		}
		u, err := a.store.UserByID(ctx, sess.User)
		if err != nil {
			slog.Error("session user", "err", err)
			next.ServeHTTP(w, r)
			return
		}
		now := a.now()
		if sess.ExpiresAt.Sub(now) < SessionLifetime-sessionRefreshAfter {
			if err := a.store.ExtendSession(ctx, hash, now.Add(SessionLifetime)); err != nil {
				slog.Error("extend session", "err", err)
			} else {
				setCookie(w, sessionCookie, c.Value, SessionLifetime, http.SameSiteLaxMode)
			}
		}
		next.ServeHTTP(w, r.WithContext(WithUser(ctx, u)))
	})
}

// LoginPath is where anonymous visitors are sent.
const LoginPath = "/logg-inn"

// RequireUser lets only logged-in users through. Others are redirected to the login
// page: htmx requests via HX-Redirect, page loads via 303, other requests get 401.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case r.Header.Get("HX-Request") != "":
			w.Header().Set("HX-Redirect", LoginPath)
			w.WriteHeader(http.StatusUnauthorized)
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			http.Redirect(w, r, LoginPath, http.StatusSeeOther)
		default:
			http.Error(w, "Du må logge inn.", http.StatusUnauthorized)
		}
	})
}

// Logout ends the current session (if any) and clears the cookie.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := a.store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			slog.Error("logout", "err", err)
		}
	}
	clearCookie(w, sessionCookie)
	http.Redirect(w, r, LoginPath, http.StatusSeeOther)
}
