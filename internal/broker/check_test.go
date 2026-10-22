package broker

import (
	"context"
	"testing"
)

func TestCheckDetectsUnavailableProviderAndDoesNotWriteEvidence(t *testing.T) {
	r, _, _ := setup(t)
	ctx := context.Background()
	before, err := r.Store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	report, err := r.Check(ctx)
	if err != nil || report.Audit != "ok" || len(report.Providers) != 1 || report.Providers[0] != "lab" {
		t.Fatal(report, err)
	}
	delete(r.Providers, "lab")
	if _, err = r.Check(ctx); err == nil {
		t.Fatal("missing provider reported healthy")
	}
	after, err := r.Store.Snapshot(ctx)
	if err != nil || before.Head != after.Head || len(after.Grants) != len(before.Grants) {
		t.Fatal("preflight changed evidence", err)
	}
}
