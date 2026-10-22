package postgres_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// disposableAdmin refuses arbitrary DSNs even when an environment variable exists.
func disposableAdmin(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("LEASE_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("run scripts/test-postgres.sh for disposable integration")
	}
	dir := os.Getenv("LEASE_PG_TEST_DIR")
	marker, err := os.ReadFile(filepath.Join(dir, "LEASE_DISPOSABLE"))
	parsed, parseErr := pgx.ParseConfig(dsn)
	if err != nil || string(marker) != "lease-disposable-postgres\n" || parseErr != nil || parsed.Host != filepath.Join(dir, "socket") || parsed.User != "lease_lab_admin" {
		t.Fatal("refusing non-disposable target")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dsn
}
func operatorDSN(t *testing.T, adminDSN, operator string) string {
	t.Helper()
	parsed, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "postgres", User: url.User(operator), Path: "/" + parsed.Database}
	q := url.Values{"host": {parsed.Host}, "sslmode": {"disable"}}
	u.RawQuery = postgresQuery(q)
	return u.String()
}
func bootstrapOperator(t *testing.T, ctx context.Context, adminDSN, operator, base string) {
	t.Helper()
	bin := os.Getenv("LEASE_PG_BIN")
	if bin == "" {
		bin = "/usr/lib/postgresql/18/bin"
	}
	command := exec.CommandContext(ctx, filepath.Join(bin, "psql"), "-X", "--dbname", adminDSN, "-v", "ON_ERROR_STOP=1", "-v", "lease_operator="+operator, "-v", "lease_base_role="+base, "-v", "lease_database=postgres", "-f", filepath.Join("..", "..", "..", "examples", "postgres-operator.sql"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap operator: %v: %s", err, output)
	}
}

// libpq trata '+' como dato literal; url.Values usa la codificación de formularios.
// %2B conserva los signos reales y sólo los '+' de espacios se vuelven %20.
func postgresQuery(values url.Values) string { return strings.ReplaceAll(values.Encode(), "+", "%20") }

func TestPostgresURIStartupOptionEncoding(t *testing.T) {
	options := "-c standard_conforming_strings=off -c application_name=probe+literal"
	u := url.URL{Scheme: "postgres", User: url.User("fixture_operator"), Path: "/postgres"}
	values := url.Values{"host": {"/tmp/lease-offline/socket"}, "sslmode": {"disable"}, "options": {options}}
	u.RawQuery = values.Encode()
	bad, err := pgx.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	if bad.RuntimeParams["options"] == options {
		t.Fatal("fixture no longer reproduces form-encoding mismatch")
	}
	u.RawQuery = postgresQuery(values)
	fixed, err := pgx.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	if fixed.RuntimeParams["options"] != options || fixed.User != "fixture_operator" || fixed.Host != "/tmp/lease-offline/socket" {
		t.Fatal("URI changed startup settings")
	}
}
