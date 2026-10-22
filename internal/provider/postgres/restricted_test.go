package postgres_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/grant"
	"github.com/octaviosantacruz/lease/internal/provider"
	"github.com/octaviosantacruz/lease/internal/store"
)

func TestRestrictedOperatorBrokerLifecycle(t *testing.T) {
	admin, dsn := disposableAdmin(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, statement := range []string{
		"CREATE ROLE restricted_reader NOLOGIN", "CREATE ROLE restricted_analyst LOGIN INHERIT",
		"CREATE TABLE public.restricted_report (value integer)", "INSERT INTO public.restricted_report VALUES (11)",
		"GRANT SELECT ON public.restricted_report TO restricted_reader",
		"CREATE ROLE restricted_unrelated NOLOGIN",
	} {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	bootstrapOperator(t, ctx, dsn, "restricted_operator", "restricted_reader")
	opDSN := operatorDSN(t, dsn, "restricted_operator")
	parsed, err := pgx.ParseConfig(opDSN)
	if err != nil {
		t.Fatal(err)
	}
	operatorDB := stdlib.OpenDB(*parsed)
	defer operatorDB.Close()
	var super, creator, dbCreator, repl, bypass bool
	if err = operatorDB.QueryRowContext(ctx, "SELECT rolsuper,rolcreaterole,rolcreatedb,rolreplication,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&super, &creator, &dbCreator, &repl, &bypass); err != nil || super || !creator || dbCreator || repl || bypass {
		t.Fatal("wrong operator attributes", err)
	}
	assertOperatorHasNoData := func() {
		t.Helper()
		if _, err := operatorDB.ExecContext(ctx, "SELECT * FROM public.restricted_report"); err == nil {
			t.Fatal("operator inherited entitlement data")
		}
	}
	assertOperatorHasNoData()
	if _, err = operatorDB.ExecContext(ctx, "ALTER ROLE restricted_unrelated LOGIN"); err == nil {
		t.Fatal("operator changed unrelated role without ADMIN")
	}
	if _, err = operatorDB.ExecContext(ctx, "CREATE ROLE forbidden_super SUPERUSER"); err == nil {
		t.Fatal("operator created superuser")
	}
	config := broker.Config{Postgres: &broker.PostgresConfig{Target: "postgres/restricted", Namespace: "rtlease", DSNEnv: "LEASE_RESTRICTED_DSN", Principals: map[string]string{"user:analyst": "restricted_analyst"}, Roles: map[string]string{"readonly": "restricted_reader"}}, Entitlements: []broker.Entitlement{{ID: "restricted-read", Provider: "postgres", Target: "postgres/restricted", Role: "readonly", MaxTTL: "5m", Requesters: []string{"user:analyst"}, Approvers: []string{"user:approver"}}}}
	t.Setenv("LEASE_RESTRICTED_DSN", opDSN)
	path := filepath.Join(t.TempDir(), "broker.sqlite")
	if err = store.Migrate(ctx, path); err != nil {
		t.Fatal(err)
	}
	runtime, err := broker.Open(ctx, path, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if runtime != nil {
			runtime.Close()
		}
	}()
	if err = runtime.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	runtime.Now = func() time.Time { return now }
	g, err := runtime.Request(ctx, "restricted-read", "user:analyst", "INC-407", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	g, err = runtime.Approve(ctx, g.ID, "user:approver")
	if err != nil || g.State != grant.Active {
		t.Fatal(g, err)
	}
	assertOperatorHasNoData()
	userCfg, err := pgx.ParseConfig(operatorDSN(t, dsn, "restricted_analyst"))
	if err != nil {
		t.Fatal(err)
	}
	userDB := stdlib.OpenDB(*userCfg)
	defer userDB.Close()
	session, err := userDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.ExecContext(ctx, "SET ROLE "+pgx.Identifier{g.Ref}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var value int
	if err = session.QueryRowContext(ctx, "SELECT value FROM public.restricted_report").Scan(&value); err != nil || value != 11 {
		t.Fatal(value, err)
	}
	now = g.ExpiresAt
	if _, err = runtime.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	g, err = runtime.Store.Get(ctx, g.ID)
	if err != nil || g.State != grant.Revoked {
		t.Fatal(g, err)
	}
	if _, err = session.ExecContext(ctx, "SELECT 1"); err == nil {
		t.Fatal("existing session survived revoke")
	}
	if _, err = userDB.ExecContext(ctx, "SELECT * FROM public.restricted_report"); err == nil {
		t.Fatal("fresh session retains privilege")
	}
	// Recuperación de resultado perdido, ahora con proveedor PostgreSQL real.
	now = time.Now().UTC()
	g, err = runtime.Request(ctx, "restricted-read", "user:analyst", "INC-408 lost result", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []grant.Event{grant.Approve, grant.StartGrant} {
		g, err = runtime.Store.Advance(ctx, store.Change{GrantID: g.ID, Version: g.Version, Event: event, Actor: "user:approver"}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	p := runtime.Providers["postgres"]
	if _, err = p.Grant(ctx, provider.GrantSpec{GrantID: g.ID, Key: g.Key, ExpiresAt: g.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	runtime, err = broker.Open(ctx, path, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	g, err = runtime.Store.Get(ctx, g.ID)
	if err != nil || g.State != grant.Revoked {
		t.Fatal(g, err)
	}
	if actual, err := runtime.Providers["postgres"].Observe(ctx, g.Key.Target); err != nil || len(actual) != 0 {
		t.Fatal(actual, err)
	}
	if err = runtime.Store.Verify(ctx); err != nil {
		t.Fatal(err)
	}
}
