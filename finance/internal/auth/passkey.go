package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/HaakonSvane/homelab/finance/internal/store"
)

const maxBody = 64 << 10

// Norwegian messages shown by passkey.js.
const (
	msgTryAgain    = "Noe gikk galt. Prøv igjen."
	msgExpired     = "Forsøket tok for lang tid. Prøv igjen."
	msgLoginFailed = "Innloggingen mislyktes. Prøv igjen."
	msgUnknownKey  = "Denne passkeyen er ikke registrert (lenger). Be om en ny registreringslenke."
	msgBadLink     = "Registreringslenken er ugyldig, allerede brukt eller utløpt. Be om en ny."
	msgNeedLogin   = "Du må logge inn først."
	msgRegFailed   = "Registreringen mislyktes. Prøv igjen."
	msgBusy        = "For mange forsøk akkurat nå. Vent litt og prøv igjen."
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *Auth) beginCeremony(w http.ResponseWriter, c ceremony, options any) {
	now := a.now()
	c.expires = now.Add(ceremonyLifetime)
	key := a.ceremonies.put(c, now)
	if key == "" {
		fail(w, http.StatusServiceUnavailable, msgBusy)
		return
	}
	setCookie(w, ceremonyCookie, key, ceremonyLifetime, http.SameSiteStrictMode)
	writeJSON(w, http.StatusOK, options)
}

func (a *Auth) takeCeremony(w http.ResponseWriter, r *http.Request, register bool) (ceremony, bool) {
	clearCookie(w, ceremonyCookie)
	c, err := r.Cookie(ceremonyCookie)
	if err != nil {
		fail(w, http.StatusBadRequest, msgExpired)
		return ceremony{}, false
	}
	cer, ok := a.ceremonies.take(c.Value, a.now())
	if !ok || cer.register != register {
		fail(w, http.StatusBadRequest, msgExpired)
		return ceremony{}, false
	}
	return cer, true
}

// BeginLogin starts a discoverable-credential login: the browser offers whichever
// passkey it has for this site, so no user name is needed.
func (a *Auth) BeginLogin(w http.ResponseWriter, r *http.Request) {
	opts, session, err := a.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		slog.Error("begin login", "err", err)
		fail(w, http.StatusInternalServerError, msgTryAgain)
		return
	}
	a.beginCeremony(w, ceremony{session: *session}, opts)
}

// FinishLogin verifies the assertion, records the passkey's use, and starts a session.
func (a *Auth) FinishLogin(w http.ResponseWriter, r *http.Request) {
	cer, ok := a.takeCeremony(w, r, false)
	if !ok {
		return
	}
	ctx := r.Context()
	parsed, err := protocol.ParseCredentialRequestResponseBody(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		slog.Warn("login: bad assertion", "err", err)
		fail(w, http.StatusBadRequest, msgLoginFailed)
		return
	}
	var used store.Passkey
	unknown := false
	lookup := func(rawID, handle []byte) (webauthn.User, error) {
		p, err := a.store.PasskeyByCredentialID(ctx, rawID)
		if errors.Is(err, store.ErrNotFound) {
			unknown = true
		}
		if err != nil {
			return nil, err
		}
		u, err := a.store.UserByID(ctx, p.User)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(handle, userHandle(u)) {
			return nil, errors.New("user handle does not match the passkey's owner")
		}
		ps, err := a.store.Passkeys(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		used = p
		return waUser{user: u, passkeys: ps}, nil
	}
	user, cred, err := a.wa.ValidatePasskeyLogin(lookup, cer.session, parsed)
	if err != nil {
		slog.Warn("login failed", "err", err, "unknown_passkey", unknown)
		msg := msgLoginFailed
		if unknown {
			msg = msgUnknownKey
		}
		fail(w, http.StatusUnauthorized, msg)
		return
	}
	u := user.(waUser).user
	if cred.Authenticator.CloneWarning {
		// Synced passkeys report a zero counter, so this only fires for hardware keys.
		slog.Warn("passkey signature counter went backwards; possible cloned authenticator", "user", u.Name, "passkey", used.ID)
	}
	if err := a.store.PasskeyUsed(ctx, used.ID, cred.Authenticator.SignCount, uint8(cred.Flags.ProtocolValue())); err != nil {
		slog.Error("record passkey use", "err", err)
	}
	if err := a.startSession(ctx, w, u); err != nil {
		slog.Error("start session", "err", err)
		fail(w, http.StatusInternalServerError, msgTryAgain)
		return
	}
	slog.Info("login", "user", u.Name, "passkey", used.ID)
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/"})
}

// BeginRegistration starts registering a passkey, either through an enrollment link
// ({"token": "..."}) or for the logged-in user adding another device.
func (a *Auth) BeginRegistration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, msgTryAgain)
		return
	}
	var u store.User
	var enrollHash []byte
	if req.Token != "" {
		var err error
		if u, err = a.EnrollmentUser(r.Context(), req.Token); err != nil {
			fail(w, http.StatusBadRequest, msgBadLink)
			return
		}
		enrollHash = hashToken(req.Token)
	} else if cur, ok := UserFrom(ctx); ok {
		u = cur
	} else {
		fail(w, http.StatusUnauthorized, msgNeedLogin)
		return
	}
	ps, err := a.store.Passkeys(ctx, u.ID)
	if err != nil {
		slog.Error("begin registration", "err", err)
		fail(w, http.StatusInternalServerError, msgTryAgain)
		return
	}
	wu := waUser{user: u, passkeys: ps}
	// Ask the browser not to create a second passkey on an authenticator that already
	// has one for this user (it shows "already registered" instead).
	var exclude []protocol.CredentialDescriptor
	for _, c := range wu.WebAuthnCredentials() {
		exclude = append(exclude, c.Descriptor())
	}
	opts, session, err := a.wa.BeginRegistration(wu, webauthn.WithExclusions(exclude))
	if err != nil {
		slog.Error("begin registration", "err", err)
		fail(w, http.StatusInternalServerError, msgTryAgain)
		return
	}
	a.beginCeremony(w, ceremony{session: *session, register: true, user: u.ID, enrollHash: enrollHash}, opts)
}

