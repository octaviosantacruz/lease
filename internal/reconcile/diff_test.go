package reconcile

import (
	"reflect"
	"testing"
	"time"

	"github.com/octaviosantacruz/lease/internal/provider"
)

var (
	now     = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	keyRO   = provider.Key{Target: "postgres/lab", Principal: "user:octavio", Role: "readonly"}
	keyRW   = provider.Key{Target: "postgres/lab", Principal: "agent:triage", Role: "readwrite"}
	actRO   = provider.Actual{Key: keyRO, Ref: "lease_ro_1"}
	actRW   = provider.Actual{Key: keyRW, Ref: "lease_rw_1"}
	active  = Expected{GrantID: "g1", Key: keyRO, ExpiresAt: now.Add(time.Hour)}
	expired = Expected{GrantID: "g2", Key: keyRO, ExpiresAt: now.Add(-time.Minute)}
)

func TestDiff(t *testing.T) {
	tests := []struct {
		name     string
		expected []Expected
		actual   []provider.Actual
		want     Plan
	}{
		{"nothing", nil, nil, Plan{}},
		{"active and present", []Expected{active}, []provider.Actual{actRO}, Plan{}},
		{"standing privilege", nil, []provider.Actual{actRW}, Plan{Revoke: []provider.Actual{actRW}}},
		{"expired still present", []Expected{expired}, []provider.Actual{actRO}, Plan{Revoke: []provider.Actual{actRO}}},
		{"active but missing", []Expected{active}, nil, Plan{Missing: []Expected{active}}},
		{"expired and gone", []Expected{expired}, nil, Plan{}},
		{
			"expires exactly now is expired",
			[]Expected{{GrantID: "g3", Key: keyRO, ExpiresAt: now}},
			[]provider.Actual{actRO},
			Plan{Revoke: []provider.Actual{actRO}},
		},
		{
			"mixed",
			[]Expected{active},
			[]provider.Actual{actRO, actRW},
			Plan{Revoke: []provider.Actual{actRW}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diff(tt.expected, tt.actual, now)
			if !samePlan(got, tt.want) {
				t.Errorf("Diff() = %+v; want %+v", got, tt.want)
			}
		})
	}
}

// samePlan treats nil and empty slices as equal so the implementation may use
// either.
func samePlan(a, b Plan) bool {
	norm := func(p Plan) Plan {
		if len(p.Revoke) == 0 {
			p.Revoke = nil
		}
		if len(p.Missing) == 0 {
			p.Missing = nil
		}
		return p
	}
	return reflect.DeepEqual(norm(a), norm(b))
}
