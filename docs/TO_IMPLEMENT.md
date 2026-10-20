# To implement — Lease

Single source of pending work. Updated: 2026-10-20. Finished work moves to
[IMPLEMENTED.md](IMPLEMENTED.md) with evidence.

## Phase 0 · MVP (core + PostgreSQL + CLI)

| ID | Task | State | Done when |
|---|---|---|---|
| `LEASE-001` | Grant state machine: implement `grant.Next` from the table in the design. **Maintainer writes it.** | Ready (tests written, failing by design) | `go test ./internal/grant` passes; no transition outside the table is accepted. |
| `LEASE-002` | Reconciler diff: implement `reconcile.Diff`. **Maintainer writes it.** | Ready (tests written, failing by design) | `go test ./internal/reconcile` passes, including the `expires_at == now` boundary. |
| `LEASE-003` | Store: SQLite schema for entitlements, grants and the hash-chained event log; intent persisted before provider calls. | Planned | Migration + tests for restart recovery of non-terminal grants. |
| `LEASE-004` | PostgreSQL provider: ephemeral role with `VALID UNTIL`, idempotent per grant ID, `Observe` from `pg_roles`/memberships. | Planned | Integration test against a disposable database only. |
| `LEASE-005` | CLI: `lease request`, `approve`, `deny`, `revoke`, `list`, `reconcile`. | Planned | End-to-end demo on a disposable database. |

## Later phases

| ID | Task | State |
|---|---|---|
| `LEASE-010` | SSH certificate provider (short-validity user certs). | Planned (phase 1) |
| `LEASE-011` | Approvals (CLI/web), break-glass with mandatory post-review. | Planned (phase 1) |
| `LEASE-020` | HTTP API, agents as principals. | Planned (phase 2) |
| `LEASE-021` | Optional `Decider` (typed AI decision, stricter-only). | Planned (phase 2) |
| `LEASE-030` | Switchboard integration (time-bound claim elevation, `ACC`). | Planned (phase 3) |
