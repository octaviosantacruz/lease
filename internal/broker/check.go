package broker

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type CheckReport struct {
	Audit     string   `json:"audit"`
	Providers []string `json:"providers"`
}

// Check validates existing local evidence and provider prerequisites without
// changing grants. It does not prove production readiness or visitor identity.
func (b *Broker) Check(ctx context.Context) (CheckReport, error) {
	report := CheckReport{Providers: []string{}}
	if err := b.Store.Verify(ctx); err != nil {
		return report, err
	}
	report.Audit = "ok"
	var failures []error
	seen := map[string]bool{}
	for _, e := range b.Config.Entitlements {
		name := e.Provider
		if seen[name] {
			continue
		}
		seen[name] = true
		p := b.Providers[name]
		if p == nil {
			failures = append(failures, fmt.Errorf("broker: provider %s unavailable", name))
			continue
		}
		if checkable, ok := p.(interface{ Check(context.Context) error }); ok {
			if err := checkable.Check(ctx); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		report.Providers = append(report.Providers, name)
	}
	sort.Strings(report.Providers)
	return report, errors.Join(failures...)
}
