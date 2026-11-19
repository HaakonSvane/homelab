package web

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/HaakonSvane/homelab/finance/internal/auth"
	"github.com/HaakonSvane/homelab/finance/internal/store"
)

const (
	testOrigin = "https://finans.home.arpa"
	testRPID   = "finans.home.arpa"
)

type harness struct {
	t         *testing.T
	st        *store.Store
	auth      *auth.Auth
	client    *http.Client
	base      string
	kari, ola store.User
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	kari, err := st.CreateUser(ctx, "kari", "Kari")
	if err != nil {
		t.Fatal(err)
	}
	ola, err := st.CreateUser(ctx, "ola", "Ola")
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(st, testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(New(st, a))
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &harness{t: t, st: st, auth: a, client: client, base: srv.URL, kari: kari, ola: ola}
}

// do sends a request; a body starting with '{' is sent as JSON, anything else as a form.
func (h *harness) do(method, path string, body string, headers ...string) (int, string, http.Header) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.base+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	switch {
	case strings.HasPrefix(body, "{"):
		req.Header.Set("Content-Type", "application/json")
	case body != "":
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (h *harness) mustJSON(status int, body string, want int) map[string]any {
	h.t.Helper()
	if status != want {
		h.t.Fatalf("status %d, want %d: %s", status, want, body)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		h.t.Fatalf("not JSON: %s", body)
	}
	return m
}

func (h *harness) enrollToken(user string) string {
	h.t.Helper()
	link, _, err := h.auth.EnrollmentLink(context.Background(), user)
	if err != nil {
		h.t.Fatal(err)
	}
	u, _ := url.Parse(link)
	if u.Scheme+"://"+u.Host != testOrigin || u.Path != "/registrer" {
		h.t.Fatalf("link = %s", link)
	}
	return u.Query().Get("token")
}

// register runs begin+create+finish and returns the new credential and the redirect.
func (h *harness) register(sa *softAuthenticator, token string) (*softCred, string) {
	h.t.Helper()
	status, body, _ := h.do("POST", "/passkey/register/begin", `{"token":"`+token+`"}`)
	if status != http.StatusOK {
		h.t.Fatalf("register/begin: %d %s", status, body)
	}
	resp, cred := sa.create([]byte(body))
	status, body = h.do2("POST", "/passkey/register/finish", string(resp))
	m := h.mustJSON(status, body, http.StatusOK)
	return cred, m["redirect"].(string)
}

func (h *harness) login(sa *softAuthenticator, cred *softCred, origin string) (int, map[string]any) {
	h.t.Helper()
	status, body, _ := h.do("POST", "/passkey/login/begin", "")
	if status != http.StatusOK {
		h.t.Fatalf("login/begin: %d %s", status, body)
	}
	status, body, _ = h.do("POST", "/passkey/login/finish", string(sa.get([]byte(body), cred, origin)))
	var m map[string]any
	json.Unmarshal([]byte(body), &m)
	return status, m
}

func (h *harness) logout() {
	h.t.Helper()
	if status, _, hdr := h.do("POST", "/logg-ut", ""); status != http.StatusSeeOther || hdr.Get("Location") != "/logg-inn" {
		h.t.Fatalf("logout: %d %s", status, hdr.Get("Location"))
	}
}

func (h *harness) expectLoggedOut() {
	h.t.Helper()
	if status, _, hdr := h.do("GET", "/", ""); status != http.StatusSeeOther || hdr.Get("Location") != "/logg-inn" {
		h.t.Fatalf("GET / while logged out: %d → %s", status, hdr.Get("Location"))
	}
}

func (h *harness) expectLoggedInAs(name string) {
	h.t.Helper()
	status, body, _ := h.do("GET", "/", "")
	if status != http.StatusOK || !strings.Contains(body, "Hei, "+name+"!") {
		h.t.Fatalf("GET /: %d, want greeting for %s:\n%s", status, name, body)
	}
}

func TestPasskeyEnrollLoginAndAddDevice(t *testing.T) {
	h := newHarness(t)
	phone := newSoftAuthenticator(t, testOrigin, testRPID)
	h.expectLoggedOut()

	// The enrollment page greets the right person.
	token := h.enrollToken("kari")
	if status, body, _ := h.do("GET", "/registrer?token="+token, ""); status != http.StatusOK || !strings.Contains(body, "Hei, Kari!") {
		t.Fatalf("enroll page: %d\n%s", status, body)
	}

	// Registration options ask for a discoverable credential with user verification,
	// bound to finans.home.arpa, for Kari's 8-byte user handle.
	status, body, _ := h.do("POST", "/passkey/register/begin", `{"token":"`+token+`"}`)
	var opts struct {
		PublicKey struct {
			RP struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"rp"`
			User struct {
				ID string `json:"id"`
			} `json:"user"`
			Selection struct {
				ResidentKey      string `json:"residentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
		} `json:"publicKey"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &opts) != nil {
		t.Fatalf("register/begin: %d %s", status, body)
	}
	wantHandle := b64.EncodeToString(binary.BigEndian.AppendUint64(nil, uint64(h.kari.ID)))
	if o := opts.PublicKey; o.RP.ID != testRPID || o.RP.Name != "Finans" || o.User.ID != wantHandle ||
		o.Selection.ResidentKey != "required" || o.Selection.UserVerification != "required" {
		t.Fatalf("creation options = %+v", o)
	}
	resp, phoneCred := phone.create([]byte(body))
	status, body = h.do2("POST", "/passkey/register/finish", string(resp))
	if m := h.mustJSON(status, body, http.StatusOK); m["redirect"] != "/" {
		t.Fatalf("redirect after enrollment = %v", m["redirect"])
	}
	h.expectLoggedInAs("Kari") // enrolling logs you in

	// The link is now spent.
	if status, _, _ := h.do("GET", "/registrer?token="+token, ""); status != http.StatusGone {
		t.Errorf("reused link page: %d, want 410", status)
	}
	if status, _, _ := h.do("POST", "/passkey/register/begin", `{"token":"`+token+`"}`); status != http.StatusBadRequest {
		t.Errorf("reused link begin: %d, want 400", status)
	}

	// Log out, then back in with the passkey.
	h.logout()
	h.expectLoggedOut()
	if status, m := h.login(phone, phoneCred, testOrigin); status != http.StatusOK || m["redirect"] != "/" {
		t.Fatalf("login: %d %v", status, m)
	}
	h.expectLoggedInAs("Kari")
	keys, _ := h.st.Passkeys(context.Background(), h.kari.ID)
	if len(keys) != 1 || keys[0].LastUsedAt.IsZero() || keys[0].Flags&(flagBE|flagBS) != flagBE|flagBS {
		t.Fatalf("passkey after login = %+v", keys)
	}

	// While logged in, add a second device. Its options exclude the existing passkey.
	mac := newSoftAuthenticator(t, testOrigin, testRPID)
	status, body, _ = h.do("POST", "/passkey/register/begin", `{}`)
	var excl struct {
		PublicKey struct {
			Exclude []struct {
				ID string `json:"id"`
			} `json:"excludeCredentials"`
		} `json:"publicKey"`
	}
	json.Unmarshal([]byte(body), &excl)
	if status != http.StatusOK || len(excl.PublicKey.Exclude) != 1 || excl.PublicKey.Exclude[0].ID != b64.EncodeToString(phoneCred.id) {
		t.Fatalf("add-device begin: %d, exclude = %+v", status, excl)
	}
	resp, macCred := mac.create([]byte(body))
	status, body = h.do2("POST", "/passkey/register/finish", string(resp))
	if m := h.mustJSON(status, body, http.StatusOK); m["redirect"] != "/innstillinger" {
		t.Fatalf("redirect after adding device = %v", m["redirect"])
	}
	status, body, _ = h.do("GET", "/innstillinger", "")
	if status != http.StatusOK || strings.Count(body, "Gi nytt navn") != 2 {
		t.Fatalf("settings should list 2 passkeys: %d\n%s", status, body)
	}

	// Rename the first passkey; a too-long name is refused with a Norwegian message.
	keys, _ = h.st.Passkeys(context.Background(), h.kari.ID)
	renamePath := "/innstillinger/passkeys/" + strconv.FormatInt(keys[0].ID, 10) + "/navn"
	if status, _, hdr := h.do("POST", renamePath, "label=Kari+sin+iPhone"); status != http.StatusSeeOther || hdr.Get("Location") != "/innstillinger" {
		t.Fatalf("rename: %d", status)
	}
	if _, body, _ := h.do("GET", "/innstillinger", ""); !strings.Contains(body, "Kari sin iPhone") {
		t.Error("renamed label not shown")
	}
	if status, body, _ := h.do("POST", renamePath, "label="+strings.Repeat("x", 61)); status != http.StatusUnprocessableEntity || !strings.Contains(body, "maks 60 tegn") {
		t.Errorf("long label: %d", status)
	}
	// Ola can't rename Kari's passkey (it looks like it doesn't exist).
	h.logout()
	olaPhone := newSoftAuthenticator(t, testOrigin, testRPID)
	olaCred, _ := h.register(olaPhone, h.enrollToken("ola"))
	h.expectLoggedInAs("Ola")
	if status, _, _ := h.do("POST", renamePath, "label=kapret"); status != http.StatusNotFound {
		t.Errorf("Ola renaming Kari's passkey: %d, want 404", status)
	}

	// Both of Kari's devices can log in; Ola's passkey logs in Ola.
	for _, c := range []struct {
		sa   *softAuthenticator
		cred *softCred
		who  string
	}{{phone, phoneCred, "Kari"}, {mac, macCred, "Kari"}, {olaPhone, olaCred, "Ola"}} {
		h.logout()
		if status, m := h.login(c.sa, c.cred, testOrigin); status != http.StatusOK {
			t.Fatalf("login as %s: %d %v", c.who, status, m)
		}
		h.expectLoggedInAs(c.who)
	}
}

func TestPasskeyLoginRejections(t *testing.T) {
	h := newHarness(t)
	phone := newSoftAuthenticator(t, testOrigin, testRPID)
	cred, _ := h.register(phone, h.enrollToken("kari"))
	h.logout()

	// A phishing site's origin in the client data is rejected.
	if status, _ := h.login(phone, cred, "https://finans.home.arpa.evil.example"); status != http.StatusUnauthorized {
		t.Errorf("wrong origin: %d, want 401", status)
	}
	// A forged signature (different key, same credential ID) is rejected.
	forged := *cred
	forged.key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if status, _ := h.login(phone, &forged, testOrigin); status != http.StatusUnauthorized {
		t.Errorf("forged signature: %d, want 401", status)
	}
	// A user handle that doesn't belong to the credential's owner is rejected.
	wrongUser := *cred
	wrongUser.userHandle = binary.BigEndian.AppendUint64(nil, uint64(h.ola.ID))
	if status, _ := h.login(phone, &wrongUser, testOrigin); status != http.StatusUnauthorized {
		t.Errorf("mismatched user handle: %d, want 401", status)
	}
	// Finishing without a ceremony (or replaying one) fails.
	status, body, _ := h.do("POST", "/passkey/login/begin", "")
	assertion := phone.get([]byte(body), cred, testOrigin)
	if status, _, _ := h.do("POST", "/passkey/login/finish", string(assertion)); status != http.StatusOK {
		t.Fatalf("valid login: %d", status)
	}
	h.logout()
	if status, _, _ := h.do("POST", "/passkey/login/finish", string(assertion)); status != http.StatusBadRequest {
		t.Errorf("replayed assertion: %d, want 400", status)
	}
	h.expectLoggedOut()

	// Registering without a link or a session is refused.
	if status, _, _ := h.do("POST", "/passkey/register/begin", `{}`); status != http.StatusUnauthorized {
		t.Errorf("anonymous register: %d, want 401", status)
	}

	// After reset-passkeys the old passkey no longer works, with a helpful message,
	// and existing sessions are gone.
	if status, _ := h.login(phone, cred, testOrigin); status != http.StatusOK {
		t.Fatal("login before reset failed")
	}
	if _, err := h.st.ResetPasskeys(context.Background(), h.kari.ID); err != nil {
		t.Fatal(err)
	}
	h.expectLoggedOut()
	status, m := h.login(phone, cred, testOrigin)
	if status != http.StatusUnauthorized || !strings.Contains(m["error"].(string), "ikke registrert") {
		t.Errorf("login after reset: %d %v", status, m)
	}
}

func TestCrossSitePostIsRejected(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/logg-ut", "/passkey/login/begin", "/innstillinger/passkeys/1/navn"} {
		if status, body, _ := h.do("POST", path, "", "Sec-Fetch-Site", "cross-site"); status != http.StatusForbidden || !strings.Contains(body, "annet nettsted") {
			t.Errorf("cross-site POST %s: %d %q", path, status, body)
		}
	}
	if status, _, _ := h.do("POST", "/logg-ut", "", "Sec-Fetch-Site", "same-origin"); status != http.StatusSeeOther {
		t.Errorf("same-origin POST /logg-ut: %d", status)
	}
}

func TestPagesAndHeaders(t *testing.T) {
	h := newHarness(t)
	status, body, hdr := h.do("GET", "/logg-inn", "")
	if status != http.StatusOK || !strings.Contains(body, `lang="nb"`) || !strings.Contains(body, "Logg inn med passkey") {
		t.Fatalf("login page: %d\n%s", status, body)
	}
	if csp := hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || hdr.Get("X-Frame-Options") != "DENY" {
		t.Errorf("security headers: %v", hdr)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "style=") {
		t.Error("inline script or style would be blocked by the CSP")
	}
	if status, _, _ := h.do("GET", "/registrer?token=nope", ""); status != http.StatusGone {
		t.Errorf("bad enrollment link: %d, want 410", status)
	}
	if status, body, _ := h.do("GET", "/finnes-ikke", ""); status != http.StatusNotFound || !strings.Contains(body, "Fant ikke siden") {
		t.Errorf("404 page: %d", status)
	}
	if status, _, hdr := h.do("GET", "/innstillinger", "", "HX-Request", "true"); status != http.StatusUnauthorized || hdr.Get("HX-Redirect") != "/logg-inn" {
		t.Errorf("htmx request while logged out: %d %v", status, hdr)
	}
	status, _, hdr = h.do("GET", "/static/app.css?v=x", "")
	if status != http.StatusOK || !strings.Contains(hdr.Get("Cache-Control"), "immutable") {
		t.Errorf("static asset: %d %v", status, hdr.Get("Cache-Control"))
	}
}

// do2 is do without the response headers.
func (h *harness) do2(method, path, body string) (int, string) {
	status, b, _ := h.do(method, path, body)
	return status, b
}
