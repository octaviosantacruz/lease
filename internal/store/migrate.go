// Package store persists broker intent and its audit trail in SQLite.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var initialSchema string

var (
	ErrNeedsMigration = errors.New("store: run the explicit database migration first")
	ErrSchema         = errors.New("store: unknown or modified schema")
)

func connect(ctx context.Context, path string, create bool) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" {
		return nil, errors.New("store: a database file path is required")
	}
	if create {
		file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			if err = file.Close(); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("store: database must be a regular file, not a symlink")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := url.URL{Scheme: "file", Path: absolute}
	query := url.Values{"_txlock": {"immediate"}, "_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "synchronous(FULL)"}}
	dsn.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	// database/sql mantiene un pool: una conexión simplifica la escritura local.
	// Los pragmas van en el DSN para aplicarse también si la conexión se reemplaza.
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func schemaVersion(ctx context.Context, q queryer) (int, error) {
	rows, err := q.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'")
	if err != nil {
		return 0, err
	}
	count, hasVersions := 0, false
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return 0, err
		}
		count++
		hasVersions = hasVersions || name == "schema_migrations"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	if !hasVersions {
		return 0, ErrSchema
	}
	rows, err = q.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	defer rows.Close()
	count = 0
	for rows.Next() {
		var version int
		var checksum string
		if err = rows.Scan(&version, &checksum); err != nil {
			return 0, fmt.Errorf("%w: %v", ErrSchema, err)
		}
		count++
		if version != 1 || checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(initialSchema))) {
			return 0, ErrSchema
		}
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, ErrSchema
	}
	return 1, nil
}

// MigrationStatus does not create a missing database or apply migrations.
func MigrationStatus(ctx context.Context, path string) (int, error) {
	db, err := connect(ctx, path, false)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return schemaVersion(ctx, db)
}

// Migrate explicitly initializes an empty file; applied SQL is checksummed.
func Migrate(ctx context.Context, path string) error {
	db, err := connect(ctx, path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// defer garantiza rollback también en los retornos tempranos; tras Commit no hace nada.
	defer tx.Rollback()
	version, err := schemaVersion(ctx, tx)
	if err != nil {
		return err
	}
	if version == 1 {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, initialSchema); err != nil {
		return err
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(initialSchema)))
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (1, ?)", checksum); err != nil {
		return err
	}
	return tx.Commit()
}
