// Package provider defines the contract every grant target implements.
package provider

import (
	"context"
	"time"
)

// Key identifies one privilege on a target, independent of any grant.
type Key struct {
	Target    string // Target and the other names retain the original snapshot encoding.
	Principal string
	Role      string
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
	Key     Key    `json:"key"`
	Ref     string `json:"ref"`
	GrantID string `json:"grant_id,omitempty"`
	Broken  bool   `json:"broken,omitempty"` // owned object exists with unexpected memberships or flags
}

// Caps describes what a provider can guarantee.
type Caps struct {
	// NativeExpiry means the target ends the entitlement at ExpiresAt even
	// while the broker is down, including any session scope the provider promises.
	// Password/certificate authentication expiry alone is not sufficient.
	// PostgreSQL VALID UNTIL must not set this capability by itself.
	NativeExpiry bool
}

// Provider applies, removes and observes privileges on one kind of target.
// Grant and Revoke must be idempotent per GrantID. Revoke must also handle
// a lost Grant result: resolve an empty Ref by GrantID/observation, or return
// an error so cleanup is retried; a missing local Ref is not proof of absence.
type Provider interface {
	Grant(ctx context.Context, g GrantSpec) (Handle, error)
	Revoke(ctx context.Context, h Handle) error
	Observe(ctx context.Context, target string) ([]Actual, error)
	Capabilities() Caps
}
