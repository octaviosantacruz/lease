// Package grant holds the grant lifecycle as a pure state machine.
package grant

import "errors"

// State is the lifecycle position of a grant.
type State string

const (
	Requested       State = "requested"
	PendingApproval State = "pending_approval"
	Approved        State = "approved"
	Denied          State = "denied"
	Granting        State = "granting"
	Active          State = "active"
	Revoking        State = "revoking"
	Revoked         State = "revoked"
)

// Event moves a grant from one state to another.
type Event string

const (
	NeedsApproval Event = "needs_approval"
	AutoApprove   Event = "auto_approve"
	Approve       Event = "approve"
	Deny          Event = "deny"
	StartGrant    Event = "start_grant"
	Granted       Event = "granted"
	Fail          Event = "fail"
	Expire        Event = "expire"
	Revoke        Event = "revoke"
	RevokeDone    Event = "revoked"
)

// ErrInvalidTransition is returned for any (state, event) pair not in the table.
var ErrInvalidTransition = errors.New("grant: invalid transition")

// Terminal reports whether no further event is accepted.
func (s State) Terminal() bool {
	return s == Denied || s == Revoked
}

// Next returns the state reached from `from` on `ev`, or ErrInvalidTransition.
//
// LEASE-001 — the maintainer writes this. The table is in
// docs/architecture/design.md and the expected behavior in state_test.go.
// Hint: a map keyed by a small struct {from State; ev Event} reads well in Go;
// remember a failed grant goes to Revoking (fail closed).
func Next(from State, ev Event) (State, error) {
	return from, ErrInvalidTransition
}
