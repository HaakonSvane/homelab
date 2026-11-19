package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/store"
)

var t0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*Auth, *store.Store, store.User, *time.Time) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := t0
	st.Now = func() time.Time { return now }
	u, err := st.CreateUser(context.Background(), "kari", "Kari")
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(st, "https://finans.home.arpa")
	if err != nil {
		t.Fatal(err)
	}
	return a, st, u, &now
}

func TestParseOrigin(t *testing.T) {
	good := map[string]string{
		"https://finans.home.arpa":      "finans.home.arpa",
		"https://finans.home.arpa/":     "finans.home.arpa",
		"https://finans.home.arpa:8443": "finans.home.arpa",
		"http://localhost:8080":         "localhost",
	}
	for in, rpID := range good {
		if _, got, err := parseOrigin(in); err != nil || got != rpID {
			t.Errorf("parseOrigin(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "finans.home.arpa", "http://finans.home.arpa", "https://finans.home.arpa/app", "https://x?y=1"} {
		if _, _, err := parseOrigin(in); err == nil {
			t.Errorf("parseOrigin(%q) accepted", in)
		}
	}
}

func TestTokens(t *testing.T) {
	raw1, hash1 := newToken()
	raw2, _ := newToken()
	if raw1 == raw2 || len(raw1) != 43 || len(hash1) != 32 {
		t.Fatalf("tokens %q %q", raw1, raw2)
	}
	if string(hashToken(raw1)) != string(hash1) {
		t.Error("hashToken not deterministic")
	}
}

// whoAmI reports the user the middleware resolved, and any Set-Cookie it sent.
func serveWithCookie(a *Auth, cookie string) (user string, setCookie string) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := UserFrom(r.Context()); ok {
			user = u.Name
		}
	})).ServeHTTP(rec, req)
	return user, rec.Header().Get("Set-Cookie")
}

func TestSessionSlidingExpiry(t *testing.T) {
	a, st, u, now := setup(t)
	rec := httptest.NewRecorder()
	if err := a.startSession(context.Background(), rec, u); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != "__Host-session" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode ||
		cookie.Path != "/" || cookie.MaxAge != int(SessionLifetime/time.Second) {
		t.Fatalf("cookie = %+v", cookie)
	}

	// Same day: recognized, no refresh.
	*now = t0.Add(time.Hour)
	if who, set := serveWithCookie(a, cookie.Value); who != "kari" || set != "" {
		t.Errorf("same day: user %q, Set-Cookie %q", who, set)
	}
	// 80 days later (still valid): recognized and pushed out to now+90d.
	*now = t0.Add(80 * 24 * time.Hour)
	if who, set := serveWithCookie(a, cookie.Value); who != "kari" || set == "" {
		t.Errorf("day 80: user %q, Set-Cookie %q", who, set)
	}
	sess, err := st.LookupSession(context.Background(), hashToken(cookie.Value))
	if err != nil || !sess.ExpiresAt.Equal(now.Add(SessionLifetime)) {
		t.Errorf("session after refresh = %+v, %v", sess, err)
	}
	// 91 days after that refresh: expired; the cookie is cleared.
	*now = now.Add(91 * 24 * time.Hour)
	if who, set := serveWithCookie(a, cookie.Value); who != "" || set == "" {
		t.Errorf("expired: user %q, Set-Cookie %q", who, set)
	}
	// Garbage cookie: anonymous.
	if who, _ := serveWithCookie(a, "not-a-session"); who != "" {
		t.Errorf("garbage cookie resolved to %q", who)
	}
}

func TestRequireUser(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	cases := []struct {
		method, hx string
		user       bool
		want       int
		location   string
	}{
		{"GET", "", true, http.StatusTeapot, ""},
		{"GET", "", false, http.StatusSeeOther, "/logg-inn"},
		{"POST", "", false, http.StatusUnauthorized, ""},
		{"GET", "true", false, http.StatusUnauthorized, ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/innstillinger", nil)
		if c.hx != "" {
			req.Header.Set("HX-Request", c.hx)
		}
		if c.user {
			req = req.WithContext(WithUser(req.Context(), store.User{ID: 1, Name: "kari"}))
		}
		rec := httptest.NewRecorder()
		RequireUser(ok).ServeHTTP(rec, req)
		if rec.Code != c.want || rec.Header().Get("Location") != c.location {
			t.Errorf("%+v: %d → %q", c, rec.Code, rec.Header().Get("Location"))
		}
		if c.hx != "" && rec.Header().Get("HX-Redirect") != "/logg-inn" {
			t.Errorf("htmx request without HX-Redirect")
		}
	}
}

func TestCeremoniesExpireAndAreSingleUse(t *testing.T) {
	cs := newCeremonies()
	key := cs.put(ceremony{expires: t0.Add(ceremonyLifetime)}, t0)
	if _, ok := cs.take(key, t0.Add(ceremonyLifetime+time.Second)); ok {
		t.Error("expired ceremony accepted")
	}
	key = cs.put(ceremony{expires: t0.Add(ceremonyLifetime)}, t0)
	if _, ok := cs.take(key, t0); !ok {
		t.Fatal("fresh ceremony rejected")
	}
	if _, ok := cs.take(key, t0); ok {
		t.Error("ceremony used twice")
	}
	for i := 0; i < maxCeremonies; i++ {
		cs.put(ceremony{expires: t0.Add(time.Minute)}, t0)
	}
	if cs.put(ceremony{expires: t0.Add(time.Minute)}, t0) != "" {
		t.Error("no cap on in-flight ceremonies")
	}
	if cs.put(ceremony{expires: t0.Add(time.Hour)}, t0.Add(2*time.Minute)) == "" {
		t.Error("expired ceremonies not swept")
	}
}

func TestEnrollmentLinkExpiry(t *testing.T) {
	a, _, _, now := setup(t)
	if _, _, err := a.EnrollmentLink(context.Background(), "nobody"); err == nil {
		t.Error("link for unknown user")
	}
	link, expires, err := a.EnrollmentLink(context.Background(), "kari")
	if err != nil || !expires.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("link %q expires %v: %v", link, expires, err)
	}
	token := link[len("https://finans.home.arpa/registrer?token="):]
	if u, err := a.EnrollmentUser(context.Background(), token); err != nil || u.Name != "kari" {
		t.Fatalf("EnrollmentUser = %+v, %v", u, err)
	}
	*now = t0.Add(31 * time.Minute)
	if _, err := a.EnrollmentUser(context.Background(), token); err != ErrBadLink {
		t.Errorf("expired link: %v, want ErrBadLink", err)
	}
}

func TestDeviceLabel(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X) AppleWebKit/605.1.15": "iPhone",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15":        "Mac",
		"Mozilla/5.0 (Linux; Android 16; Pixel 9) AppleWebKit/537.36":                 "Android",
		"curl/8.0": "Passkey",
	} {
		if got := deviceLabel(ua); got != want {
			t.Errorf("deviceLabel(%q) = %q, want %q", ua, got, want)
		}
	}
}
