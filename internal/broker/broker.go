// Package broker coordinates policy, durable intent and provider side effects.
package broker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/octaviosantacruz/lease/internal/grant"
	"github.com/octaviosantacruz/lease/internal/provider"
	"github.com/octaviosantacruz/lease/internal/reconcile"
	"github.com/octaviosantacruz/lease/internal/store"
)

var ErrForbidden = errors.New("broker: actor is not allowed or cannot approve own request")

type Broker struct {
	Store     *store.Store
	Config    Config
	Providers map[string]provider.Provider
	Now       func() time.Time
	// Mutex serializa efectos dentro del proceso; LockExecutor lo hace entre procesos.
	mu sync.Mutex
}

type Report struct {
	Missing []reconcile.Expected `json:"missing"`
	Drift   []provider.Actual    `json:"drift"`
}

func (b *Broker) Initialize(ctx context.Context) error {
	if err := b.Config.Validate(); err != nil {
		return err
	}
	if _, err := b.Check(ctx); err != nil {
		return err
	}
	snapshot, err := b.Store.Snapshot(ctx)
	if err != nil {
		return err
	}
	existing := map[string]store.Entitlement{}
	for _, e := range snapshot.Entitlements {
		existing[e.ID] = e
	}
	for _, e := range b.Config.Entitlements {
		if got, ok := existing[e.ID]; ok {
			if got != e.Record() {
				return fmt.Errorf("broker: immutable entitlement %s changed; use a new ID", e.ID)
			}
			continue
		}
		if err := b.Store.CreateEntitlement(ctx, e.Record()); err != nil {
			return err
		}
	}
	return nil
}
func (b *Broker) Request(ctx context.Context, entitlement, principal, reason string, ttl time.Duration) (store.Grant, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.Config.Find(entitlement)
	if !ok || !contains(e.Requesters, principal) {
		return store.Grant{}, ErrForbidden
	}
	if ttl <= 0 || ttl > e.Record().MaxTTL {
		return store.Grant{}, store.ErrInvalid
	}
	at := b.Now().UTC()
	id := "g_" + rand.Text() // rand.Text genera un identificador impredecible sin estado compartido.
	g, err := b.Store.CreateRequest(ctx, store.Request{ID: id, EntitlementID: entitlement, Principal: principal, Reason: reason, ExpiresAt: at.Add(ttl)}, principal, at)
	if err != nil {
		return g, err
	}
	return b.advance(ctx, g, grant.NeedsApproval, principal, "")
}
func (b *Broker) Approve(ctx context.Context, id, actor string) (store.Grant, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, err := b.Store.Get(ctx, id)
	if err != nil {
		return g, err
	}
	e, ok := b.Config.Find(g.EntitlementID)
	if !ok || !contains(e.Approvers, actor) || actor == g.Key.Principal || !contains(e.Requesters, g.Key.Principal) {
		return g, ErrForbidden
	}
	if !b.Now().Before(g.ExpiresAt) {
		return g, store.ErrInvalid
	}
	g, err = b.advance(ctx, g, grant.Approve, actor, "")
	if err != nil {
		return g, err
	}
	return b.apply(ctx, g)
}
func (b *Broker) Deny(ctx context.Context, id, actor string) (store.Grant, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, err := b.Store.Get(ctx, id)
	if err != nil {
		return g, err
	}
	e, ok := b.Config.Find(g.EntitlementID)
	if !ok || !contains(e.Approvers, actor) || actor == g.Key.Principal {
		return g, ErrForbidden
	}
	if g.State != grant.Requested && g.State != grant.PendingApproval {
		return g, grant.ErrInvalidTransition
	}
	return b.advance(ctx, g, grant.Deny, actor, "")
}
func (b *Broker) Revoke(ctx context.Context, id, actor string) (store.Grant, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, err := b.Store.Get(ctx, id)
	if err != nil {
		return g, err
	}
	e, ok := b.Config.Find(g.EntitlementID)
	if !ok || (!contains(e.Approvers, actor) && actor != g.Key.Principal) {
		return g, ErrForbidden
	}
	if g.State == grant.Revoked {
		return g, nil
	}
	if g.State == grant.Requested || g.State == grant.PendingApproval || g.State == grant.Approved {
		return b.advance(ctx, g, grant.Deny, actor, "")
	}
	if g.State == grant.Granting {
		g, err = b.advance(ctx, g, grant.Fail, actor, "")
	} else if g.State != grant.Revoking {
		g, err = b.advance(ctx, g, grant.Revoke, actor, "")
	}
	if err != nil {
		return g, err
	}
	return b.cleanup(ctx, g)
}
func (b *Broker) advance(ctx context.Context, g store.Grant, event grant.Event, actor, ref string) (store.Grant, error) {
	return b.Store.Advance(ctx, store.Change{GrantID: g.ID, Version: g.Version, Event: event, Actor: actor, Ref: ref}, b.Now().UTC())
}
func (b *Broker) apply(ctx context.Context, g store.Grant) (store.Grant, error) {
	e, ok := b.Config.Find(g.EntitlementID)
	if !ok || !contains(e.Requesters, g.Key.Principal) {
		return b.advance(ctx, g, grant.Deny, "system:reconciler", "")
	}
	p := b.Providers[g.Provider]
	if p == nil {
		return g, errors.New("broker: provider unavailable")
	}
	// StartGrant debe estar confirmado antes de llamar al destino.
	intent, err := b.advance(ctx, g, grant.StartGrant, "system:reconciler", "")
	if err != nil {
		return g, err
	}
	h, err := p.Grant(ctx, provider.GrantSpec{GrantID: g.ID, Key: g.Key, ExpiresAt: g.ExpiresAt})
	if err != nil || h.GrantID != g.ID || h.Key != g.Key || h.Ref == "" || !b.Now().Before(g.ExpiresAt) {
		revoking, transitionErr := b.advance(ctx, intent, grant.Fail, "system:reconciler", "")
		if transitionErr != nil {
			return intent, errors.Join(err, transitionErr)
		}
		cleaned, cleanupErr := b.cleanup(ctx, revoking)
		return cleaned, errors.Join(errors.New("broker: grant failed or expired; cleanup required"), err, cleanupErr)
	}
	return b.advance(ctx, intent, grant.Granted, "system:reconciler", h.Ref)
}
func (b *Broker) cleanup(ctx context.Context, g store.Grant) (store.Grant, error) {
	p := b.Providers[g.Provider]
	if p == nil {
		return g, errors.New("broker: provider unavailable; revoke pending")
	}
	if err := p.Revoke(ctx, provider.Handle{GrantID: g.ID, Key: g.Key, Ref: g.Ref}); err != nil {
		return b.revokeFailed(ctx, g, fmt.Errorf("broker: revoke pending: %w", err))
	}
	actual, err := p.Observe(ctx, g.Key.Target)
	if err != nil {
		return b.revokeFailed(ctx, g, err)
	}
	for _, a := range actual {
		if a.GrantID == g.ID || (g.Ref != "" && a.Ref == g.Ref) {
			return b.revokeFailed(ctx, g, errors.New("broker: revoke not confirmed by observation"))
		}
	}
	return b.advance(ctx, g, grant.RevokeDone, "system:reconciler", "")
}

