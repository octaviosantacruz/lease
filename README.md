# Lease

Zero standing privilege for humans and agents: nobody holds permanent access.
The intended workflow is a principal requesting an entitlement, policy and
approval, time-bounded access, active revocation and reconciliation. These
access operations are not implemented yet; no live privilege is managed.

Status: **SQLite storage and database maintenance CLI (2026-10-22)**. Core
state machine and reconciler remain maintainer exercises; no functional access
broker or real provider yet. Working name.

## Why

Lease explores a small, readable, target-agnostic broker for temporary access.

The hard part is not the approval flow. It is guaranteeing revocation when the
broker crashes, a revoke fails, or someone grants a role by hand. See
[the design](docs/architecture/design.md).

## Layout

```
cmd/lease/            explicit db up/status/verify CLI (access commands pending)
internal/grant/       grant lifecycle state machine (pure)
internal/reconcile/   expected-vs-actual diff (pure)
internal/provider/    Provider interface: Grant, Revoke, Observe
internal/store/       SQLite migrations, durable intent and chained audit log
docs/                 design, trackers
scripts/verify.sh     local gate
```

## Commands

```bash
go test -race ./internal/store ./cmd/lease  # implemented infrastructure
scripts/verify.sh                           # full gate (core exercises still red)
```

Database commands and guarantees: [storage contract](docs/storage.md).
The maintainer still owns `LEASE-001` and `LEASE-002`; their tests intentionally
fail until those functions are implemented.

## Non-goals

Not a password vault, not session recording, not an identity provider, and not a
replacement for each application's own authorization.
