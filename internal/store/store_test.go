package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/octaviosantacruz/lease/internal/grant"
)

var testNow = time.Date(2026, 10, 22, 12, 0, 0, 123, time.UTC)
var testContext = context.Background()

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lease.sqlite")
	if err := Migrate(testContext, path); err != nil {
		t.Fatal(err)
	}
	s, err := Open(testContext, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e := Entitlement{ID: "lab-ro", Provider: "fake", Target: "lab/disposable", Role: "readonly", MaxTTL: time.Hour}
	if err := s.CreateEntitlement(testContext, e); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func request(id string) Request {
	return Request{ID: id, EntitlementID: "lab-ro", Principal: "user:lab", Reason: "test on a disposable target", ExpiresAt: testNow.Add(30 * time.Minute)}
}

func create(t *testing.T, s *Store, id string) Grant {
	t.Helper()
	g, err := s.CreateRequest(testContext, request(id), "user:lab", testNow)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func countEvents(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM events").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrationExplicitAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.sqlite")
	version, err := MigrationStatus(testContext, path)
	if err != nil || version != 0 {
		t.Fatalf("status = %d, %v", version, err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status created a file: %v", err)
	}
	if _, err = Open(testContext, path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open = %v", err)
	}
	for range 2 {
		if err = Migrate(testContext, path); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("database permissions = %o", info.Mode().Perm())
	}
	version, err = MigrationStatus(testContext, path)
	if err != nil || version != 1 {
		t.Fatalf("migrated status = %d, %v", version, err)
	}
}

func TestOpenDoesNotMigrateAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.sqlite")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(testContext, path); !errors.Is(err, ErrNeedsMigration) {
		t.Fatalf("Open = %v", err)
	}
	version, err := MigrationStatus(testContext, path)
	if err != nil || version != 0 {
		t.Fatalf("schema silently changed: %d %v", version, err)
	}
}

func TestMigrationRejectsForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.sqlite")
	db, err := connect(testContext, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE unrelated (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(testContext, path); !errors.Is(err, ErrSchema) {
		t.Fatalf("Migrate = %v", err)
	}
	var tables int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'table'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatalf("foreign schema changed: %d tables", tables)
	}
}

func TestMigrationRejectsChangedHistory(t *testing.T) {
	for _, mutation := range []string{"UPDATE schema_migrations SET checksum = printf('%064d', 0)", "UPDATE schema_migrations SET version = 2"} {
		t.Run(mutation, func(t *testing.T) {
			s, path := openTestStore(t)
			if _, err := s.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(testContext, path); !errors.Is(err, ErrSchema) {
				t.Fatalf("Migrate = %v", err)
			}
			if _, err := Open(testContext, path); !errors.Is(err, ErrSchema) {
				t.Fatalf("Open = %v", err)
			}
		})
	}
}

func TestRequestSurvivesReopen(t *testing.T) {
	s, path := openTestStore(t)
	g := create(t, s, "g1")
	if g.State != grant.Requested || g.Version != 1 || g.Key.Role != "readonly" || g.Provider != "fake" {
		t.Fatalf("unexpected request: %+v", g)
	}
	if _, err := s.CreateRequest(testContext, request("g1"), "user:lab", testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate = %v", err)
	}
	if countEvents(t, s) != 1 {
		t.Fatal("duplicate appended an event")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(testContext, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Get(testContext, "g1")
	if err != nil || got != g {
		t.Fatalf("reopen = %+v, %v; want %+v", got, err, g)
	}
	pending, err := reopened.Recoverable(testContext)
	if err != nil || len(pending) != 1 || pending[0] != g {
		t.Fatalf("recover = %+v, %v", pending, err)
	}
}

func TestRequestValidation(t *testing.T) {
	s, _ := openTestStore(t)
	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"no id", func(r *Request) { r.ID = " " }},
		{"no principal", func(r *Request) { r.Principal = "" }},
		{"no reason", func(r *Request) { r.Reason = "" }},
		{"expired exactly now", func(r *Request) { r.ExpiresAt = testNow }},
		{"expired", func(r *Request) { r.ExpiresAt = testNow.Add(-time.Nanosecond) }},
		{"ttl too long", func(r *Request) { r.ExpiresAt = testNow.Add(time.Hour + time.Nanosecond) }},
		{"time outside nanosecond range", func(r *Request) { r.ExpiresAt = time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.name)
			tc.mutate(&r)
			if _, err := s.CreateRequest(testContext, r, "user:lab", testNow); !errors.Is(err, ErrInvalid) {
				t.Fatalf("CreateRequest = %v", err)
			}
		})
	}
	missing := request("missing")
	missing.EntitlementID = "not-configured"
	if _, err := s.CreateRequest(testContext, missing, "user:lab", testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown entitlement = %v", err)
	}
	if _, err := s.CreateRequest(testContext, request("actor"), "", testNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing actor = %v", err)
	}
	if countEvents(t, s) != 0 {
		t.Fatal("invalid request produced evidence")
	}
}