func (b *Broker) revokeFailed(ctx context.Context, g store.Grant, cause error) (store.Grant, error) {
	updated, err := b.advance(ctx, g, grant.Fail, "system:reconciler", "")
	if err != nil {
		return g, errors.Join(cause, err)
	}
	return updated, cause
}

// Reconcile never reapplies a grant left in Granting after an unknown outcome.
func (b *Broker) Reconcile(ctx context.Context) (Report, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	report := Report{Missing: []reconcile.Expected{}, Drift: []provider.Actual{}}
	grants, err := b.Store.Recoverable(ctx)
	if err != nil {
		return report, err
	}
	var problems []error
	for _, g := range grants {
		expired := !b.Now().Before(g.ExpiresAt)
		switch g.State {
		case grant.Requested, grant.PendingApproval, grant.Approved:
			if expired {
				_, err = b.advance(ctx, g, grant.Deny, "system:reconciler", "")
			} else if g.State == grant.Requested {
				_, err = b.advance(ctx, g, grant.NeedsApproval, "system:reconciler", "")
			} else if g.State == grant.Approved {
				_, err = b.apply(ctx, g)
			} else {
				err = nil
			}
		case grant.Granting:
			g, err = b.advance(ctx, g, grant.Fail, "system:reconciler", "")
			if err == nil {
				_, err = b.cleanup(ctx, g)
			}
		case grant.Active:
			err = nil
			if expired {
				g, err = b.advance(ctx, g, grant.Expire, "system:reconciler", "")
				if err == nil {
					_, err = b.cleanup(ctx, g)
				}
			}
		case grant.Revoking:
			_, err = b.cleanup(ctx, g)
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", g.ID, err))
		}
	}
	snapshot, err := b.Store.Snapshot(ctx)
	if err != nil {
		return report, errors.Join(append(problems, err)...)
	}
	targets := map[string]string{}
	for _, e := range b.Config.Entitlements {
		targets[e.Provider+"\x00"+e.Target] = e.ID
	}
	for _, id := range targets {
		e, _ := b.Config.Find(id)
		p := b.Providers[e.Provider]
		if p == nil {
			problems = append(problems, errors.New("broker: provider unavailable"))
			continue
		}
		actual, err := p.Observe(ctx, e.Target)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		expected := []reconcile.Expected{}
		for _, g := range snapshot.Grants {
			if g.Provider == e.Provider && g.Key.Target == e.Target && g.State == grant.Active {
				expected = append(expected, reconcile.Expected{GrantID: g.ID, Key: g.Key, ExpiresAt: g.ExpiresAt})
			}
		}
		plan := reconcile.Diff(expected, actual, b.Now())
		report.Missing = append(report.Missing, plan.Missing...)
		for _, a := range plan.Revoke {
			report.Drift = append(report.Drift, a)
			covered := false
			for _, g := range snapshot.Grants {
				if !g.State.Terminal() && (g.ID == a.GrantID || (g.Ref != "" && g.Ref == a.Ref)) {
					covered = true
					if g.State == grant.Active && a.Broken {
						intent, err := b.advance(ctx, g, grant.Revoke, "system:reconciler", "")
						if err == nil {
							_, err = b.cleanup(ctx, intent)
						}
						if err != nil {
							problems = append(problems, err)
						}
					}
					break
				}
			}
			if covered {
				continue
			}
			if err := b.cleanDrift(ctx, e.Provider, a); err != nil {
				problems = append(problems, err)
			}
		}
	}
	return report, errors.Join(problems...)
}

// cleanDrift records a distinct cleanup lease before touching an orphaned object.
func (b *Broker) cleanDrift(ctx context.Context, kind string, a provider.Actual) error {
	var entitlement Entitlement
	for _, e := range b.Config.Entitlements {
		if e.Provider == kind && e.Target == a.Key.Target && e.Role == a.Key.Role {
			entitlement = e
			break
		}
	}
	if entitlement.ID == "" || a.Ref == "" {
		return errors.New("broker: drift outside configured cleanup scope")
	}
	at := b.Now().UTC()
	g, err := b.Store.CreateCleanup(ctx, store.Request{ID: "cleanup_" + rand.Text(), EntitlementID: entitlement.ID, Principal: a.Key.Principal, Reason: "Cleanup of observed orphan " + a.Ref, ExpiresAt: at.Add(time.Second)}, a.Ref, "system:reconciler", at)
	if err != nil {
		return err
	}
	_, err = b.cleanup(ctx, g)
	return err
}
