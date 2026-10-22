package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// This in-process catalog model exercises the recursive query and rejection
// rules without sockets. It does NOT replace the real PostgreSQL integration.
func TestCatalogPaths(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		root   string
		login  bool
		unsafe bool
	}{
		{"safe custom chain", []string{"INSERT INTO pg_auth_members VALUES (2,1,0),(3,2,0)"}, "analyst", true, false},
		{"direct privileged attribute", []string{"UPDATE pg_roles SET rolcreatedb=1 WHERE oid=1"}, "analyst", true, true},
		{"transitive privileged attribute", []string{"UPDATE pg_roles SET rolcreaterole=1 WHERE oid=3", "INSERT INTO pg_auth_members VALUES (2,1,0),(3,2,0)"}, "analyst", true, true},
		{"predefined ancestor", []string{"UPDATE pg_roles SET rolname='pg_read_server_files' WHERE oid=3", "INSERT INTO pg_auth_members VALUES (2,1,0),(3,2,0)"}, "analyst", true, true},
		{"delegable ancestor", []string{"INSERT INTO pg_auth_members VALUES (2,1,0),(3,2,1)"}, "analyst", true, true},
		{"ancestor is another login", []string{"UPDATE pg_roles SET rolcanlogin=1 WHERE oid=2", "INSERT INTO pg_auth_members VALUES (2,1,0)"}, "analyst", true, true},
		{"unexpected login", nil, "analyst", false, true},
		{"missing login", nil, "missing", true, true},
		{"unrelated dangerous role", []string{"UPDATE pg_roles SET rolsuper=1 WHERE oid=3"}, "analyst", true, false},
		{"duplicate paths terminate", []string{"INSERT INTO pg_auth_members VALUES (2,1,0),(3,2,0),(2,3,0)"}, "analyst", true, false},
		{"quoted name is data", []string{"UPDATE pg_roles SET rolname='analyst''quoted' WHERE oid=1"}, "analyst'quoted", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			statements := []string{
				"CREATE TABLE pg_roles(oid INTEGER PRIMARY KEY,rolname TEXT,rolcanlogin INTEGER,rolsuper INTEGER DEFAULT 0,rolcreaterole INTEGER DEFAULT 0,rolcreatedb INTEGER DEFAULT 0,rolreplication INTEGER DEFAULT 0,rolbypassrls INTEGER DEFAULT 0)",
				"CREATE TABLE pg_auth_members(roleid INTEGER,member INTEGER,admin_option INTEGER)",
				"INSERT INTO pg_roles(oid,rolname,rolcanlogin) VALUES (1,'analyst',1),(2,'reader',0),(3,'group',0)",
			}
			for _, statement := range append(statements, tc.sql...) {
				if _, err = db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = checkRole(ctx, db, tc.root, tc.login)
			if tc.unsafe && !errors.Is(err, ErrUnsafe) {
				t.Fatalf("unsafe path accepted: %v", err)
			}
			if !tc.unsafe && err != nil {
				t.Fatal(err)
			}
		})
	}
}
