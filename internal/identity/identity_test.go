package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Ozal is a test double that signs exactly like Ozal's assertion.Signer:
// EdDSA, "kid" = KeyID(public key), typ JWT.
type Ozal struct {
	Key ed25519.PrivateKey
	PEM []byte
}

func NewOzal(t testing.TB) Ozal {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return Ozal{Key: priv, PEM: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})}
}

func (o Ozal) Sign(t testing.TB, claims map[string]any) string {
	t.Helper()
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), keyID(o.Key.Public().(ed25519.PublicKey)))
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: o.Key}, opts)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(claims)
	obj, err := s.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func Claims(at time.Time, sub, id string) map[string]any {
	return map[string]any{
		"iss": "https://auth.example.test", "aud": "lease", "sub": sub, "email": sub + "@example.test",
		"name": strings.ToUpper(sub[:1]) + sub[1:], "iat": at.Unix(), "exp": at.Add(60 * time.Second).Unix(), "jti": id,
	}
}

func testConfig() Config {
	return Config{
		Issuer: "https://auth.example.test", Audience: "lease", PublicKeys: []string{"ozal.pem"},
		Origin:   "https://lease.example.test",
		Subjects: map[string]string{"ines": "user:ines", "mateo": "user:mateo"},
		Emails:   map[string]string{"Agent@Example.test": "agent:diagnostico"},
	}
}

