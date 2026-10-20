// Package provider defines the contract every grant target implements.
package provider

import (
	"context"
	"time"
)

// Key identifies one privilege on a target, independent of any grant.
type Key struct {
	Target    string // e.g. "postgres/custodia"
	Principal string // e.g. "user:octavio" or "agent:triage"
	Role      string // e.g. "readonly"
}

// GrantSpec is what the broker asks a provider to apply.
type GrantSpec struct {
	GrantID   string // idempotency key: the same ID never grants twice
	Key       Key
	ExpiresAt time.Time
}

// Handle is the provider's reference to an applied grant, stored by the broker
// so the grant can be revoked later.
type Handle struct {
	GrantID string
	Key     Key
	Ref     string // provider-specific, e.g. the ephemeral role name
}

// Actual is a privilege that exists on the target right now.
type Actual struct {
	Key Key
	Ref string
}

// Caps describes what a provider can guarantee.
type Caps struct {
	// NativeExpiry means the target itself ends the access at ExpiresAt, even
	// if the broker is down (PostgreSQL VALID UNTIL, SSH certificate validity).
	NativeExpiry bool
}

// Provider applies, removes and observes privileges on one kind of target.
// Grant and Revoke must be idempotent.
type Provider interface {
	Grant(ctx context.Context, g GrantSpec) (Handle, error)
	Revoke(ctx context.Context, h Handle) error
	Observe(ctx context.Context, target string) ([]Actual, error)
	Capabilities() Caps
}
