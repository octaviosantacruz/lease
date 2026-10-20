# Lease

Zero standing privilege for humans and agents: nobody holds permanent access.
A principal asks for an entitlement, a policy decides, access is granted with an
expiry, **revocation is guaranteed**, and a reconciler proves nothing was left
behind.

Status: **design + scaffold (2026-10-20)**. Nothing is usable yet. Working name.

## Why

Open-source just-in-time access exists only in pieces: Teleport Community lacks
approval workflows (Enterprise feature), AWS TEAM covers only AWS, and small tools
cover one target each. The commercial products (CyberArk, BeyondTrust, Britive,
Opal, Apono) are closed. Lease is a small, readable, target-agnostic broker.

The hard part is not the approval flow. It is guaranteeing revocation when the
broker crashes, a revoke fails, or someone grants a role by hand. See
[the design](docs/architecture/design.md).

## Layout

```
cmd/lease/            CLI entry point (stub)
internal/grant/       grant lifecycle state machine (pure)
internal/reconcile/   expected-vs-actual diff (pure)
internal/provider/    Provider interface: Grant, Revoke, Observe
docs/                 design, trackers
scripts/verify.sh     local gate
```

## Commands

```bash
go test -race ./...    # tests
scripts/verify.sh      # full gate
```

## Non-goals

Not a password vault, not session recording, not an identity provider, and not a
replacement for each application's own authorization.
