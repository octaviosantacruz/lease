package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/octaviosantacruz/lease/internal/provider"
	pg "github.com/octaviosantacruz/lease/internal/provider/postgres"
)

func TestPreflightRejectsDangerousPathsWithoutBlockingCleanup(t *testing.T) {
	admin, dsn := disposableAdmin(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, statement := range []string{
		"CREATE ROLE check_reader NOLOGIN", "CREATE ROLE check_analyst LOGIN NOINHERIT", "CREATE ROLE check_mid NOLOGIN",
		"CREATE ROLE check_danger NOLOGIN CREATEROLE", "CREATE ROLE check_plain NOLOGIN",
	} {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	bootstrapOperator(t, ctx, dsn, "check_operator", "check_reader")
	config := pg.Config{Target: "postgres/checks", Namespace: "cklease", Principals: map[string]string{"user:analyst": "check_analyst"}, Roles: map[string]string{"readonly": "check_reader"}}
	p, err := pg.Open(ctx, operatorDSN(t, dsn, "check_operator"), config)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Check(ctx); err != nil {
		t.Fatal("valid preflight rejected", err)
	}
	spec := provider.GrantSpec{GrantID: "first", Key: provider.Key{Target: config.Target, Principal: "user:analyst", Role: "readonly"}, ExpiresAt: time.Now().Add(time.Minute)}
	countObjects := func() int {
		t.Helper()
		var n int
		if err := admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_roles WHERE starts_with(rolname,'cklease_')").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Direct, transitive, predefined and delegable privileges are all denied.
	cases := []struct {
		name           string
		setup, cleanup []string
	}{
		{"direct attribute", []string{"ALTER ROLE check_analyst CREATEDB"}, []string{"ALTER ROLE check_analyst NOCREATEDB"}},
		{"inherited privileged path", []string{"GRANT check_danger TO check_mid", "GRANT check_mid TO check_analyst"}, []string{"REVOKE check_mid FROM check_analyst", "REVOKE check_danger FROM check_mid"}},
		{"predefined role", []string{"GRANT pg_read_server_files TO check_reader"}, []string{"REVOKE pg_read_server_files FROM check_reader"}},
		{"admin delegation", []string{"GRANT check_plain TO check_reader WITH ADMIN TRUE, INHERIT FALSE, SET FALSE"}, []string{"REVOKE check_plain FROM check_reader"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, sql := range tc.setup {
				if _, err := admin.ExecContext(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				for _, sql := range tc.cleanup {
					if _, err := admin.ExecContext(ctx, sql); err != nil {
						t.Error(err)
					}
				}
			}()
			if err := p.Check(ctx); !errors.Is(err, pg.ErrUnsafe) {
				t.Fatal("unsafe preflight accepted", err)
			}
			g := spec
			g.GrantID = tc.name
			if _, err := p.Grant(ctx, g); !errors.Is(err, pg.ErrUnsafe) {
				t.Fatal("unsafe grant accepted", err)
			}
			if countObjects() != 0 {
				t.Fatal("unsafe grant left an object")
			}
		})
	}
	if err = p.Check(ctx); err != nil {
		t.Fatal(err)
	}
	// NOINHERIT on the login must not change the explicit membership contract.
	h, err := p.Grant(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := p.Observe(ctx, config.Target); err != nil || len(actual) != 1 || actual[0].Broken {
		t.Fatal(actual, err)
	}
	if _, err = admin.ExecContext(ctx, "GRANT check_danger TO check_reader"); err != nil {
		t.Fatal(err)
	}
	if err = p.Check(ctx); !errors.Is(err, pg.ErrUnsafe) {
		t.Fatal(err)
	}
	actual, err := p.Observe(ctx, config.Target)
	if err != nil || len(actual) != 1 || !actual[0].Broken {
		t.Fatal("new privileged path not observed", actual, err)
	}
	// Reopening for recovery must not apply the grant-only guard.
	p.Close()
	p, err = pg.Open(ctx, operatorDSN(t, dsn, "check_operator"), config)
	if err != nil {
		t.Fatal("recovery blocked", err)
	}
	defer p.Close()
	if err = p.Revoke(ctx, h); err != nil {
		t.Fatal("unsafe existing permission cannot be removed", err)
	}
	if countObjects() != 0 {
		t.Fatal("cleanup incomplete")
	}
	if _, err = admin.ExecContext(ctx, "REVOKE check_danger FROM check_reader"); err != nil {
		t.Fatal(err)
	}
	// Missing ADMIN or signal privileges prevents new grants before any DDL.
	if _, err = admin.ExecContext(ctx, "REVOKE ADMIN OPTION FOR check_reader FROM check_operator"); err != nil {
		t.Fatal(err)
	}
	if err = p.Check(ctx); !errors.Is(err, pg.ErrOperator) {
		t.Fatal("missing ADMIN accepted", err)
	}
	if _, err = p.Grant(ctx, spec); !errors.Is(err, pg.ErrOperator) {
		t.Fatal(err)
	}
	if _, err = admin.ExecContext(ctx, "GRANT check_reader TO check_operator WITH ADMIN TRUE, INHERIT FALSE, SET FALSE"); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.ExecContext(ctx, "REVOKE pg_signal_backend FROM check_operator"); err != nil {
		t.Fatal(err)
	}
	if err = p.Check(ctx); !errors.Is(err, pg.ErrOperator) {
		t.Fatal("missing signal permission accepted", err)
	}
	if countObjects() != 0 {
		t.Fatal("preflight wrote privileges")
	}
}