// FinishRegistration verifies the new credential and stores it. Through an enrollment
// link it also consumes the link and logs the user in.
func (a *Auth) FinishRegistration(w http.ResponseWriter, r *http.Request) {
	cer, ok := a.takeCeremony(w, r, true)
	if !ok {
		return
	}
	ctx := r.Context()
	if cer.enrollHash == nil {
		if cur, ok := UserFrom(ctx); !ok || cur.ID != cer.user {
			fail(w, http.StatusUnauthorized, msgNeedLogin)
			return
		}
	}
	u, err := a.store.UserByID(ctx, cer.user)
	if err != nil {
		fail(w, http.StatusBadRequest, msgRegFailed)
		return
	}
	ps, err := a.store.Passkeys(ctx, u.ID)
	if err != nil {
		slog.Error("finish registration", "err", err)
		fail(w, http.StatusInternalServerError, msgTryAgain)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		slog.Warn("registration: bad response", "err", err)
		fail(w, http.StatusBadRequest, msgRegFailed)
		return
	}
	cred, err := a.wa.CreateCredential(waUser{user: u, passkeys: ps}, cer.session, parsed)
	if err != nil {
		slog.Warn("registration failed", "err", err)
		fail(w, http.StatusBadRequest, msgRegFailed)
		return
	}
	transports := make([]string, len(cred.Transport))
	for i, t := range cred.Transport {
		transports[i] = string(t)
	}
	id, err := a.store.AddPasskey(ctx, store.Passkey{
		User:         u.ID,
		CredentialID: cred.ID,
		PublicKey:    cred.PublicKey,
		SignCount:    cred.Authenticator.SignCount,
		Transports:   transports,
		Flags:        uint8(cred.Flags.ProtocolValue()),
		AAGUID:       cred.Authenticator.AAGUID,
		Label:        deviceLabel(r.UserAgent()),
	}, cer.enrollHash)
	if errors.Is(err, store.ErrInvalid) {
		fail(w, http.StatusBadRequest, msgBadLink)
		return
	}
	if err != nil {
		slog.Error("store passkey", "err", err)
		fail(w, http.StatusInternalServerError, msgTryAgain)
		return
	}
	slog.Info("passkey registered", "user", u.Name, "passkey", id, "via_link", cer.enrollHash != nil)
	redirect := "/innstillinger"
	if cer.enrollHash != nil {
		if err := a.startSession(ctx, w, u); err != nil {
			slog.Error("start session", "err", err)
			fail(w, http.StatusInternalServerError, msgTryAgain)
			return
		}
		redirect = "/"
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": redirect})
}