func TestProductionUsesMaintainerStateMachine(t *testing.T) {
	s, _ := openTestStore(t)
	g := create(t, s, "g1")
	want, wantErr := grant.Next(g.State, grant.AutoApprove)
	got, err := s.Advance(testContext, Change{GrantID: g.ID, Version: g.Version, Event: grant.AutoApprove, Actor: "policy:lab"}, testNow)
	if wantErr != nil {
		if !errors.Is(err, wantErr) {
			t.Fatalf("state machine error bypassed: %v", err)
		}
		persisted, err := s.Get(testContext, g.ID)
		if err != nil || persisted != g || countEvents(t, s) != 1 {
			t.Fatal("rejected transition changed storage")
		}
	} else if err != nil || got.State != want {
		t.Fatalf("state machine result not respected: %+v %v", got, err)
	}
}

// Este doble devuelve un resultado del núcleo; no implementa su tabla de estados.
// Permite probar una transacción aunque LEASE-001 siga reservado al dueño.
func nextResult(state grant.State) func(grant.State, grant.Event) (grant.State, error) {
	return func(grant.State, grant.Event) (grant.State, error) { return state, nil }
}

func TestEventFailureRollsBackRequestAndTransition(t *testing.T) {
	for _, transition := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "advance"}[transition], func(t *testing.T) {
			s, _ := openTestStore(t)
			if transition {
				create(t, s, "g1")
				s.next = nextResult(grant.Approved)
			}
			if _, err := s.db.Exec("CREATE TRIGGER reject_evidence BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT, 'injected write failure'); END"); err != nil {
				t.Fatal(err)
			}
			var err error
			if transition {
				_, err = s.Advance(testContext, Change{GrantID: "g1", Version: 1, Event: grant.AutoApprove, Actor: "policy:lab"}, testNow)
			} else {
				_, err = s.CreateRequest(testContext, request("g1"), "user:lab", testNow)
			}
			if err == nil {
				t.Fatal("event write failure was ignored")
			}
			if transition {
				g, err := s.Get(testContext, "g1")
				if err != nil || g.State != grant.Requested || g.Version != 1 || countEvents(t, s) != 1 {
					t.Fatalf("transition partially committed: %+v %v", g, err)
				}
			} else if _, err := s.Get(testContext, "g1"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("request partially committed: %v", err)
			}
			if err := s.Verify(testContext); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReopenRecoversEveryNonTerminalState(t *testing.T) {
	s, path := openTestStore(t)
	states := []grant.State{grant.Requested, grant.PendingApproval, grant.Approved, grant.Granting, grant.Active, grant.Revoking, grant.Denied, grant.Revoked}
	for _, state := range states {
		g := create(t, s, string(state))
		if state != grant.Requested {
			s.next = nextResult(state)
			_, err := s.Advance(testContext, Change{GrantID: g.ID, Version: 1, Event: grant.Event("fixture"), Actor: "test:fixture"}, testNow)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(testContext, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pending, err := reopened.Recoverable(testContext)
	if err != nil || len(pending) != 6 {
		t.Fatalf("recovery = %+v, %v", pending, err)
	}
	for _, g := range pending {
		if g.State.Terminal() {
			t.Fatalf("terminal state selected: %s", g.State)
		}
		if !g.ExpiresAt.Before(testNow.Add(time.Hour)) {
			t.Fatal("fixture should be expired at simulated restart")
		}
	}
}

func TestConcurrentVersionsAcrossConnections(t *testing.T) {
	s, path := openTestStore(t)
	create(t, s, "g1")
	other, err := Open(testContext, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s.next, other.next = nextResult(grant.Approved), nextResult(grant.Approved)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, writer := range []*Store{s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := writer.Advance(testContext, Change{GrantID: "g1", Version: 1, Event: grant.AutoApprove, Actor: "policy:lab"}, testNow)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 || countEvents(t, s) != 2 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	if err := s.Verify(testContext); err != nil {
		t.Fatal(err)
	}
}

func TestAuditRejectsMutationAndDetectsCorruption(t *testing.T) {
	cases := []string{
		"UPDATE chain_head SET sequence = 0",
		"UPDATE grants SET payload = CAST('{}' AS BLOB)",
		"UPDATE grants SET state = 'active'",
		"DROP TRIGGER events_no_update; UPDATE events SET body = CAST('{}' AS BLOB)",
		"DROP TRIGGER events_no_delete; DELETE FROM events",
	}
	for _, mutation := range cases {
		t.Run(mutation, func(t *testing.T) {
			s, path := openTestStore(t)
			create(t, s, "g1")
			if _, err := s.db.Exec("UPDATE events SET hash = previous_hash"); err == nil {
				t.Fatal("audit accepted update")
			}
			if _, err := s.db.Exec("DELETE FROM events"); err == nil {
				t.Fatal("audit accepted deletion")
			}
			if _, err := s.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if err := s.Verify(testContext); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("Verify = %v", err)
			}
			if _, err := s.Get(testContext, "g1"); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("read on a corrupt ledger did not fail closed: %v", err)
			}
			if _, err := s.Recoverable(testContext); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("recovery did not fail closed: %v", err)
			}
			if _, err := s.CreateRequest(testContext, request("g2"), "user:lab", testNow); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("creation on a corrupt ledger did not fail closed: %v", err)
			}
			if _, err := s.Advance(testContext, Change{GrantID: "g1", Version: 1, Event: grant.AutoApprove, Actor: "policy:lab"}, testNow); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("transition on a corrupt ledger did not fail closed: %v", err)
			}
			if _, err := Open(testContext, path); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("Open = %v", err)
			}
		})
	}
}

