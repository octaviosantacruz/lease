// Package identity turns a request that arrived through Ozal into a Lease
// principal. Ozal authenticates the person (passkeys) and signs a short-lived
// assertion (EdDSA JWS, ≤60 s, unique jti) for this application; Lease pins
// Ozal's public key, verifies the assertion, rejects replays and maps the
// verified subject to a principal named in the policy. Nothing else proves
// identity: no header, cookie or request field is trusted by itself.
//
// The verifier follows Custodia's (server/internal/identity), which is
// exercised end to end against a real Ozal. Keep the two in step until Ozal
// publishes a verifier package both can import.
package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Header is where Ozal sends the assertion.
const Header = "X-Ozal-Assertion"

// MaxLifetime is the longest validity accepted from an assertion.
const MaxLifetime = 60 * time.Second

// Rejection codes are stable for tests and logs. The token is never logged.
const (
	Missing    = "missing"
	Malformed  = "malformed"
	UnknownKey = "unknown_key"
	Signature  = "signature"
	Issuer     = "issuer"
	Audience   = "audience"
	Expired    = "expired"
	NotYet     = "not_yet_valid"
	Lifetime   = "lifetime"
	Subject    = "subject"
	ID         = "id"
	Replayed   = "replayed"
	Capacity   = "replay_capacity"
	Unmapped   = "unmapped_subject"
)

// Person is who Ozal authorized.
type Person struct {
	Subject string   `json:"sub"`
	Email   string   `json:"email,omitempty"`
	Name    string   `json:"name,omitempty"`
	Groups  []string `json:"groups,omitempty"`
}

// En Go, incrustar un struct (Person dentro de claims) "promueve" sus campos:
// json.Unmarshal llena Subject, Email, etc. como si fueran de claims.
type claims struct {
	Person
	Issuer   string `json:"iss"`
	Audience string `json:"aud"`
	IssuedAt int64  `json:"iat"`
	Expiry   int64  `json:"exp"`
	ID       string `json:"jti"`
}

// check applies the claim rules; it returns "" or a rejection code.
func check(c claims, issuer, audience string, now time.Time, skew time.Duration) string {
	switch {
	case c.Issuer == "" || c.Issuer != issuer:
		return Issuer
	case c.Audience == "" || c.Audience != audience:
		return Audience
	case strings.TrimSpace(c.Subject) == "":
		return Subject
	case strings.TrimSpace(c.ID) == "":
		return ID
	case c.Expiry-c.IssuedAt <= 0 || time.Duration(c.Expiry-c.IssuedAt)*time.Second > MaxLifetime:
		return Lifetime
	case now.Add(skew).Unix() < c.IssuedAt:
		return NotYet
	case now.Add(-skew).Unix() >= c.Expiry:
		return Expired
	}
	return ""
}

// Config is what the operator pins, loaded from a JSON file.
type Config struct {
	Issuer   string `json:"issuer"`   // Ozal's issuer, e.g. https://auth.lab.ozal.dev
	Audience string `json:"audience"` // the identity_assertion.audience of Lease's route
	// PublicKeys are paths to PEM files from "ozal assertion public-key".
	PublicKeys []string `json:"public_keys"`
	// Origin is the public origin of Lease behind Ozal. Mutations must come
	// from it (CSRF): the browser sends Ozal's session cookie on any request.
	Origin string `json:"origin"`
	Skew   string `json:"skew,omitempty"` // default 5s, at most 30s
	// Subjects maps a stable Ozal subject to a Lease principal. Emails maps a
	// verified email when the subject is not known in advance. A subject entry
	// wins over an email entry. Anyone not mapped is refused.
	Subjects map[string]string `json:"subjects,omitempty"`
	Emails   map[string]string `json:"emails,omitempty"`
	// MaxIDs bounds the replay cache (default 100000); when full, new
	// assertions are refused until old ones expire (fail closed).
	MaxIDs int `json:"max_ids,omitempty"`
}

// Load reads and validates a configuration file strictly.
func Load(path string) (Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("identity config: %w", err)
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("identity config: trailing JSON")
	}
	return c, nil
}

// Verifier checks assertions and maps people to principals. It is safe for
// concurrent use.
type Verifier struct {
	keys     map[string]ed25519.PublicKey
	issuer   string
	audience string
	origin   string
	skew     time.Duration
	subjects map[string]string
	emails   map[string]string
	now      func() time.Time
	replay   *replayCache
}

