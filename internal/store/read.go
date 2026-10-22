package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Snapshot is read in one transaction so the console never mixes revisions.
type Snapshot struct {
	Entitlements []Entitlement `json:"entitlements"`
	Grants       []Grant       `json:"grants"`
	Events       []Event       `json:"events"`
	Head         string        `json:"head"`
}

type Event struct {
	Sequence     int64           `json:"sequence"`
	GrantID      string          `json:"grant_id"`
	Body         json.RawMessage `json:"body"`
	PreviousHash string          `json:"previous_hash"`
	Hash         string          `json:"hash"`
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	result := Snapshot{Entitlements: []Entitlement{}, Grants: []Grant{}, Events: []Event{}}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := verify(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT id, provider, target, role, max_ttl_ns FROM entitlements ORDER BY id")
		if err != nil {
			return err
		}
		for rows.Next() {
			var e Entitlement
			if err := rows.Scan(&e.ID, &e.Provider, &e.Target, &e.Role, &e.MaxTTL); err != nil {
				rows.Close()
				return err
			}
			result.Entitlements = append(result.Entitlements, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, "SELECT "+grantColumns+" FROM grants ORDER BY id")
		if err != nil {
			return err
		}
		for rows.Next() {
			g, _, err := readGrant(rows)
			if err != nil {
				rows.Close()
				return err
			}
			result.Grants = append(result.Grants, g)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, "SELECT sequence, grant_id, body, previous_hash, hash FROM events ORDER BY sequence")
		if err != nil {
			return err
		}
		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.Sequence, &e.GrantID, &e.Body, &e.PreviousHash, &e.Hash); err != nil {
				rows.Close()
				return err
			}
			result.Events = append(result.Events, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT hash FROM chain_head WHERE singleton = 1").Scan(&result.Head)
	})
	return result, err
}
