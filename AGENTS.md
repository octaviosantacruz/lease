# AGENTS.md

Instructions for any coding agent working on Lease.

## Required context

Read before changing code:

1. `AGENTS.md` (this file).
2. `docs/architecture/design.md`: concepts, lifecycle, revocation guarantees.
3. `docs/TO_IMPLEMENT.md`: the task's ID, state and done criteria.

The workspace `../AGENTS.md` and `../TO_IMPLEMENT.md` (`IDEA-009`) hold the
cross-project context.

## Working mode: learning project

The maintainer is learning Go. Explain Go idioms briefly (in Spanish) in any code
you write, and leave well-scoped core pieces to the maintainer with hints and
review. `LEASE-001` and `LEASE-002` (state machine and reconciler diff) are
theirs: do not implement them unless asked explicitly.

## Rules

- Pure core: `internal/grant` and `internal/reconcile` do no I/O and read no
  clock; time is passed in. They are tested without a database.
- Fail closed: a grant whose outcome is unknown is treated as applied and
  revoked. An expiry equal to `now` is expired.
- Write intent before acting: persist a transition before calling a provider.
- Providers are idempotent per grant ID and prefer native expiry at the target.
- AI (`Decider`) is optional and may only make a decision stricter than policy.
- Plane boundary: Lease is trust plane. It does not manage processes, units or
  ports (Server Ops). Switchboard may consume it later; no dependency the other
  way.
- Only disposable lab targets in tests and demos. Never print or commit secrets.

## Verification

`scripts/verify.sh` before reporting any change as done. While `LEASE-001`/`002`
are open, their tests fail by design: report that, do not hide it.

No commit, push or publication without an explicit request.