// New builds a verifier. readFile loads the pinned keys (os.ReadFile in
// production); now is the clock (time.Now in production).
func New(c Config, readFile func(string) ([]byte, error), now func() time.Time) (*Verifier, error) {
	if c.Issuer == "" || c.Audience == "" {
		return nil, errors.New("identity: issuer and audience are required")
	}
	if len(c.PublicKeys) == 0 {
		return nil, errors.New("identity: pin at least one Ozal public key")
	}
	origin, err := url.Parse(c.Origin)
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.User != nil {
		return nil, errors.New("identity: origin must be scheme://host[:port] with nothing after it")
	}
	if origin.Scheme != "https" && !strings.HasSuffix(origin.Hostname(), ".localhost") {
		return nil, errors.New("identity: origin must be https (http only for *.localhost)")
	}
	skew := 5 * time.Second
	if c.Skew != "" {
		if skew, err = time.ParseDuration(c.Skew); err != nil {
			return nil, errors.New("identity: invalid skew")
		}
	}
	if skew < 0 || skew > 30*time.Second {
		return nil, errors.New("identity: clock skew must be between 0 and 30 seconds")
	}
	if len(c.Subjects) == 0 && len(c.Emails) == 0 {
		return nil, errors.New("identity: map at least one Ozal subject or email to a principal")
	}
	subjects := map[string]string{}
	for sub, p := range c.Subjects {
		if strings.TrimSpace(sub) == "" || !validPrincipal(p) {
			return nil, fmt.Errorf("identity: invalid subject mapping %q", sub)
		}
		subjects[sub] = p
	}
	emails := map[string]string{}
	for e, p := range c.Emails {
		key := normalizeEmail(e)
		if !strings.Contains(key, "@") || !validPrincipal(p) {
			return nil, fmt.Errorf("identity: invalid email mapping %q", e)
		}
		if _, dup := emails[key]; dup {
			return nil, fmt.Errorf("identity: email %q mapped twice", e)
		}
		emails[key] = p
	}
	keys := map[string]ed25519.PublicKey{}
	for _, path := range c.PublicKeys {
		data, err := readFile(path)
		if err != nil {
			return nil, fmt.Errorf("identity: read Ozal public key: %w", err)
		}
		key, err := parsePublicKey(data)
		if err != nil {
			return nil, err
		}
		keys[keyID(key)] = key
	}
	maxIDs := c.MaxIDs
	if maxIDs <= 0 {
		maxIDs = 100_000
	}
	if now == nil {
		now = time.Now
	}
	return &Verifier{
		keys: keys, issuer: c.Issuer, audience: c.Audience, origin: origin.Scheme + "://" + origin.Host,
		skew: skew, subjects: subjects, emails: emails, now: now,
		replay: &replayCache{max: maxIDs, seen: map[string]int64{}},
	}, nil
}

// Origin is the configured public origin.
func (v *Verifier) Origin() string { return v.origin }

// Verify checks the token and returns the person, or a rejection code.
func (v *Verifier) Verify(token string) (Person, string) {
	if token == "" {
		return Person{}, Missing
	}
	signed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(signed.Signatures) != 1 {
		return Person{}, Malformed
	}
	key, ok := v.keys[signed.Signatures[0].Header.KeyID]
	if !ok {
		return Person{}, UnknownKey
	}
	payload, err := signed.Verify(key)
	if err != nil {
		return Person{}, Signature
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Person{}, Malformed
	}
	now := v.now()
	if code := check(c, v.issuer, v.audience, now, v.skew); code != "" {
		return Person{}, code
	}
	if code := v.replay.claim(c.ID, c.Expiry+int64(v.skew/time.Second), now.Unix()); code != "" {
		return Person{}, code
	}
	c.Person.Groups = slices.Clone(c.Person.Groups)
	return c.Person, ""
}

// Principal maps a verified person to a Lease principal, or returns "".
func (v *Verifier) Principal(p Person) string {
	if principal, ok := v.subjects[p.Subject]; ok {
		return principal
	}
	if e := normalizeEmail(p.Email); e != "" {
		return v.emails[e]
	}
	return ""
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// validPrincipal mirrors the policy rules: non-empty and never the internal
// reconciler actor.
func validPrincipal(p string) bool {
	return strings.TrimSpace(p) != "" && p != "system:reconciler" && len(p) <= 200
}

// replayCache remembers assertion IDs until they expire.
type replayCache struct {
	mu   sync.Mutex
	max  int
	seen map[string]int64
}

func (r *replayCache) claim(id string, until, now int64) string {
	// defer ejecuta Unlock al salir de la función, por cualquier return.
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen[id]; ok {
		return Replayed
	}
	if len(r.seen) >= r.max {
		for k, exp := range r.seen {
			if exp < now {
				delete(r.seen, k)
			}
		}
		if len(r.seen) >= r.max {
			return Capacity
		}
	}
	r.seen[id] = until
	return ""
}

// keyID matches Ozal's assertion.KeyID: hex of the first 16 bytes of
// SHA-256 over the raw public key.
func keyID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:16])
}

func parsePublicKey(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("identity: an Ozal public key must be a PEM PUBLIC KEY")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("identity: an Ozal public key cannot be parsed")
	}
	// Una "type assertion" (parsed.(ed25519.PublicKey)) con dos valores no
	// entra en pánico: ok es false si la clave es de otro tipo (RSA, ECDSA).
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("identity: an Ozal public key must be Ed25519")
	}
	return key, nil
}