func TestIntentHandleAndExpiryBoundaries(t *testing.T) {
	s, path := openTestStore(t)
	g := create(t, s, "g1")
	s.next = nextResult(grant.Granting)
	change := Change{GrantID: g.ID, Version: 1, Event: grant.StartGrant, Actor: "broker:lab", Ref: "ephemeral-lab-role"}
	if _, err := s.Advance(testContext, change, g.ExpiresAt); !errors.Is(err, ErrInvalid) {
		t.Fatalf("grant exactly at expiry = %v", err)
	}
	if _, err := s.Advance(testContext, change, testNow.Add(-time.Nanosecond)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("backwards time = %v", err)
	}
	intent, err := s.Advance(testContext, change, testNow.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(testContext, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pending, err := reopened.Recoverable(testContext)
	if err != nil || len(pending) != 1 || pending[0] != intent || pending[0].Ref != change.Ref {
		t.Fatalf("lost grant intent or handle: %+v %v", pending, err)
	}
	reopened.next = nextResult(grant.Active)
	change.Version, change.Event, change.Ref = intent.Version, grant.Granted, "different-role"
	if _, err := reopened.Advance(testContext, change, testNow.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("handle changed = %v", err)
	}
	if countEvents(t, reopened) != 2 {
		t.Fatal("rejected operations appended evidence")
	}
}

func TestConcurrentRequestsShareOneAuditChain(t *testing.T) {
	s, path := openTestStore(t)
	other, err := Open(testContext, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			writer := s
			if i%2 == 1 {
				writer = other
			}
			_, err := writer.CreateRequest(testContext, request(fmt.Sprintf("g%d", i)), "user:lab", testNow)
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if countEvents(t, s) != 8 {
		t.Fatal("audit events lost under contention")
	}
	if err := s.Verify(testContext); err != nil {
		t.Fatal(err)
	}
}

// El subproceso termina sin Close ni defers: simula caída del proceso, no un
// apagado ordenado. Sólo recibe una base descartable creada por este test.
func TestCrashHelper(t *testing.T) {
	mode := os.Getenv("LEASE_STORE_CRASH_TEST")
	if mode == "" {
		return
	}
	s, err := Open(testContext, os.Getenv("LEASE_STORE_CRASH_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	if mode == "after-commit" {
		s.next = nextResult(grant.Granting)
		_, err = s.Advance(testContext, Change{GrantID: "g1", Version: 1, Event: grant.StartGrant, Actor: "broker:lab", Ref: "lab-role"}, testNow)
		if err != nil {
			t.Fatal(err)
		}
		os.Exit(73)
	}
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := readGrant(tx.QueryRowContext(testContext, "SELECT "+grantColumns+" FROM grants WHERE id = 'g1'"))
	if err != nil {
		t.Fatal(err)
	}
	g.State, g.Version = grant.Approved, 2
	payload, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE grants SET state = ?, version = ?, payload = ? WHERE id = 'g1'", g.State, g.Version, payload); err != nil {
		t.Fatal(err)
	}
	if err = appendEvent(testContext, tx, g, string(grant.AutoApprove), "policy:lab"); err != nil {
		t.Fatal(err)
	}
	// No Commit: ni la concesión ni el evento pueden sobrevivir a esta caída.
	os.Exit(73)
}

func TestAbruptProcessExit(t *testing.T) {
	for _, mode := range []string{"before-commit", "after-commit"} {
		t.Run(mode, func(t *testing.T) {
			s, path := openTestStore(t)
			create(t, s, "g1")
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
			command.Env = append(os.Environ(), "LEASE_STORE_CRASH_TEST="+mode, "LEASE_STORE_CRASH_PATH="+path)
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("crash helper = %v; %s", err, output)
			}
			reopened, err := Open(testContext, path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			pending, err := reopened.Recoverable(testContext)
			if err != nil || len(pending) != 1 {
				t.Fatalf("recover: %+v %v", pending, err)
			}
			if mode == "before-commit" {
				if pending[0].State != grant.Requested || pending[0].Version != 1 || countEvents(t, reopened) != 1 {
					t.Fatal("uncommitted state/event survived process exit")
				}
			} else if pending[0].State != grant.Granting || pending[0].Ref != "lab-role" || countEvents(t, reopened) != 2 {
				t.Fatal("committed intent lost after process exit")
			}
		})
	}
}

func TestTTLDoesNotSaturateDuration(t *testing.T) {
	s, _ := openTestStore(t)
	e := Entitlement{ID: "long-window", Provider: "fake", Target: "lab/disposable", Role: "readonly", MaxTTL: time.Duration(1<<63 - 1)}
	if err := s.CreateEntitlement(testContext, e); err != nil {
		t.Fatal(err)
	}
	at := time.Date(1678, 1, 1, 0, 0, 0, 0, time.UTC)
	r := request("long")
	r.EntitlementID = e.ID
	r.ExpiresAt = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.CreateRequest(testContext, r, "user:lab", at); !errors.Is(err, ErrInvalid) {
		t.Fatalf("window exceeds MaxTTL despite duration saturation: %v", err)
	}
	if countEvents(t, s) != 0 {
		t.Fatal("invalid long window was persisted")
	}
}
