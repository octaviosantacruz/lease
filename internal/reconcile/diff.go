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
// GrantID separa dos leases simultáneos para el mismo permiso. Si un adaptador
// antiguo no devuelve ID, sólo puede compararse el permiso agregado por Key.
func Diff(expected []Expected, actual []provider.Actual, now time.Time) Plan {
	var plan Plan
	present := make(map[string]bool)
	for _, a := range actual {
		allowed := false
		for _, e := range expected {
			if !a.Broken && e.Key == a.Key && (a.GrantID == "" || a.GrantID == e.GrantID) && now.Before(e.ExpiresAt) {
				allowed = true
				present[e.GrantID] = true
			}
		}
		if !allowed {
			plan.Revoke = append(plan.Revoke, a)
		}
	}
	for _, e := range expected {
		if now.Before(e.ExpiresAt) && !present[e.GrantID] {
			plan.Missing = append(plan.Missing, e)
		}
	}
	return plan
}