func newVerifier(t *testing.T, o Ozal, now *time.Time) *Verifier {
	t.Helper()
	read := func(path string) ([]byte, error) {
		if path != "ozal.pem" {
			return nil, errors.New("unexpected path")
		}
		return o.PEM, nil
	}
	v, err := New(testConfig(), read, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerify(t *testing.T) {
	o := NewOzal(t)
	now := time.Unix(1_800_000_000, 0)
	v := newVerifier(t, o, &now)

	p, code := v.Verify(o.Sign(t, Claims(now, "ines", "a1")))
	if code != "" || p.Subject != "ines" || v.Principal(p) != "user:ines" {
		t.Fatalf("valid assertion: %+v %q", p, code)
	}
	if _, code := v.Verify(o.Sign(t, Claims(now, "ines", "a1"))); code != Replayed {
		t.Fatalf("replay: %q", code)
	}

	other := NewOzal(t)
	mutate := func(k string, val any) map[string]any {
		c := Claims(now, "ines", "x-"+k)
		if val == nil {
			delete(c, k)
		} else {
			c[k] = val
		}
		return c
	}
	cases := map[string]struct {
		token string
		want  string
	}{
		"missing":      {"", Missing},
		"garbage":      {"not.a.jws", Malformed},
		"unknown key":  {other.Sign(t, Claims(now, "ines", "b1")), UnknownKey},
		"issuer":       {o.Sign(t, mutate("iss", "https://evil.test")), Issuer},
		"audience":     {o.Sign(t, mutate("aud", "custodia")), Audience},
		"no subject":   {o.Sign(t, mutate("sub", " ")), Subject},
		"no id":        {o.Sign(t, mutate("jti", nil)), ID},
		"too long":     {o.Sign(t, mutate("exp", now.Add(10*time.Minute).Unix())), Lifetime},
		"expired":      {o.Sign(t, Claims(now.Add(-2*time.Minute), "ines", "c1")), Expired},
		"not yet":      {o.Sign(t, Claims(now.Add(time.Minute), "ines", "c2")), NotYet},
		"tampered sig": {tamper(o.Sign(t, Claims(now, "ines", "c3"))), Signature},
	}
	for name, c := range cases {
		if _, code := v.Verify(c.token); code != c.want {
			t.Errorf("%s: got %q, want %q", name, code, c.want)
		}
	}
}

// tamper flips one character of the signature segment.
func tamper(token string) string {
	b := []byte(token)
	i := len(b) - 5
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

func TestPrincipalMapping(t *testing.T) {
	o := NewOzal(t)
	now := time.Unix(1_800_000_000, 0)
	v := newVerifier(t, o, &now)
	cases := []struct {
		p    Person
		want string
	}{
		{Person{Subject: "mateo"}, "user:mateo"},
		{Person{Subject: "x9", Email: " agent@EXAMPLE.test "}, "agent:diagnostico"},
		{Person{Subject: "ines", Email: "agent@example.test"}, "user:ines"}, // subject wins
		{Person{Subject: "stranger", Email: "stranger@example.test"}, ""},
		{Person{Subject: "stranger"}, ""},
	}
	for _, c := range cases {
		if got := v.Principal(c.p); got != c.want {
			t.Errorf("Principal(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestNewRefusesWeakSetups(t *testing.T) {
	o := NewOzal(t)
	read := func(string) ([]byte, error) { return o.PEM, nil }
	mods := map[string]func(*Config){
		"no issuer":          func(c *Config) { c.Issuer = "" },
		"no audience":        func(c *Config) { c.Audience = "" },
		"no key":             func(c *Config) { c.PublicKeys = nil },
		"http origin":        func(c *Config) { c.Origin = "http://lease.example.test" },
		"origin with path":   func(c *Config) { c.Origin = "https://lease.example.test/app" },
		"skew too large":     func(c *Config) { c.Skew = "1m" },
		"no mappings":        func(c *Config) { c.Subjects, c.Emails = nil, nil },
		"reconciler":         func(c *Config) { c.Subjects = map[string]string{"x": "system:reconciler"} },
		"empty principal":    func(c *Config) { c.Subjects = map[string]string{"x": " "} },
		"email without at":   func(c *Config) { c.Emails = map[string]string{"nobody": "user:x"} },
		"duplicate email":    func(c *Config) { c.Emails = map[string]string{"A@x.test": "user:a", "a@x.test": "user:b"} },
		"empty subject name": func(c *Config) { c.Subjects = map[string]string{" ": "user:x"} },
	}
	for name, mod := range mods {
		c := testConfig()
		mod(&c)
		if _, err := New(c, read, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	c := testConfig()
	c.Origin = "http://lease.localhost:8080"
	if _, err := New(c, read, nil); err != nil {
		t.Errorf("http on .localhost should be allowed for local tests: %v", err)
	}
	if _, err := New(testConfig(), func(string) ([]byte, error) { return []byte("not pem"), nil }, nil); err == nil {
		t.Error("accepted a non-PEM key")
	}
}

func TestReplayCacheFailsClosedWhenFull(t *testing.T) {
	r := &replayCache{max: 2, seen: map[string]int64{}}
	if r.claim("a", 100, 10) != "" || r.claim("b", 100, 10) != "" {
		t.Fatal("first claims refused")
	}
	if r.claim("c", 100, 10) != Capacity {
		t.Fatal("full cache accepted a new id")
	}
	if r.claim("c", 200, 150) != "" {
		t.Fatal("expired ids were not evicted")
	}
}

func TestMiddleware(t *testing.T) {
	o := NewOzal(t)
	now := time.Unix(1_800_000_000, 0)
	v := newVerifier(t, o, &now)
	var got Actor
	h := Middleware(v, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = From(r.Context())
		if r.Header.Get(Header) != "" {
			t.Error("assertion forwarded downstream")
		}
		w.WriteHeader(204)
	}))
	do := func(remote string, tokens ...string) int {
		req := httptest.NewRequest("GET", "/api/state", nil)
		req.RemoteAddr = remote
		for _, tok := range tokens {
			req.Header.Add(Header, tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := do("127.0.0.1:5000", o.Sign(t, Claims(now, "ines", "m1"))); code != 204 || got.Principal != "user:ines" {
		t.Fatalf("valid: %d %+v", code, got)
	}
	if code := do("[::1]:5000", o.Sign(t, Claims(now, "mateo", "m2"))); code != 204 || got.Principal != "user:mateo" {
		t.Fatalf("ipv6 loopback: %d", code)
	}
	if code := do("192.168.1.9:5000", o.Sign(t, Claims(now, "ines", "m3"))); code != 401 {
		t.Fatalf("non-loopback peer: %d", code)
	}
	if code := do("127.0.0.1:5000"); code != 401 {
		t.Fatalf("missing assertion: %d", code)
	}
	if code := do("127.0.0.1:5000", o.Sign(t, Claims(now, "ines", "m4")), o.Sign(t, Claims(now, "mateo", "m5"))); code != 401 {
		t.Fatalf("two assertions: %d", code)
	}
	if code := do("127.0.0.1:5000", o.Sign(t, Claims(now, "stranger", "m6"))); code != 403 {
		t.Fatalf("unmapped subject: %d", code)
	}
}
