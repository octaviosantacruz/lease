// Package lab simulates a target in a separate SQLite file. It never grants OS access.
package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/octaviosantacruz/lease/internal/provider"
	_ "modernc.org/sqlite"
)

type Provider struct{ db *sql.DB }

func Open(path string) (*Provider, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("lab: path required")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("lab: regular file required")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS privileges (id TEXT PRIMARY KEY NOT NULL, body BLOB NOT NULL) STRICT"); err != nil {
		db.Close()
		return nil, err
	}
	return &Provider{db: db}, nil
}
func (p *Provider) Close() error                { return p.db.Close() }
func (p *Provider) Capabilities() provider.Caps { return provider.Caps{NativeExpiry: false} }
func (p *Provider) Grant(ctx context.Context, g provider.GrantSpec) (provider.Handle, error) {
	if g.GrantID == "" || !strings.HasPrefix(g.Key.Target, "lab/") {
		return provider.Handle{}, errors.New("lab: invalid grant")
	}
	a := provider.Actual{GrantID: g.GrantID, Key: g.Key, Ref: g.GrantID}
	body, err := json.Marshal(a)
	if err != nil {
		return provider.Handle{}, err
	}
	if _, err = p.db.ExecContext(ctx, "INSERT INTO privileges VALUES (?, ?) ON CONFLICT(id) DO NOTHING", g.GrantID, body); err != nil {
		return provider.Handle{}, err
	}
	var stored []byte
	if err = p.db.QueryRowContext(ctx, "SELECT body FROM privileges WHERE id = ?", g.GrantID).Scan(&stored); err != nil {
		return provider.Handle{}, err
	}
	if string(stored) != string(body) {
		return provider.Handle{}, errors.New("lab: idempotency conflict")
	}
	return provider.Handle{GrantID: g.GrantID, Key: g.Key, Ref: g.GrantID}, nil
}
func (p *Provider) Revoke(ctx context.Context, h provider.Handle) error {
	ref := h.Ref
	if ref == "" {
		ref = h.GrantID
	}
	var body []byte
	err := p.db.QueryRowContext(ctx, "SELECT body FROM privileges WHERE id = ?", ref).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var a provider.Actual
	if json.Unmarshal(body, &a) != nil || a.Key != h.Key {
		return errors.New("lab: revoke ownership mismatch")
	}
	_, err = p.db.ExecContext(ctx, "DELETE FROM privileges WHERE id = ?", ref)
	return err
}
func (p *Provider) Observe(ctx context.Context, target string) ([]provider.Actual, error) {
	if !strings.HasPrefix(target, "lab/") {
		return nil, errors.New("lab: invalid target")
	}
	rows, err := p.db.QueryContext(ctx, "SELECT body FROM privileges ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []provider.Actual{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var a provider.Actual
		if err := json.Unmarshal(body, &a); err != nil {
			return nil, fmt.Errorf("lab: corrupt privilege: %w", err)
		}
		if a.Key.Target == target {
			result = append(result, a)
		}
	}
	return result, rows.Err()
}

// Check verifies that the isolated target can still read its privilege table.
func (p *Provider) Check(ctx context.Context) error {
	var count int
	return p.db.QueryRowContext(ctx, "SELECT count(*) FROM privileges").Scan(&count)
}
