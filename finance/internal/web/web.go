// Package web holds the HTTP routes, handlers and templ pages. User-facing paths and
// text are Norwegian; code is English.
package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"

	"github.com/HaakonSvane/homelab/finance/internal/auth"
	"github.com/HaakonSvane/homelab/finance/internal/store"
	"github.com/HaakonSvane/homelab/finance/internal/web/pages"
	assets "github.com/HaakonSvane/homelab/finance/web"
)

// Server holds what the handlers need.
type Server struct {
	store *store.Store
	auth  *auth.Auth
}

// New returns the application's root handler.
func New(st *store.Store, a *auth.Auth) http.Handler {
	s := &Server{store: st, auth: a}
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /healthz", healthz(st))
	mux.Handle("GET /static/", assets.Handler())
	mux.HandleFunc("GET /logg-inn", s.loginPage)
	mux.HandleFunc("GET /registrer", s.enrollPage)
	mux.HandleFunc("POST /passkey/login/begin", a.BeginLogin)
	mux.HandleFunc("POST /passkey/login/finish", a.FinishLogin)
	// Registration checks for itself: an enrollment token, or a logged-in user.
	mux.HandleFunc("POST /passkey/register/begin", a.BeginRegistration)
	mux.HandleFunc("POST /passkey/register/finish", a.FinishRegistration)
	mux.HandleFunc("POST /logg-ut", a.Logout)

	// Logged in only.
	private := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, auth.RequireUser(h)) }
	private("GET /{$}", s.overview)
	private("POST /kan-jeg-kjope", s.canIBuy)
	private("POST /felleskonto/saldo", s.checkBuffer)
	private("GET /utgifter", s.expenses)
	private("GET /utgifter/ny", s.newExpense)
	private("POST /utgifter", s.createExpense)
	private("GET /utgifter/{id}/endre", s.editExpense)
	private("POST /utgifter/{id}", s.updateExpense)
	private("POST /utgifter/{id}/avslutt", s.endExpense)
	private("POST /utgifter/{id}/slett", s.deleteExpense)
	private("GET /inntekt", s.income)
	private("GET /inntekt/{id}/endre", s.editIncome)
	private("POST /inntekt/{id}", s.saveIncome)
	private("GET /innstillinger", s.settings)
	private("POST /innstillinger/passkeys/{id}/navn", s.renamePasskey)

	mux.HandleFunc("/", s.notFound)

	// CrossOriginProtection rejects state-changing requests from other sites, using
	// Sec-Fetch-Site/Origin (Go 1.25+); SameSite=Lax cookies are a second layer.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forespørselen ble avvist fordi den kom fra et annet nettsted.", http.StatusForbidden)
	}))
	return securityHeaders(cop.Handler(a.Middleware(mux)))
}

// csp allows only same-origin scripts and styles: no inline code anywhere.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// Pinger is the part of the store /healthz needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

func healthz(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := db.Ping(ctx); err != nil {
			http.Error(w, "database: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	}
}

// render writes a page. It renders into a buffer first so a template error becomes
// a clean 500 instead of half a page.
func render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		slog.Error("render", "path", r.URL.Path, "err", err)
		status, buf = http.StatusInternalServerError, *bytes.NewBufferString("Noe gikk galt.")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	render(w, r, http.StatusInternalServerError, pages.Error("Noe gikk galt", "Prøv igjen om litt."))
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	render(w, r, http.StatusNotFound, pages.Error("Fant ikke siden", "Siden du lette etter finnes ikke."))
}

// currentUser is only called behind RequireUser.
func currentUser(r *http.Request) store.User {
	u, _ := auth.UserFrom(r.Context())
	return u
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	render(w, r, http.StatusOK, pages.Login())
}

func (s *Server) enrollPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	u, err := s.auth.EnrollmentUser(r.Context(), token)
	if errors.Is(err, auth.ErrBadLink) {
		render(w, r, http.StatusGone, pages.EnrollInvalid())
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	render(w, r, http.StatusOK, pages.Enroll(u.DisplayName, token))
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, http.StatusOK, pages.SettingsError{})
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, status int, formErr pages.SettingsError) {
	u := currentUser(r)
	passkeys, err := s.store.Passkeys(r.Context(), u.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	render(w, r, status, pages.Settings(u, passkeys, formErr))
}

func (s *Server) renamePasskey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	err = s.store.RenamePasskey(r.Context(), currentUser(r).ID, id, r.FormValue("label"))
	switch {
	case errors.Is(err, store.ErrInvalid):
		s.renderSettings(w, r, http.StatusUnprocessableEntity, pages.SettingsError{PasskeyID: id, Message: "Navnet kan ha maks 60 tegn."})
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		serverError(w, r, err)
	default:
		http.Redirect(w, r, "/innstillinger", http.StatusSeeOther)
	}
}
