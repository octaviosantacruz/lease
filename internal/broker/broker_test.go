package broker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/octaviosantacruz/lease/internal/grant"
	"github.com/octaviosantacruz/lease/internal/provider"
	"github.com/octaviosantacruz/lease/internal/store"
)

var testNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*Runtime, *time.Time, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broker.sqlite")
	ctx := context.Background()
	if err := store.Migrate(ctx, path); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, path, DemoConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	now := testNow
	r.Now = func() time.Time { return now }
	if err = r.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	return r, &now, path
}
func requested(t *testing.T, b *Broker) store.Grant {
	t.Helper()
	g, err := b.Request(context.Background(), "analytics-read", "user:ines", "INC-284", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func TestLifecyclePolicyAndExpiry(t *testing.T) {
	r, now, _ := setup(t)
	ctx := context.Background()
	if _, err := r.Request(ctx, "analytics-read", "user:intruder", "ticket", time.Minute); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := r.Request(ctx, "analytics-read", "user:ines", "ticket", time.Hour); !errors.Is(err, store.ErrInvalid) {
		t.Fatal(err)
	}
	g := requested(t, r.Broker)
	for _, actor := range []string{"user:ines", "user:intruder"} {
		if _, err := r.Approve(ctx, g.ID, actor); !errors.Is(err, ErrForbidden) {
			t.Fatal(actor, err)
		}
	}
	g, err := r.Approve(ctx, g.ID, "user:mateo")
	if err != nil || g.State != grant.Active {
		t.Fatal(g, err)
	}
	actual, err := r.Providers["lab"].Observe(ctx, g.Key.Target)
	if err != nil || len(actual) != 1 {
		t.Fatal(actual, err)
	}
	*now = g.ExpiresAt
	if _, err = r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	g, err = r.Store.Get(ctx, g.ID)
	if err != nil || g.State != grant.Revoked {
		t.Fatal(g, err)
	}
	actual, err = r.Providers["lab"].Observe(ctx, g.Key.Target)
	if err != nil || len(actual) != 0 {
		t.Fatal(actual, err)
	}
	if err = r.Store.Verify(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestPendingExpiryNeverGrants(t *testing.T) {
	r, now, _ := setup(t)
	g := requested(t, r.Broker)
	*now = g.ExpiresAt
	if _, err := r.Approve(context.Background(), g.ID, "user:mateo"); err == nil {
		t.Fatal("approved expired request")
	}
	if _, err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _ = r.Store.Get(context.Background(), g.ID)
	if g.State != grant.Denied {
		t.Fatal(g)
	}
}
func TestUnknownGrantAfterRestartIsRevoked(t *testing.T) {
	r, _, path := setup(t)
	ctx := context.Background()
	g := requested(t, r.Broker)
	var err error
	for _, ev := range []grant.Event{grant.Approve, grant.StartGrant} {
		g, err = r.advance(ctx, g, ev, "user:mateo", "")
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = r.Providers["lab"].Grant(ctx, provider.GrantSpec{GrantID: g.ID, Key: g.Key, ExpiresAt: g.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	// Resultado perdido antes de Active: el destino conserva el permiso, el libro Granting.
	r.Close()
	r.close = nil
	recovered, err := Open(ctx, path, DemoConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	recovered.Now = func() time.Time { return testNow.Add(time.Second) }
	if _, err = recovered.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	g, err = recovered.Store.Get(ctx, g.ID)
	if err != nil || g.State != grant.Revoked {
		t.Fatal(g, err)
	}
}

type faulty struct {
	provider.Provider
	revokeError, grantError, observeError bool
	calls                                 int
}

func (f *faulty) Revoke(ctx context.Context, h provider.Handle) error {
	f.calls++
	if f.revokeError {
		return errors.New("target unavailable")
	}
	return f.Provider.Revoke(ctx, h)
}
func (f *faulty) Grant(ctx context.Context, g provider.GrantSpec) (provider.Handle, error) {
	h, err := f.Provider.Grant(ctx, g)
	if err == nil && f.grantError {
		return provider.Handle{}, errors.New("lost response after apply")
	}
	return h, err
}
func (f *faulty) Observe(ctx context.Context, target string) ([]provider.Actual, error) {
	if f.observeError {
		return nil, errors.New("observation unavailable")
	}
	return f.Provider.Observe(ctx, target)
}
func TestUnknownApplyAndFailedRevokeStayRecoverable(t *testing.T) {
	r, now, _ := setup(t)
	ctx := context.Background()
	f := &faulty{Provider: r.Providers["lab"], grantError: true, revokeError: true}
	r.Providers["lab"] = f
	g := requested(t, r.Broker)
	if _, err := r.Approve(ctx, g.ID, "user:mateo"); err == nil {
		t.Fatal("unknown success not reported")
	}
	g, _ = r.Store.Get(ctx, g.ID)
	if g.State != grant.Revoking {
		t.Fatal(g)
	}
	*now = now.Add(time.Second)
	f.revokeError = false
	if _, err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	g, _ = r.Store.Get(ctx, g.ID)
	if g.State != grant.Revoked || f.calls < 2 {
		t.Fatal(g, f.calls)
	}
}
func TestMissingNeverRegrantedAndDriftHasDurableCleanup(t *testing.T) {
	r, _, _ := setup(t)
	ctx := context.Background()
	g := requested(t, r.Broker)
	g, err := r.Approve(ctx, g.ID, "user:mateo")
	if err != nil {
		t.Fatal(err)
	}
	p := r.Providers["lab"]
	if err = p.Revoke(ctx, provider.Handle{GrantID: g.ID, Key: g.Key, Ref: g.Ref}); err != nil {
		t.Fatal(err)
	}
	report, err := r.Reconcile(ctx)
	if err != nil || len(report.Missing) != 1 {
		t.Fatal(report, err)
	}
	if actual, _ := p.Observe(ctx, g.Key.Target); len(actual) != 0 {
		t.Fatal("silently regranted", actual)
	}
	if _, err = p.Grant(ctx, provider.GrantSpec{GrantID: "orphan", Key: g.Key, ExpiresAt: g.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	report, err = r.Reconcile(ctx)
	if err != nil || len(report.Drift) != 1 {
		t.Fatal(report, err)
	}
	if actual, _ := p.Observe(ctx, g.Key.Target); len(actual) != 0 {
		t.Fatal("orphan remains", actual)
	}
	snap, err := r.Store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range snap.Grants {
		if entry.Ref == "orphan" {
			found = true
			if entry.State != grant.Revoked {
				t.Fatal(entry)
			}
		}
	}
	if !found {
		t.Fatal("no cleanup evidence")
	}
}
func TestFailedObservationDoesNotProveRevocation(t *testing.T) {
	r, _, _ := setup(t)
	ctx := context.Background()
	g := requested(t, r.Broker)
	g, err := r.Approve(ctx, g.ID, "user:mateo")
	if err != nil {
		t.Fatal(err)
	}
	f := &faulty{Provider: r.Providers["lab"], observeError: true}
	r.Providers["lab"] = f
	if _, err = r.Revoke(ctx, g.ID, "user:mateo"); err == nil {
		t.Fatal("unobserved revoke accepted")
	}
	g, _ = r.Store.Get(ctx, g.ID)
	if g.State != grant.Revoking {
		t.Fatal(g)
	}
	f.observeError = false
	if _, err = r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBrokenOwnedObjectIsRevoked(t *testing.T) {
	r, _, _ := setup(t)
	ctx := context.Background()
	g := requested(t, r.Broker)
	g, err := r.Approve(ctx, g.ID, "user:mateo")
	if err != nil {
		t.Fatal(err)
	}
	r.Providers["lab"] = &broken{Provider: r.Providers["lab"]}
	report, err := r.Reconcile(ctx)
	if err != nil || len(report.Drift) != 1 || len(report.Missing) != 1 {
		t.Fatal(report, err)
	}
	g, err = r.Store.Get(ctx, g.ID)
	if err != nil || g.State != grant.Revoked {
		t.Fatal(g, err)
	}
}

type broken struct{ provider.Provider }

func (p *broken) Observe(ctx context.Context, target string) ([]provider.Actual, error) {
	actual, err := p.Provider.Observe(ctx, target)
	for i := range actual {
		actual[i].Broken = true
	}
	return actual, err
}
