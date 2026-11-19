package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
)

// softAuthenticator is a minimal platform authenticator for tests: one P-256 key per
// credential, "none" attestation, and backup-eligible/backed-up flags like an iCloud
// Keychain passkey. It produces exactly the JSON that credential.toJSON() does in a
// browser, so the server is tested against real WebAuthn messages.
type softAuthenticator struct {
	t      *testing.T
	origin string
	rpID   string
	creds  map[string]*softCred // by base64url credential ID
}

type softCred struct {
	id         []byte
	key        *ecdsa.PrivateKey
	userHandle []byte
	signCount  uint32
}

func newSoftAuthenticator(t *testing.T, origin, rpID string) *softAuthenticator {
	return &softAuthenticator{t: t, origin: origin, rpID: rpID, creds: map[string]*softCred{}}
}

var b64 = base64.RawURLEncoding

const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

// Minimal CBOR encoding (RFC 8949), just enough for attestation objects and COSE keys.
func cborHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 1<<8:
		return []byte{major<<5 | 24, byte(n)}
	case n < 1<<16:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	default:
		return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(n))
	}
}
func cborInt(i int64) []byte {
	if i >= 0 {
		return cborHead(0, uint64(i))
	}
	return cborHead(1, uint64(-1-i))
}
func cborBytes(b []byte) []byte { return append(cborHead(2, uint64(len(b))), b...) }
func cborText(s string) []byte  { return append(cborHead(3, uint64(len(s))), s...) }
func cborMap(kv ...[]byte) []byte {
	out := cborHead(5, uint64(len(kv)/2))
	for _, p := range kv {
		out = append(out, p...)
	}
	return out
}

func (a *softAuthenticator) clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

func (a *softAuthenticator) authData(flags byte, signCount uint32, attested []byte) []byte {
	rpHash := sha256.Sum256([]byte(a.rpID))
	out := append(rpHash[:], flags)
	out = binary.BigEndian.AppendUint32(out, signCount)
	return append(out, attested...)
}

// create answers a registration: options is the JSON from /passkey/register/begin.
func (a *softAuthenticator) create(options []byte) (body []byte, cred *softCred) {
	a.t.Helper()
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &opts); err != nil {
		a.t.Fatalf("creation options: %v\n%s", err, options)
	}
	userHandle, err := b64.DecodeString(opts.PublicKey.User.ID)
	if err != nil {
		a.t.Fatalf("user.id: %v", err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cred = &softCred{id: make([]byte, 16), key: key, userHandle: userHandle}
	rand.Read(cred.id)
	a.creds[b64.EncodeToString(cred.id)] = cred

	pub, _ := key.PublicKey.ECDH()
	raw := pub.Bytes() // 0x04 || X || Y
	cose := cborMap(cborInt(1), cborInt(2), cborInt(3), cborInt(-7), cborInt(-1), cborInt(1),
		cborInt(-2), cborBytes(raw[1:33]), cborInt(-3), cborBytes(raw[33:]))
	attested := append(make([]byte, 16), byte(len(cred.id)>>8), byte(len(cred.id))) // zero AAGUID
	attested = append(append(attested, cred.id...), cose...)
	authData := a.authData(flagUP|flagUV|flagBE|flagBS|flagAT, 0, attested)
	attObj := cborMap(cborText("fmt"), cborText("none"), cborText("attStmt"), cborMap(), cborText("authData"), cborBytes(authData))

	body, _ = json.Marshal(map[string]any{
		"id": b64.EncodeToString(cred.id), "rawId": b64.EncodeToString(cred.id), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(a.clientData("webauthn.create", opts.PublicKey.Challenge, a.origin)),
			"attestationObject": b64.EncodeToString(attObj),
			"transports":        []string{"internal", "hybrid"},
		},
	})
	return body, cred
}

// get answers a login with cred, claiming to run on origin. options is the JSON
// from /passkey/login/begin.
func (a *softAuthenticator) get(options []byte, cred *softCred, origin string) []byte {
	a.t.Helper()
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &opts); err != nil {
		a.t.Fatalf("request options: %v\n%s", err, options)
	}
	// Synced passkeys always report a zero signature counter.
	authData := a.authData(flagUP|flagUV|flagBE|flagBS, cred.signCount, nil)
	clientData := a.clientData("webauthn.get", opts.PublicKey.Challenge, origin)
	cdHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, cred.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(cred.id), "rawId": b64.EncodeToString(cred.id), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData),
			"authenticatorData": b64.EncodeToString(authData),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(cred.userHandle),
		},
	})
	return body
}
