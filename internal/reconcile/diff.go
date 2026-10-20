// Package reconcile compares expected grants with the privileges that actually
// exist on a target. It is pure: no I/O, and the clock is passed in.
package reconcile

import (
	"time"

	"github.com/octaviosantacruz/lease/internal/provider"
)

// Expected is a grant the broker believes is in force.
type Expected struct {
	GrantID   string
	Key       provider.Key
	ExpiresAt time.Time
}

// Plan is what the reconciler must do.
type Plan struct {
	// Revoke holds privileges that must not exist: no expected grant (standing
	// privilege or drift) or an expired grant still present.
	Revoke []provider.Actual
	// Missing holds active expected grants absent from the target. They are
	// reported, never re-granted automatically.
	Missing []Expected
}

// Diff builds the plan. A grant with ExpiresAt equal to now is expired.
//
// LEASE-002 — the maintainer writes this. Cases are in
// docs/architecture/design.md and diff_test.go.
// Hint: index the expected grants in a map[provider.Key]Expected first; a
// struct of comparable fields can be a map key. Use now.Before(e.ExpiresAt)
// for "still active".
func Diff(expected []Expected, actual []provider.Actual, now time.Time) Plan {
	return Plan{}
}
