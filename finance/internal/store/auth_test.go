package store

import (
	"errors"
	"testing"
	"time"
)

func TestEnrollmentTokenIsSingleUse(t *testing.T) {
	s := newStore(t)
	a, b := twoUsers(t, s)
	hash := []byte("hash-of-token-aaaaaaaaaaaaaaaaaa")
	if err := s.CreateEnrollmentToken(ctx, a.ID, hash, testNow.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	tok, err := s.LookupEnrollmentToken(ctx, hash)
	if err != nil || tok.User != a.ID || tok.Used || !tok.ExpiresAt.Equal(testNow.Add(30*time.Minute)) {
		t.Fatalf("token = %+v, %v", tok, err)
	}
	pk := Passkey{User: a.ID, CredentialID: []byte("cred-1"), PublicKey: []byte("pk"), Transports: []string{"internal"}, Flags: 0x5d, Label: "iPhone"}

	// A token can't be used for another user's passkey.
	other := pk
	other.User, other.CredentialID = b.ID, []byte("cred-x")
	if _, err := s.AddPasskey(ctx, other, hash); !errors.Is(err, ErrInvalid) {
		t.Errorf("token for A used for B: err = %v, want ErrInvalid", err)
	}
	if _, err := s.AddPasskey(ctx, pk, hash); err != nil {
		t.Fatal(err)
	}
	if tok, _ := s.LookupEnrollmentToken(ctx, hash); !tok.Used {
		t.Error("token not marked used")
	}
	second := pk
	second.CredentialID = []byte("cred-2")
	if _, err := s.AddPasskey(ctx, second, hash); !errors.Is(err, ErrInvalid) {
		t.Errorf("reused token: err = %v, want ErrInvalid", err)
	}
	if ps, _ := s.Passkeys(ctx, a.ID); len(ps) != 1 {
		t.Errorf("passkeys = %d, want 1", len(ps))
	}

	// Expired tokens are rejected, and swept when the next token is created.
	expired := []byte("hash-of-expired-token-bbbbbbbbbb")
	if err := s.CreateEnrollmentToken(ctx, a.ID, expired, testNow.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPasskey(ctx, second, expired); !errors.Is(err, ErrInvalid) {
		t.Errorf("expired token: err = %v, want ErrInvalid", err)
	}
	if err := s.CreateEnrollmentToken(ctx, a.ID, []byte("another-hash-cccccccccccccccccccc"), testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupEnrollmentToken(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired token not swept: %v", err)
	}
}

func TestPasskeyRoundTripRenameAndUse(t *testing.T) {
	s := newStore(t)
	a, b := twoUsers(t, s)
	id, err := s.AddPasskey(ctx, Passkey{User: a.ID, CredentialID: []byte{1, 2, 3}, PublicKey: []byte{4, 5}, SignCount: 7,
		Transports: []string{"internal", "hybrid"}, Flags: 0x5d, AAGUID: make([]byte, 16), Label: " iPhone "}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PasskeyByCredentialID(ctx, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != id || p.User != a.ID || p.SignCount != 7 || p.Flags != 0x5d || p.Label != "iPhone" ||
		len(p.Transports) != 2 || len(p.AAGUID) != 16 || !p.LastUsedAt.IsZero() || !p.CreatedAt.Equal(testNow) {
		t.Errorf("passkey = %+v", p)
	}
	if _, err := s.AddPasskey(ctx, Passkey{User: b.ID, CredentialID: []byte{1, 2, 3}, PublicKey: []byte{9}}, nil); err == nil {
		t.Error("duplicate credential ID accepted")
	}

	s.Now = func() time.Time { return testNow.Add(time.Hour) }
	if err := s.PasskeyUsed(ctx, id, 8, 0x1d); err != nil {
		t.Fatal(err)
	}
	if err := s.RenamePasskey(ctx, a.ID, id, "Kari sin iPhone"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenamePasskey(ctx, b.ID, id, "kapret"); !errors.Is(err, ErrNotFound) {
		t.Errorf("renaming another user's passkey: err = %v, want ErrNotFound", err)
	}
	p, _ = s.PasskeyByCredentialID(ctx, []byte{1, 2, 3})
	if p.SignCount != 8 || p.Flags != 0x1d || !p.LastUsedAt.Equal(testNow.Add(time.Hour)) || p.Label != "Kari sin iPhone" {
		t.Errorf("after use/rename: %+v", p)
	}
	if _, err := s.PasskeyByCredentialID(ctx, []byte{9}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown credential: %v", err)
	}
}

func TestSessionsAndReset(t *testing.T) {
	s := newStore(t)
	a, b := twoUsers(t, s)
	hashA, hashB := []byte("session-a"), []byte("session-b")
	for _, c := range []struct {
		hash []byte
		user User
	}{{hashA, a}, {hashB, b}} {
		if err := s.CreateSession(ctx, c.user.ID, c.hash, testNow.Add(90*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if sess, err := s.LookupSession(ctx, hashA); err != nil || sess.User != a.ID {
		t.Fatalf("session = %+v, %v", sess, err)
	}
	if err := s.ExtendSession(ctx, hashA, testNow.Add(91*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sess, _ := s.LookupSession(ctx, hashA); !sess.ExpiresAt.Equal(testNow.Add(91 * 24 * time.Hour)) {
		t.Errorf("not extended: %+v", sess)
	}

	// Past expiry the session no longer resolves.
	s.Now = func() time.Time { return testNow.Add(92 * 24 * time.Hour) }
	if _, err := s.LookupSession(ctx, hashA); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session: %v", err)
	}
	s.Now = func() time.Time { return testNow }

	if _, err := s.AddPasskey(ctx, Passkey{User: a.ID, CredentialID: []byte("c"), PublicKey: []byte("p")}, nil); err != nil {
		t.Fatal(err)
	}
	n, err := s.ResetPasskeys(ctx, a.ID)
	if err != nil || n != 1 {
		t.Fatalf("reset = %d, %v", n, err)
	}
	if _, err := s.LookupSession(ctx, hashA); !errors.Is(err, ErrNotFound) {
		t.Error("reset must end the user's sessions")
	}
	if _, err := s.LookupSession(ctx, hashB); err != nil {
		t.Error("reset must not touch the other user's sessions")
	}
	if err := s.DeleteSession(ctx, hashB); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupSession(ctx, hashB); !errors.Is(err, ErrNotFound) {
		t.Error("deleted session still resolves")
	}
}
