package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/octaviosantacruz/lease/internal/grant"
)

const genesis = "0000000000000000000000000000000000000000000000000000000000000000"

type auditEvent struct {
	Sequence int64     `json:"sequence"`
	Event    string    `json:"event"`
	Actor    string    `json:"actor"`
	At       time.Time `json:"at"`
	Grant    Grant     `json:"grant"`
}

func eventHash(previous string, body []byte) string {
	hash := sha256.New()
	hash.Write([]byte("lease-event-v1\x00"))
	hash.Write([]byte(previous))
	hash.Write([]byte{0})
	hash.Write(body)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func appendEvent(ctx context.Context, tx *sql.Tx, g Grant, event, actor string) error {
	var sequence int64
	var previous string
	if err := tx.QueryRowContext(ctx, "SELECT sequence, hash FROM chain_head WHERE singleton = 1").Scan(&sequence, &previous); err != nil {
		return err
	}
	sequence++
	body, err := json.Marshal(auditEvent{Sequence: sequence, Event: event, Actor: actor, At: g.UpdatedAt, Grant: g})
	if err != nil {
		return err
	}
	digest := eventHash(previous, body)
	if _, err = tx.ExecContext(ctx, "INSERT INTO events VALUES (?, ?, ?, ?, ?)", sequence, g.ID, body, previous, digest); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE chain_head SET sequence = ?, hash = ? WHERE singleton = 1", sequence, digest)
	return err
}

// Verify checks the chain, its head and the latest snapshot of every grant.
// It detects local inconsistency; an administrator can rewrite an unanchored
// database and recompute hashes. This is not a signature or external proof.
func (s *Store) Verify(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error { return verify(ctx, tx) })
}

func verify(ctx context.Context, tx *sql.Tx) error {
	var headSequence int64
	var headHash string
	if err := tx.QueryRowContext(ctx, "SELECT sequence, hash FROM chain_head WHERE singleton = 1").Scan(&headSequence, &headHash); err != nil {
		return fmt.Errorf("%w: head: %v", ErrIntegrity, err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT sequence, grant_id, body, previous_hash, hash FROM events ORDER BY sequence")
	if err != nil {
		return fmt.Errorf("%w: events: %v", ErrIntegrity, err)
	}
	latest := make(map[string]Grant)
	sequence, previous := int64(0), genesis
	for rows.Next() {
		var id, prev, digest string
		var body []byte
		var seq int64
		if err = rows.Scan(&seq, &id, &body, &prev, &digest); err != nil {
			rows.Close()
			return err
		}
		var event auditEvent
		if json.Unmarshal(body, &event) != nil || seq != sequence+1 || prev != previous || digest != eventHash(prev, body) || event.Sequence != seq || event.Grant.ID != id || !nonempty(event.Actor, event.Event) || !event.At.Equal(event.Grant.UpdatedAt) || !validTime(event.At) {
			rows.Close()
			return fmt.Errorf("%w: event %d", ErrIntegrity, seq)
		}
		g := event.Grant
		before, exists := latest[id]
		if !exists {
			if g.Version != 1 || g.State != grant.Requested || event.Event != "created" || !g.CreatedAt.Equal(g.UpdatedAt) {
				rows.Close()
				return ErrIntegrity
			}
		} else {
			if before.State.Terminal() || g.Version != before.Version+1 || g.EntitlementID != before.EntitlementID || g.Provider != before.Provider || g.Key != before.Key || g.Reason != before.Reason || !g.CreatedAt.Equal(before.CreatedAt) || !g.ExpiresAt.Equal(before.ExpiresAt) || g.UpdatedAt.Before(before.UpdatedAt) || (before.Ref != "" && g.Ref != before.Ref) {
				rows.Close()
				return ErrIntegrity
			}
		}
		latest[id], sequence, previous = g, seq, digest
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if sequence != headSequence || previous != headHash {
		return fmt.Errorf("%w: head mismatch", ErrIntegrity)
	}
	rows, err = tx.QueryContext(ctx, "SELECT "+grantColumns+" FROM grants")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		g, stored, err := readGrant(rows)
		if err != nil {
			return err
		}
		snapshot, exists := latest[g.ID]
		if !exists {
			return fmt.Errorf("%w: unlogged grant", ErrIntegrity)
		}
		expected, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if !bytes.Equal(stored, expected) {
			return fmt.Errorf("%w: grant snapshot mismatch", ErrIntegrity)
		}
		delete(latest, g.ID)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(latest) != 0 {
		return fmt.Errorf("%w: missing grant", ErrIntegrity)
	}
	return nil
}
