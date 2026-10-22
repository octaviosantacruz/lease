package broker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/octaviosantacruz/lease/internal/provider"
	"github.com/octaviosantacruz/lease/internal/provider/lab"
	pg "github.com/octaviosantacruz/lease/internal/provider/postgres"
	"github.com/octaviosantacruz/lease/internal/store"
)

// Runtime owns resources; Close libera también el lock ante retornos tempranos.
type Runtime struct {
	*Broker
	close []func()
}

func (r *Runtime) Close() {
	for i := len(r.close) - 1; i >= 0; i-- {
		r.close[i]()
	}
	r.close = nil
}
func Open(ctx context.Context, path string, c Config) (*Runtime, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	unlock, err := store.LockExecutor(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("broker: another executor may be running: %w", err)
	}
	r := &Runtime{close: []func(){unlock}}
	fail := func(err error) (*Runtime, error) { r.Close(); return nil, err }
	s, err := store.Open(ctx, path)
	if err != nil {
		return fail(err)
	}
	r.close = append(r.close, func() { s.Close() })
	r.Broker = &Broker{Store: s, Config: c, Providers: map[string]provider.Provider{}, Now: time.Now}
	for _, e := range c.Entitlements {
		if r.Providers[e.Provider] != nil {
			continue
		}
		switch e.Provider {
		case "lab":
			p, err := lab.Open(path + ".target.sqlite")
			if err != nil {
				return fail(err)
			}
			r.Providers[e.Provider] = p
			r.close = append(r.close, func() { p.Close() })
		case "postgres":
			if c.Postgres.DSNEnv == "" || os.Getenv(c.Postgres.DSNEnv) == "" {
				return fail(errors.New("broker: PostgreSQL DSN environment variable is required"))
			}
			p, err := pg.Open(ctx, os.Getenv(c.Postgres.DSNEnv), pg.Config{Target: c.Postgres.Target, Namespace: c.Postgres.Namespace, Principals: c.Postgres.Principals, Roles: c.Postgres.Roles})
			if err != nil {
				return fail(err)
			}
			r.Providers[e.Provider] = p
			r.close = append(r.close, func() { p.Close() })
		}
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return fail(err)
	}
	for _, existing := range snapshot.Entitlements {
		e, ok := c.Find(existing.ID)
		if !ok || e.Record() != existing {
			return fail(errors.New("broker: configuration must retain all immutable entitlements for recovery"))
		}
	}
	return r, nil
}

func DemoConfig() Config {
	return Config{Entitlements: []Entitlement{
		{ID: "analytics-read", Provider: "lab", Target: "lab/analytics", Role: "readonly", MaxTTL: "30m", Requesters: []string{"user:ines", "agent:diagnostico"}, Approvers: []string{"user:mateo"}},
		{ID: "payments-read", Provider: "lab", Target: "lab/payments", Role: "readonly", MaxTTL: "15m", Requesters: []string{"user:ines"}, Approvers: []string{"user:mateo"}},
		{ID: "logs-investigate", Provider: "lab", Target: "lab/logs", Role: "investigator", MaxTTL: "5m", Requesters: []string{"agent:diagnostico", "user:ines"}, Approvers: []string{"user:mateo"}},
	}}
}
func SeedDemo(ctx context.Context, b *Broker) error {
	snap, err := b.Store.Snapshot(ctx)
	if err != nil {
		return err
	}
	if len(snap.Grants) > 0 {
		return nil
	}
	g, err := b.Request(ctx, "analytics-read", "user:ines", "Investigar diferencia en el reporte de conciliación, INC-284", 20*time.Minute)
	if err != nil {
		return err
	}
	if _, err = b.Approve(ctx, g.ID, "user:mateo"); err != nil {
		return err
	}
	if _, err = b.Request(ctx, "logs-investigate", "agent:diagnostico", "Reunir evidencia del timeout, INC-291", 3*time.Minute); err != nil {
		return err
	}
	g, err = b.Request(ctx, "payments-read", "user:ines", "Revisar una operación duplicada, INC-276", 10*time.Minute)
	if err != nil {
		return err
	}
	_, err = b.Deny(ctx, g.ID, "user:mateo")
	return err
}
