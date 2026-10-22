// Package postgres manages ephemeral NOLOGIN roles for mapped existing logins.
// It revokes membership and terminates that login's sessions across the cluster.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/octaviosantacruz/lease/internal/provider"
)

type Config struct {
	Target     string
	Namespace  string
	Principals map[string]string
	Roles      map[string]string
}
type Provider struct {
	db       *sql.DB
	config   Config
	operator string
}
type metadata struct {
	GrantID   string
	Key       provider.Key
	ExpiresAt time.Time
	Login     string
	Role      string
}

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9]{1,11}$`)

func Open(ctx context.Context, dsn string, c Config) (*Provider, error) {
	if !namespacePattern.MatchString(c.Namespace) || c.Target == "" || len(c.Principals) == 0 || len(c.Roles) == 0 {
		return nil, errors.New("postgres: invalid configuration")
	}
	// ParseConfig y un pool pequeño evitan construir SQL con el DSN o abrir una conexión por operación.
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("postgres: invalid connection configuration")
	}
	parsed.ConnectTimeout = 5 * time.Second
	db := stdlib.OpenDB(*parsed)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	p := &Provider{db: db, config: c}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("postgres: connection unavailable (check local DSN)")
	}
	var operator string
	if err = db.QueryRowContext(ctx, "SELECT current_user").Scan(&operator); err != nil {
		db.Close()
		return nil, err
	}
	p.operator = operator
	for _, login := range c.Principals {
		var super, createRole, createDB, repl, bypass, canLogin bool
		err = db.QueryRowContext(ctx, "SELECT rolsuper,rolcreaterole,rolcreatedb,rolreplication,rolbypassrls,rolcanlogin FROM pg_roles WHERE rolname=$1", login).Scan(&super, &createRole, &createDB, &repl, &bypass, &canLogin)
		if err != nil || login == operator || super || createRole || createDB || repl || bypass || !canLogin {
			db.Close()
			return nil, errors.New("postgres: principal must be an unprivileged login distinct from operator")
		}
	}
	for _, role := range c.Roles {
		var dangerous bool
		err = db.QueryRowContext(ctx, "SELECT rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls OR rolcanlogin FROM pg_roles WHERE rolname=$1", role).Scan(&dangerous)
		if err != nil || dangerous {
			db.Close()
			return nil, errors.New("postgres: entitlement role must be an unprivileged NOLOGIN role")
		}
	}
	return p, nil
}
func (p *Provider) Close() error                { return p.db.Close() }
func (p *Provider) Capabilities() provider.Caps { return provider.Caps{NativeExpiry: false} }
func (p *Provider) ref(id string) string {
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%s_%x", p.config.Namespace, sum[:16])
}
func ident(s string) string   { return pgx.Identifier{s}.Sanitize() }
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func (p *Provider) validate(k provider.Key) error {
	if k.Target != p.config.Target || p.config.Principals[k.Principal] == "" || p.config.Roles[k.Role] == "" {
		return errors.New("postgres: privilege outside configured mappings")
	}
	return nil
}
func (p *Provider) begin(ctx context.Context) (*sql.Tx, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// El literal JSON del COMMENT debe conservar escapes incluso si el servidor
	// tiene standard_conforming_strings desactivado por su configuración local.
	if _, err = tx.ExecContext(ctx, "SET LOCAL standard_conforming_strings = on"); err != nil {
		tx.Rollback()
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", p.config.Namespace); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func (p *Provider) Grant(ctx context.Context, g provider.GrantSpec) (provider.Handle, error) {
	if err := p.validate(g.Key); err != nil {
		return provider.Handle{}, err
	}
	if g.GrantID == "" || !time.Now().Before(g.ExpiresAt) {
		return provider.Handle{}, errors.New("postgres: expired or invalid grant")
	}
	tx, err := p.begin(ctx)
	if err != nil {
		return provider.Handle{}, err
	}
	defer tx.Rollback()
	ref := p.ref(g.GrantID)
	body, _ := json.Marshal(metadata{GrantID: g.GrantID, Key: g.Key, ExpiresAt: g.ExpiresAt.UTC(), Login: p.config.Principals[g.Key.Principal], Role: p.config.Roles[g.Key.Role]})
	var comment string
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(shobj_description(oid,'pg_authid'),'') FROM pg_roles WHERE rolname=$1", ref).Scan(&comment)
	if err == nil {
		if comment != string(body) {
			return provider.Handle{}, errors.New("postgres: role ownership or idempotency conflict")
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		statements := []string{
			"CREATE ROLE " + ident(ref) + " NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS",
			"COMMENT ON ROLE " + ident(ref) + " IS " + literal(string(body)),
			"GRANT " + ident(p.config.Roles[g.Key.Role]) + " TO " + ident(ref),
			"GRANT " + ident(ref) + " TO " + ident(p.config.Principals[g.Key.Principal]),
		}
		for _, statement := range statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return provider.Handle{}, err
			}
		}
	} else {
		return provider.Handle{}, err
	}
	if err = tx.Commit(); err != nil {
		return provider.Handle{}, err
	}
	return provider.Handle{GrantID: g.GrantID, Key: g.Key, Ref: ref}, nil
}
func (p *Provider) Revoke(ctx context.Context, h provider.Handle) error {
	if err := p.validate(h.Key); err != nil {
		return err
	}
	ref := h.Ref
	if ref == "" {
		ref = p.ref(h.GrantID)
	}
	if !strings.HasPrefix(ref, p.config.Namespace+"_") {
		return errors.New("postgres: object outside managed namespace")
	}
	tx, err := p.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var body string
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(shobj_description(oid,'pg_authid'),'') FROM pg_roles WHERE rolname=$1", ref).Scan(&body)

	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	login := p.config.Principals[h.Key.Principal]
	if exists {
		var m metadata
		if json.Unmarshal([]byte(body), &m) != nil || m.Key != h.Key || p.ref(m.GrantID) != ref || m.Login == "" || m.Role == "" {
			return errors.New("postgres: object ownership mismatch")
		}
		login = m.Login
		for _, statement := range []string{"REVOKE " + ident(ref) + " FROM " + ident(login), "REVOKE " + ident(m.Role) + " FROM " + ident(ref)} {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	// La revocación se confirma antes de terminar sesiones: los nuevos logins ya
	// no heredan el rol. Un crash aquí deja el objeto para el próximo reintento.
	rows, err := p.db.QueryContext(ctx, "SELECT pg_terminate_backend(pid,5000) FROM pg_stat_activity WHERE usename=$1 AND pid<>pg_backend_pid()", login)
	if err != nil {
		return err
	}
	for rows.Next() {
		var terminated bool
		if err = rows.Scan(&terminated); err != nil {
			rows.Close()
			return err
		}
		if !terminated {
			rows.Close()
			return errors.New("postgres: session termination not confirmed")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, "DROP ROLE IF EXISTS "+ident(ref))
	return err
}
func (p *Provider) Observe(ctx context.Context, target string) ([]provider.Actual, error) {
	if target != p.config.Target {
		return nil, errors.New("postgres: target outside configured scope")
	}
	rows, err := p.db.QueryContext(ctx, "SELECT rolname,COALESCE(shobj_description(oid,'pg_authid'),'') FROM pg_roles WHERE starts_with(rolname,$1) ORDER BY rolname", p.config.Namespace+"_")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	actual := []provider.Actual{}
	for rows.Next() {
		var ref, body string
		if err = rows.Scan(&ref, &body); err != nil {
			return nil, err
		}
		var m metadata
		if json.Unmarshal([]byte(body), &m) != nil || p.ref(m.GrantID) != ref || p.validate(m.Key) != nil || m.Login == "" || m.Role == "" {
			return nil, errors.New("postgres: unrecognized object in managed namespace; operator review required")
		}

		var safe bool
		err = p.db.QueryRowContext(ctx, `SELECT
 NOT (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls OR rolcanlogin) AND rolinherit
 AND (SELECT count(*)=1 AND bool_and(roleid=(SELECT oid FROM pg_roles WHERE rolname=$2)) FROM pg_auth_members WHERE member=r.oid)
 AND (SELECT count(*)=1 AND bool_and(member=(SELECT oid FROM pg_roles WHERE rolname=$3) AND inherit_option AND set_option AND NOT admin_option) FROM pg_auth_members WHERE roleid=r.oid AND member<>(SELECT oid FROM pg_roles WHERE rolname=$4))
 FROM pg_roles r WHERE rolname=$1`, ref, m.Role, m.Login, p.operator).Scan(&safe)
		if err != nil {
			return nil, err
		}
		actual = append(actual, provider.Actual{GrantID: m.GrantID, Key: m.Key, Ref: ref, Broken: !safe})
	}
	return actual, rows.Err()
}
