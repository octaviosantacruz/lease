package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/octaviosantacruz/lease/internal/provider"
	pg "github.com/octaviosantacruz/lease/internal/provider/postgres"
)

func TestDisposablePostgresContract(t *testing.T) {
	dsn := os.Getenv("LEASE_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("run scripts/test-postgres.sh for the disposable integration")
	}
	dir := os.Getenv("LEASE_PG_TEST_DIR")
	marker, err := os.ReadFile(filepath.Join(dir, "LEASE_DISPOSABLE"))
	if err != nil || string(marker) != "lease-disposable-postgres\n" {
		t.Fatal("private disposable cluster marker required")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil || parsed.Host != filepath.Join(dir, "socket") || parsed.User != "lease_lab_admin" {
		t.Fatal("refusing non-disposable target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"CREATE TABLE public.report (value integer)", "INSERT INTO public.report VALUES (7)",
		"CREATE ROLE fixture_reader NOLOGIN", "GRANT USAGE ON SCHEMA public TO fixture_reader", "GRANT SELECT ON public.report TO fixture_reader",
		"CREATE ROLE fixture_analyst LOGIN INHERIT", "CREATE ROLE unrelated NOLOGIN",
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	config := pg.Config{Target: "postgres/disposable", Namespace: "lslab", Principals: map[string]string{"user:analyst": "fixture_analyst"}, Roles: map[string]string{"readonly": "fixture_reader"}}
	p, err := pg.Open(ctx, dsn, config)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Capabilities().NativeExpiry {
		t.Fatal("PostgreSQL cannot promise native entitlement expiry")
	}
	parsed.User = "fixture_analyst"
	userDB := stdlib.OpenDB(*parsed)
	defer userDB.Close()
	if _, err = userDB.ExecContext(ctx, "SELECT * FROM public.report"); err == nil {
		t.Fatal("access exists before grant")
	}
	g := provider.GrantSpec{GrantID: "grant-1", Key: provider.Key{Target: config.Target, Principal: "user:analyst", Role: "readonly"}, ExpiresAt: time.Now().Add(time.Second)}
	h, err := p.Grant(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := p.Grant(ctx, g)
	if err != nil || repeated != h {
		t.Fatal(repeated, err)
	}
	changed := g
	changed.ExpiresAt = changed.ExpiresAt.Add(time.Hour)
	if _, err = p.Grant(ctx, changed); err == nil {
		t.Fatal("conflicting idempotency accepted")
	}
	session, err := userDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.ExecContext(ctx, "SET ROLE "+pgx.Identifier{h.Ref}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var value int
	if err = session.QueryRowContext(ctx, "SELECT value FROM public.report").Scan(&value); err != nil || value != 7 {
		t.Fatal(value, err)
	}
	actual, err := p.Observe(ctx, config.Target)
	if err != nil || len(actual) != 1 || actual[0].GrantID != g.GrantID || actual[0].Broken {
		t.Fatal(actual, err)
	}
	// Unix-socket trust authentication demonstrates why password expiry isn't sufficient.
	time.Sleep(time.Until(g.ExpiresAt) + 20*time.Millisecond)
	if err = session.QueryRowContext(ctx, "SELECT value FROM public.report").Scan(&value); err != nil {
		t.Fatal("expected access to remain without worker", err)
	}
	if _, err = db.ExecContext(ctx, "GRANT unrelated TO "+pgx.Identifier{h.Ref}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	actual, err = p.Observe(ctx, config.Target)
	if err != nil || len(actual) != 1 || !actual[0].Broken {
		t.Fatal("membership drift not observed", actual, err)
	}
	h.Ref = "" // recovery must resolve the object even if the Grant response was lost
	if err = p.Revoke(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err = session.ExecContext(ctx, "SELECT 1"); err == nil {
		t.Fatal("existing SET ROLE session survived revoke")
	}
	fresh := stdlib.OpenDB(*parsed)
	defer fresh.Close()
	if _, err = fresh.ExecContext(ctx, "SELECT * FROM public.report"); err == nil {
		t.Fatal("new session retains access")
	}
	if err = p.Revoke(ctx, h); err != nil {
		t.Fatal("revoke is not idempotent", err)
	}
	actual, err = p.Observe(ctx, config.Target)
	if err != nil || len(actual) != 0 {
		t.Fatal(actual, err)
	}
	if err = p.Revoke(ctx, provider.Handle{GrantID: g.GrantID, Key: g.Key, Ref: "unrelated"}); err == nil {
		t.Fatal("namespace escape accepted")
	}
	var exists bool
	if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='unrelated')").Scan(&exists); err != nil || !exists {
		t.Fatal("unrelated role affected", err)
	}

	logical := "user:analyst'\\; DROP ROLE unrelated; --"
	injectedConfig := config
	injectedConfig.Principals = map[string]string{logical: "fixture_analyst"}
	q, err := pg.Open(ctx, dsn+" options='-c standard_conforming_strings=off'", injectedConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	injected := provider.GrantSpec{GrantID: "quoted-metadata", Key: provider.Key{Target: config.Target, Principal: logical, Role: "readonly"}, ExpiresAt: time.Now().Add(time.Minute)}
	ih, err := q.Grant(ctx, injected)
	if err != nil {
		t.Fatal("unsafe metadata quoting", err)
	}
	if err = q.Revoke(ctx, ih); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='unrelated')").Scan(&exists); err != nil || !exists {
		t.Fatal("metadata escaped SQL literal", err)
	}
	if strings.Contains(h.Ref, ";") {
		t.Fatal("unsafe object identifier")
	}
}
