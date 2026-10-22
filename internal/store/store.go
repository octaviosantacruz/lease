package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/octaviosantacruz/lease/internal/grant"
	"github.com/octaviosantacruz/lease/internal/provider"
)

var (
	ErrNotFound  = errors.New("store: record not found")
	ErrConflict  = errors.New("store: record exists or version changed")
	ErrInvalid   = errors.New("store: invalid request")
	ErrIntegrity = errors.New("store: audit integrity check failed")
)

type Entitlement struct {
	ID       string
	Provider string
	Target   string
	Role     string
	MaxTTL   time.Duration
}

// Request defines a fixed access window, not permission to grant it.
type Request struct {
	ID            string
	EntitlementID string
	Principal     string
	Reason        string
	ExpiresAt     time.Time
}

// Grant contains no credential; Ref is a provider object identifier only.
type Grant struct {
	ID            string       `json:"id"`
	EntitlementID string       `json:"entitlement_id"`
	Provider      string       `json:"provider"`
	Key           provider.Key `json:"key"`
	Reason        string       `json:"reason"`
	State         grant.State  `json:"state"`
	Version       int64        `json:"version"`
	ExpiresAt     time.Time    `json:"expires_at"`
	Ref           string       `json:"ref"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

type Change struct {
	GrantID string
	Version int64
	Event   grant.Event
	Actor   string
	Ref     string
}

type Store struct {
	db *sql.DB
	// Privado: los tests aíslan persistencia sin resolver el ejercicio LEASE-001.
	// Open siempre usa grant.Next; no existe un bypass público de la máquina.
	next func(grant.State, grant.Event) (grant.State, error)
}

// Open checks the existing schema and audit trail; it never migrates on startup.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := connect(ctx, path, false)
	if err != nil {
		return nil, err
	}
	version, err := schemaVersion(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if version == 0 {
		db.Close()
		return nil, ErrNeedsMigration
	}
	s := &Store{db: db, next: grant.Next}
	if err = s.Verify(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func nonempty(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func validTime(at time.Time) bool {
	// UnixNano necesita un rango representable: evitar que una fecha desborde el índice.
	return !at.IsZero() && time.Unix(0, at.UnixNano()).Equal(at)
}

// CreateEntitlement stores an immutable entitlement. A change needs a new ID.
func (s *Store) CreateEntitlement(ctx context.Context, e Entitlement) error {
	if !nonempty(e.ID, e.Provider, e.Target, e.Role) || e.MaxTTL <= 0 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM entitlements WHERE id = ?", e.ID).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return ErrConflict
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO entitlements VALUES (?, ?, ?, ?, ?)", e.ID, e.Provider, e.Target, e.Role, int64(e.MaxTTL))
		return err
	})
}

// CreateRequest atomically stores Requested and its first event. Policy runs later.
func (s *Store) CreateRequest(ctx context.Context, r Request, actor string, at time.Time) (Grant, error) {
	return s.createRequest(ctx, r, actor, at, "")
}

// CreateCleanup atomically adopts an orphan as Revoking, never as an approvable request.
func (s *Store) CreateCleanup(ctx context.Context, r Request, ref, actor string, at time.Time) (Grant, error) {
	if !nonempty(ref) {
		return Grant{}, ErrInvalid
	}
	return s.createRequest(ctx, r, actor, at, ref)
}
func (s *Store) createRequest(ctx context.Context, r Request, actor string, at time.Time, cleanupRef string) (Grant, error) {
	if !nonempty(r.ID, r.EntitlementID, r.Principal, r.Reason, actor) || !validTime(at) || !validTime(r.ExpiresAt) || !at.Before(r.ExpiresAt) {
		return Grant{}, ErrInvalid
	}
	var result Grant
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := verify(ctx, tx); err != nil {
			return err
		}
		var e Entitlement
		var ttl int64
		err := tx.QueryRowContext(ctx, "SELECT id, provider, target, role, max_ttl_ns FROM entitlements WHERE id = ?", r.EntitlementID).Scan(&e.ID, &e.Provider, &e.Target, &e.Role, &ttl)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// Comparar instantes evita la saturación de Time.Sub en ventanas muy largas.
		if r.ExpiresAt.After(at.Add(time.Duration(ttl))) {
			return ErrInvalid
		}
		var exists int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM grants WHERE id = ?", r.ID).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return ErrConflict
		}
		result = Grant{ID: r.ID, EntitlementID: e.ID, Provider: e.Provider,
			Key: provider.Key{Target: e.Target, Principal: r.Principal, Role: e.Role}, Reason: r.Reason,
			State: grant.Requested, Version: 1, ExpiresAt: r.ExpiresAt.UTC(), CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO grants VALUES (?, ?, ?, ?, ?, ?)", result.ID, result.EntitlementID, result.State, result.Version, result.ExpiresAt.UnixNano(), body)
		if err != nil {
			return err
		}
		if err = appendEvent(ctx, tx, result, "created", actor); err != nil {
			return err
		}
		if cleanupRef != "" {
			result.State, result.Version, result.Ref = grant.Revoking, 2, cleanupRef
			payload, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE grants SET state=?,version=?,payload=? WHERE id=?", result.State, result.Version, payload, result.ID); err != nil {
				return err
			}
			return appendEvent(ctx, tx, result, string(grant.Revoke), actor)
		}
		return nil
	})
	if err != nil {
		return Grant{}, err
	}
	return result, nil
}

const grantColumns = "id, entitlement_id, state, version, expires_at_ns, payload"

type scanner interface{ Scan(...any) error }

func readGrant(row scanner) (Grant, []byte, error) {
	var g Grant
	var id, entitlement, state string
	var version, expires int64
	var payload []byte
	err := row.Scan(&id, &entitlement, &state, &version, &expires, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return g, nil, ErrNotFound
	}
	if err != nil {
		return g, nil, err
	}
	if err = json.Unmarshal(payload, &g); err != nil {
		return g, nil, fmt.Errorf("%w: malformed grant", ErrIntegrity)
	}
	if g.ID != id || g.EntitlementID != entitlement || string(g.State) != state || g.Version != version || !validTime(g.ExpiresAt) || g.ExpiresAt.UnixNano() != expires {
		return g, nil, ErrIntegrity
	}
	return g, payload, nil
}

func (s *Store) Get(ctx context.Context, id string) (Grant, error) {
	var result Grant
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := verify(ctx, tx); err != nil {
			return err
		}
		g, _, err := readGrant(tx.QueryRowContext(ctx, "SELECT "+grantColumns+" FROM grants WHERE id = ?", id))
		if err != nil {
			return err
		}
		result = g
		return nil
	})
	return result, err
}

// Advance commits intent and evidence together. Only after it succeeds may an
// executor call a provider. It does not itself grant, revoke or approve policy.
func (s *Store) Advance(ctx context.Context, change Change, at time.Time) (Grant, error) {
	if !nonempty(change.GrantID, change.Actor) || change.Version < 1 || !validTime(at) {
		return Grant{}, ErrInvalid
	}
	var result Grant
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := verify(ctx, tx); err != nil {
			return err
		}
		g, _, err := readGrant(tx.QueryRowContext(ctx, "SELECT "+grantColumns+" FROM grants WHERE id = ?", change.GrantID))
		if err != nil {
			return err
		}
		if g.Version != change.Version {
			return ErrConflict
		}
		if at.Before(g.UpdatedAt) {
			return ErrInvalid
		}
		next, err := s.next(g.State, change.Event)
		if err != nil {
			return err
		}
		if next == grant.Granting && !at.Before(g.ExpiresAt) {
			return ErrInvalid
		}
		if change.Ref != "" {
			if change.Event != grant.StartGrant && change.Event != grant.Granted && change.Event != grant.Revoke {
				return ErrInvalid
			}
			if g.Ref != "" && g.Ref != change.Ref {
				return ErrConflict
			}
			g.Ref = change.Ref
		}
		g.State, g.Version, g.UpdatedAt = next, g.Version+1, at.UTC()
		payload, err := json.Marshal(g)
		if err != nil {
			return err
		}
		row, err := tx.ExecContext(ctx, "UPDATE grants SET state = ?, version = ?, payload = ? WHERE id = ? AND version = ?", g.State, g.Version, payload, g.ID, change.Version)
		if err != nil {
			return err
		}
		affected, err := row.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrConflict
		}
		if err = appendEvent(ctx, tx, g, string(change.Event), change.Actor); err != nil {
			return err
		}
		result = g
		return nil
	})
	if err != nil {
		return Grant{}, err
	}
	return result, nil
}

// Recoverable includes every non-terminal intent, even expired or unknown grants.
// Deciding cleanup belongs to the maintainer's reconciler, never to this query.
func (s *Store) Recoverable(ctx context.Context) ([]Grant, error) {
	var result []Grant
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := verify(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+grantColumns+" FROM grants WHERE state NOT IN ('denied', 'revoked') ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			g, _, err := readGrant(rows)
			if err != nil {
				return err
			}
			result = append(result, g)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
