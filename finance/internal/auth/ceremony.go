package auth

import (
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
)

// ceremony is the server half of an in-flight WebAuthn registration or login: the
// challenge (inside session) and what the ceremony is for. Kept in memory for a few
// minutes; a restart only interrupts logins that are in progress.
type ceremony struct {
	session    webauthn.SessionData
	register   bool
	user       ledger.UserID // registration only
	enrollHash []byte        // registration through an enrollment link
	expires    time.Time
}

const maxCeremonies = 1000

type ceremonies struct {
	mu sync.Mutex
	m  map[string]ceremony
}

func newCeremonies() *ceremonies { return &ceremonies{m: map[string]ceremony{}} }

// put stores c and returns the key to hand to the browser, or "" if too many
// ceremonies are in flight (only possible if someone is hammering the endpoint).
func (cs *ceremonies) put(c ceremony, now time.Time) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for k, v := range cs.m {
		if now.After(v.expires) {
			delete(cs.m, k)
		}
	}
	if len(cs.m) >= maxCeremonies {
		return ""
	}
	key, _ := newToken()
	cs.m[key] = c
	return key
}

// take removes and returns the ceremony for key; each ceremony can finish once.
func (cs *ceremonies) take(key string, now time.Time) (ceremony, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c, ok := cs.m[key]
	delete(cs.m, key)
	if !ok || now.After(c.expires) {
		return ceremony{}, false
	}
	return c, true
}
