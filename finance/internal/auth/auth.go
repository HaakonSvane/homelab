// Package auth handles passkey (WebAuthn) registration and login, login sessions,
// one-time enrollment links, and the middleware that puts the logged-in user on the
// request context. There are no passwords and no open registration: a user gets a
// passkey through a link printed by `finance enroll`, or adds one while logged in.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/HaakonSvane/homelab/finance/internal/store"
)

const (
	// SessionLifetime is how long a session lasts without being used. Each day it is
	// used, it is pushed out again (sliding expiry).
	SessionLifetime = 90 * 24 * time.Hour
	// EnrollmentLifetime is how long an enrollment link printed by the CLI is valid.
	EnrollmentLifetime = 30 * time.Minute

	sessionRefreshAfter = 24 * time.Hour
	ceremonyLifetime    = 5 * time.Minute

	// The __Host- prefix makes the browser insist on Secure, Path=/ and no Domain.
	sessionCookie  = "__Host-session"
	ceremonyCookie = "__Host-ceremony"
)

// Auth is the passkey and session machinery.
type Auth struct {
	store      *store.Store
	wa         *webauthn.WebAuthn
	origin     string
	ceremonies *ceremonies
}

// New sets up WebAuthn for origin, e.g. "https://finans.home.arpa". The relying
// party ID is the origin's host name. It must never change once passkeys exist:
// every passkey is bound to it.
func New(st *store.Store, origin string) (*Auth, error) {
	origin, rpID, err := parseOrigin(origin)
	if err != nil {
		return nil, err
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "Finans",
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
	})
	if err != nil {
		return nil, err
	}
	return &Auth{store: st, wa: wa, origin: origin, ceremonies: newCeremonies()}, nil
}

// parseOrigin accepts https://host, or http://localhost[:port] for local testing.
func parseOrigin(raw string) (origin, rpID string, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", "", fmt.Errorf("auth: FINANCE_ORIGIN %q must look like https://finans.home.arpa", raw)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && u.Hostname() == "localhost") {
		return "", "", fmt.Errorf("auth: FINANCE_ORIGIN %q must use https (passkeys need a secure origin)", raw)
	}
	return u.Scheme + "://" + u.Host, u.Hostname(), nil
}

// Origin is the configured origin, e.g. "https://finans.home.arpa".
func (a *Auth) Origin() string { return a.origin }

func (a *Auth) now() time.Time { return a.store.Now() }

// newToken returns a random URL-safe token and its sha256. Only the hash is stored.
func newToken() (raw string, hash []byte) {
	b := make([]byte, 32)
	rand.Read(b) // never returns an error; crashes the program on failure
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw)
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// userHandle is the WebAuthn user handle: the user's ID as 8 big-endian bytes. It
// carries no personal data, and stays the same across reset-passkeys, so a phone
// replaces its old passkey for the user instead of keeping both.
func userHandle(u store.User) []byte {
	return binary.BigEndian.AppendUint64(nil, uint64(u.ID))
}

// waUser adapts a user and their passkeys to webauthn.User.
type waUser struct {
	user     store.User
	passkeys []store.Passkey
}

func (w waUser) WebAuthnID() []byte          { return userHandle(w.user) }
func (w waUser) WebAuthnName() string        { return w.user.Name }
func (w waUser) WebAuthnDisplayName() string { return w.user.DisplayName }
func (w waUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, len(w.passkeys))
	for i, p := range w.passkeys {
		out[i] = toCredential(p)
	}
	return out
}

func toCredential(p store.Passkey) webauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, len(p.Transports))
	for i, t := range p.Transports {
		transports[i] = protocol.AuthenticatorTransport(t)
	}
	return webauthn.Credential{
		ID:            p.CredentialID,
		PublicKey:     p.PublicKey,
		Transport:     transports,
		Flags:         webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(p.Flags)),
		Authenticator: webauthn.Authenticator{AAGUID: p.AAGUID, SignCount: p.SignCount},
	}
}

// deviceLabel guesses a default passkey label from the User-Agent; users can rename it.
func deviceLabel(userAgent string) string {
	for _, d := range []struct{ needle, label string }{
		{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Macintosh", "Mac"}, {"Android", "Android"},
		{"Windows", "Windows"}, {"CrOS", "Chromebook"}, {"Linux", "Linux"},
	} {
		if strings.Contains(userAgent, d.needle) {
			return d.label
		}
	}
	return "Passkey"
}
