package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrOperator = errors.New("postgres: operator prerequisites not satisfied")
	ErrUnsafe   = errors.New("postgres: unsafe principal or entitlement role")
)

// La interfaz mínima permite usar el mismo control sobre DB o sobre la
// transacción que concederá el permiso, sin abrir una segunda conexión.
type catalog interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (p *Provider) checkOperator(ctx context.Context, q catalog, base string) error {
	var permitted bool
	err := q.QueryRowContext(ctx, `SELECT
 current_user=session_user AND NOT rolsuper AND rolcreaterole AND NOT rolinherit
 AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls
 AND pg_has_role(current_user,'pg_signal_backend','USAGE')
 AND has_function_privilege(current_user,'pg_catalog.pg_terminate_backend(integer,bigint)','EXECUTE')
 AND EXISTS (SELECT 1 FROM pg_auth_members m JOIN pg_roles b ON b.oid=m.roleid
  WHERE m.member=r.oid AND b.rolname=$1 AND m.admin_option AND NOT m.inherit_option AND NOT m.set_option)
 FROM pg_roles r WHERE rolname=current_user`, base).Scan(&permitted)
	if err != nil {
		return err
	}
	if !permitted {
		return fmt.Errorf("%w: require NOSUPERUSER CREATEROLE NOINHERIT, signal permission and ADMIN-only membership on %s", ErrOperator, base)
	}
	return nil
}

func checkRole(ctx context.Context, q catalog, name string, login bool) error {
	// UNION (sin ALL) corta ciclos y evita repetir caminos. Se revisan todas las
	// membresías, incluso inactivas: ADMIN permite al miembro volver a concederlas.
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE reachable(oid) AS (
 SELECT oid FROM pg_roles WHERE rolname=$1
 UNION
 SELECT m.roleid FROM pg_auth_members m JOIN reachable x ON m.member=x.oid
 ) SELECT r.rolname,r.rolcanlogin,
 r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication OR r.rolbypassrls OR substr(r.rolname,1,3)='pg_',
 EXISTS(SELECT 1 FROM pg_auth_members m WHERE m.member=r.oid AND m.admin_option)
 FROM reachable x JOIN pg_roles r ON r.oid=x.oid ORDER BY r.rolname`, name)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var role string
		var canLogin, dangerous, admin bool
		if err = rows.Scan(&role, &canLogin, &dangerous, &admin); err != nil {
			return err
		}
		if role == name {
			found = true
			if canLogin != login {
				return fmt.Errorf("%w: unexpected LOGIN attribute on %s", ErrUnsafe, name)
			}
		} else if canLogin {
			dangerous = true
		}
		if dangerous || admin {
			return fmt.Errorf("%w: privileged or delegable path from %s through %s", ErrUnsafe, name, role)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: missing role %s", ErrUnsafe, name)
	}
	return nil
}

// Check is a read-only preflight. It never creates, grants, revokes or terminates
// sessions. Grant repeats the relevant checks immediately before DDL.
func (p *Provider) Check(ctx context.Context) error {
	var failures []error
	for _, base := range values(p.config.Roles) {
		if err := p.checkOperator(ctx, p.db, base); err != nil {
			failures = append(failures, err)
		}
		if err := checkRole(ctx, p.db, base, false); err != nil {
			failures = append(failures, err)
		}
	}
	for _, login := range values(p.config.Principals) {
		if err := checkRole(ctx, p.db, login, true); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func values(m map[string]string) []string {
	result := make([]string, 0, len(m))
	seen := map[string]bool{}
	for _, v := range m {
		if !seen[v] {
			result = append(result, v)
			seen[v] = true
		}
	}
	sort.Strings(result)
	return result
}
