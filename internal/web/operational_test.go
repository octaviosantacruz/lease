package web

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/identity"
	"github.com/octaviosantacruz/lease/internal/store"
)

const testOrigin = "https://lease.example.test"

// fakeOzal signs assertions the way Ozal's assertion.Signer does.
type fakeOzal struct {
	key ed25519.PrivateKey
	n   atomic.Int64
}

func (o *fakeOzal) pem(t *testing.T) []byte {
	der, err := x509.MarshalPKIXPublicKey(o.key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func (o *fakeOzal) assert(t *testing.T, sub, email string) string {
	t.Helper()
	pub := o.key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), hex.EncodeToString(sum[:16]))
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: o.key}, opts)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	payload, _ := json.Marshal(map[string]any{
		"iss": "https://auth.example.test", "aud": "lease", "sub": sub, "email": email, "name": sub,
		"iat": now.Unix(), "exp": now.Add(60 * time.Second).Unix(), "jti": fmt.Sprintf("jti-%d", o.n.Add(1)),
	})
	obj, err := s.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := obj.CompactSerialize()
	return tok
}

func operationalApp(t *testing.T) (*App, *fakeOzal) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	o := &fakeOzal{key: priv}
	v, err := identity.New(identity.Config{
		Issuer: "https://auth.example.test", Audience: "lease", PublicKeys: []string{"k"}, Origin: testOrigin,
		Subjects: map[string]string{"ines-sub": "user:ines", "mateo-sub": "user:mateo"},
		Emails:   map[string]string{"diag@example.test": "agent:diagnostico"},
	}, func(string) ([]byte, error) { return o.pem(t), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ops.sqlite")
	ctx := context.Background()
	if err := store.Migrate(ctx, path); err != nil {
		t.Fatal(err)
	}
	r, err := broker.Open(ctx, path, broker.DemoConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	if err = r.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := NewOperational(r.Broker, v)
	if err != nil {
		t.Fatal(err)
	}
	return a, o
}

type call struct {
	method, path, token, origin, remote string
	body                                any
	noMarker                            bool
}

func (c call) do(t *testing.T, a *App) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if c.body != nil {
		payload, _ = json.Marshal(c.body)
	}
	r := httptest.NewRequest(c.method, "http://127.0.0.1:8790"+c.path, bytes.NewReader(payload))
	r.RemoteAddr = "127.0.0.1:40000"
	if c.remote != "" {
		r.RemoteAddr = c.remote
	}
	if c.token != "" {
		r.Header.Set(identity.Header, c.token)
	}
	if c.method == "POST" {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", testOrigin)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if !c.noMarker {
			r.Header.Set("X-Lease-Request", "1")
		}
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func TestOperationalWorkflow(t *testing.T) {
	a, o := operationalApp(t)
	ines := func() string { return o.assert(t, "ines-sub", "ines@example.test") }
	mateo := func() string { return o.assert(t, "mateo-sub", "mateo@example.test") }
	req := map[string]any{"entitlement": "analytics-read", "reason": "INC-284", "seconds": 60}

	// The browser cannot name the actor.
	w := call{method: "POST", path: "/api/request", token: ines(), body: map[string]any{"actor": "user:mateo", "entitlement": "analytics-read", "reason": "x", "seconds": 60}}.do(t, a)
	if w.Code != 400 {
		t.Fatalf("actor in body: %d %s", w.Code, w.Body)
	}
	// Cross-origin and marker-less mutations are refused even with a valid identity.
	if w := (call{method: "POST", path: "/api/request", token: ines(), body: req, origin: "https://evil.test"}).do(t, a); w.Code != 403 {
		t.Fatalf("cross origin: %d", w.Code)
	}
	if w := (call{method: "POST", path: "/api/request", token: ines(), body: req, noMarker: true}).do(t, a); w.Code != 403 {
		t.Fatalf("missing marker: %d", w.Code)
	}

	w = call{method: "POST", path: "/api/request", token: ines(), body: req}.do(t, a)
	if w.Code != 200 {
		t.Fatalf("request: %d %s", w.Code, w.Body)
	}
	var g store.Grant
	json.Unmarshal(w.Body.Bytes(), &g)
	if g.Key.Principal != "user:ines" {
		t.Fatalf("grant principal = %q, want the verified actor", g.Key.Principal)
	}

	// Self-approval is refused by the policy with the verified actor.
	if w := (call{method: "POST", path: "/api/approve", token: ines(), body: map[string]any{"id": g.ID}}).do(t, a); w.Code != 403 {
		t.Fatalf("self approval: %d", w.Code)
	}
	if w := (call{method: "POST", path: "/api/approve", token: mateo(), body: map[string]any{"id": g.ID}}).do(t, a); w.Code != 200 {
		t.Fatalf("approval by mateo: %d %s", w.Code, w.Body)
	}
}

func TestOperationalVisibility(t *testing.T) {
	a, o := operationalApp(t)
	req := map[string]any{"entitlement": "payments-read", "reason": "INC-9", "seconds": 60}
	if w := (call{method: "POST", path: "/api/request", token: o.assert(t, "ines-sub", ""), body: req}).do(t, a); w.Code != 200 {
		t.Fatalf("request: %d %s", w.Code, w.Body)
	}
	state := func(token string) (map[string]any, View) {
		w := call{method: "GET", path: "/api/state", token: token}.do(t, a)
		if w.Code != 200 {
			t.Fatalf("state: %d %s", w.Code, w.Body)
		}
		var raw map[string]json.RawMessage
		json.Unmarshal(w.Body.Bytes(), &raw)
		var v View
		json.Unmarshal(raw["snapshot"], &v.Snapshot)
		json.Unmarshal(raw["policy"], &v.Policy)
		var ident map[string]any
		json.Unmarshal(raw["identity"], &ident)
		return ident, v
	}
	// The agent (mapped by email) neither requests nor approves payments-read:
	// it must not see that entitlement, Inés's grant or its events.
	ident, v := state(o.assert(t, "unknown-sub", "diag@example.test"))
	if ident["principal"] != "agent:diagnostico" {
		t.Fatalf("identity = %v", ident)
	}
	for _, e := range v.Policy {
		if e.ID == "payments-read" {
			t.Fatal("agent sees an entitlement outside its policy")
		}
	}
	if len(v.Snapshot.Grants) != 0 || len(v.Snapshot.Events) != 0 {
		t.Fatalf("agent sees %d grants / %d events of others", len(v.Snapshot.Grants), len(v.Snapshot.Events))
	}
	// The approver sees it.
	if _, v := state(o.assert(t, "mateo-sub", "")); len(v.Snapshot.Grants) != 1 || len(v.Snapshot.Events) == 0 {
		t.Fatalf("approver view: %d grants, %d events", len(v.Snapshot.Grants), len(v.Snapshot.Events))
	}
}

func TestOperationalIdentityBoundary(t *testing.T) {
	a, o := operationalApp(t)
	tok := o.assert(t, "ines-sub", "")
	cases := map[string]call{
		"no assertion":      {method: "GET", path: "/api/state"},
		"not through ozal":  {method: "GET", path: "/api/state", token: o.assert(t, "ines-sub", ""), remote: "10.0.0.5:1234"},
		"static asset bare": {method: "GET", path: "/console/"},
	}
	for name, c := range cases {
		if w := c.do(t, a); w.Code != 401 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if w := (call{method: "GET", path: "/api/state", token: tok}).do(t, a); w.Code != 200 {
		t.Fatalf("first use: %d", w.Code)
	}
	if w := (call{method: "GET", path: "/api/state", token: tok}).do(t, a); w.Code != 401 {
		t.Fatalf("replayed assertion: %d", w.Code)
	}
	if w := (call{method: "GET", path: "/api/state", token: o.assert(t, "stranger", "x@example.test")}).do(t, a); w.Code != 403 {
		t.Fatalf("unmapped person: %d", w.Code)
	}
	if w := (call{method: "GET", path: "/console/", token: o.assert(t, "ines-sub", "")}).do(t, a); w.Code != 200 {
		t.Fatalf("console with identity: %d", w.Code)
	}
}

func TestOperationalAllowsRealProviders(t *testing.T) {
	// The lab constructor refuses non-lab entitlements; the operational one
	// must not, since verified identity is what makes real targets acceptable.
	if _, err := NewOperational(&broker.Broker{Config: broker.Config{Entitlements: []broker.Entitlement{{Provider: "postgres"}}}}, nil); err == nil {
		t.Fatal("NewOperational accepted a nil verifier")
	}
	if _, err := New(&broker.Broker{Config: broker.Config{Entitlements: []broker.Entitlement{{Provider: "postgres"}}}}, "127.0.0.1:1"); err == nil {
		t.Fatal("lab console accepted a real provider")
	}
}
