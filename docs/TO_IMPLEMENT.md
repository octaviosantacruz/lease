# To implement — Lease

Single source of pending work. Updated: 2026-10-22. Finished work moves to
[IMPLEMENTED.md](IMPLEMENTED.md) with evidence.

## Phase 0 · MVP (core + PostgreSQL + CLI)

| ID | Task | State | Done when |
|---|---|---|---|
| `LEASE-001` | Grant state machine: implement `grant.Next` from the table in the design. **Maintainer writes it.** | Ready (tests written, failing by design) | `go test ./internal/grant` passes; no transition outside the table is accepted. |
| `LEASE-002` | Reconciler diff: implement `reconcile.Diff`. **Maintainer writes it.** | Ready (tests written, failing by design) | `go test ./internal/reconcile` passes, including the `expires_at == now` boundary. |
| `LEASE-004` | PostgreSQL provider: ephemeral role, password validity, active revoke and observation. | Planned; storage base available | Disposable integration tests for new and existing sessions, non-password authentication, unknown outcomes and cleanup by GrantID. `VALID UNTIL` alone is not full privilege expiry; do not claim NativeExpiry from it. |
| `LEASE-005` | CLI: `lease request`, `approve`, `deny`, `revoke`, `list`, `reconcile`. | In progress: only explicit local `db up/status/verify` exists | Request/approval/revoke workflow and end-to-end demo on a disposable database remain pending; storage commands do not grant access. |

## Later phases

| ID | Task | State |
|---|---|---|
| `LEASE-010` | SSH certificate provider (short-validity user certs). | Planned (phase 1) |
| `LEASE-011` | Approvals (CLI/web), break-glass with mandatory post-review. | Planned (phase 1) |
| `LEASE-020` | HTTP API, agents as principals. | Planned (phase 2) |
| `LEASE-021` | Optional `Decider` (typed AI decision, stricter-only). | Planned (phase 2) |
| `LEASE-030` | Switchboard integration (time-bound claim elevation, `ACC`). | Planned (phase 3) |

`LEASE-003` moved to [IMPLEMENTED.md](IMPLEMENTED.md). It does not unblock
a functional broker until the maintainer completes `LEASE-001`/`LEASE-002`.
The full gate remains failing by design for those two packages.
