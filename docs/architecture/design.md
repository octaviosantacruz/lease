# Lease — design

Status: proposed, updated 2026-10-22. Workspace idea `IDEA-009`.
Storage infrastructure is implemented separately in [storage.md](../storage.md);
policy, providers and the maintainer core exercises are not complete.

## Goal

Zero standing privilege (ZSP) for humans and agents: every privilege is a lease
with an expiry; active revocation and continuous verification are required.
This is a design goal, not an implemented guarantee.

## Concepts

| Concept | Meaning |
|---|---|
| Principal | Who asks: a human (external OIDC) or an agent (workload identity). |
| Entitlement | What can be requested: provider, target, role, maximum duration. E.g. `postgres/custodia/readonly`, ≤ 2h. |
| Policy | Who may request which entitlement, who approves, whether a justification or ticket is required, maximum TTL. |
| Request | One concrete ask with its justification. |
| Grant | The lease: a granted entitlement with `expires_at`. |
| Evidence | Append-only, hash-chained log of every transition. |

## Grant lifecycle

```
requested ──NeedsApproval──► pending_approval ──Approve──► approved
    │  └────AutoApprove────────────────────────────────────►  │
    └──Deny──► denied ◄──Deny── pending_approval              │
                                                    StartGrant│
                                                              ▼
              revoked ◄──Revoked── revoking ◄──Fail──── granting
                                      ▲                       │ Granted
                                      └──Expire / Revoke──── active
```

Terminal states: `denied`, `revoked`.

- **A failed grant goes to `revoking`, not to an error state.** The provider may
  have applied part of it; fail closed and clean up.
- A failed revoke stays in `revoking` with an attempt counter and an alert; it is
  retried, never abandoned.
- `Next(from, event)` is a pure function; anything not in the table is an error.

## Revocation design: three layers

1. **Native expiry of the entitlement**, when the target actually enforces it.
   Credential expiry alone may only stop new authentication; it does not prove
   that existing sessions or privileges ended. Providers must document and test
   that scope before claiming `NativeExpiry`. PostgreSQL `VALID UNTIL` expires
   the password, not the role, and does not cover non-password authentication.
   SSH certificate validity likewise describes a certificate authentication
   window; session lifetime requires its own tested enforcement. No provider
   may advertise automatic expiry of every privilege from these settings alone.
2. **Active revoke** when the lease expires or is revoked early.
3. **Reconciler.** Periodically asks each provider what privileges *actually*
   exist (`Observe`), compares them with the expected active grants and revokes
   plus alerts on any difference. This catches manual grants and failed revokes.

**Intent before action:** the transition (e.g. `granting`) is persisted before the
provider is called. After a restart, every non-terminal grant is reconciled.

PostgreSQL contract reference: [CREATE ROLE](https://www.postgresql.org/docs/18/sql-createrole.html).
SSH validity reference: [ssh-keygen -V](https://man.openbsd.org/ssh-keygen.1#V).
The PostgreSQL provider must test session termination/privilege removal and
revoke retries separately; a complete broker cannot promise a deadline during
a broker outage unless the target independently enforces that deadline.

The reconciler also yields a **standing-privilege inventory**: how far a target is
from ZSP. That is a metric to report, not only an enforcement loop.

### Reconciler diff (pure)

Input: expected grants (key + `expires_at`), actual privileges (key), `now`.
Key = (target, principal, role).

| Case | Plan |
|---|---|
| Actual with no expected grant | Revoke (standing privilege / drift) |
| Expected but expired (`expires_at <= now`) and still present | Revoke |
| Expected, active, present | Nothing |
| Expected, active, absent | Report as missing (do not re-grant automatically) |
| Expected, expired, absent | Nothing |

## Provider contract

```go
type Provider interface {
    Grant(ctx context.Context, g GrantSpec) (Handle, error) // idempotent per grant ID
    Revoke(ctx context.Context, h Handle) error             // idempotent
    Observe(ctx context.Context, t Target) ([]Actual, error)
    Capabilities() Caps                                     // e.g. NativeExpiry
}
```

Two grant modes:

- **Ephemeral credential** (default, purest ZSP): create a temporary user or
  certificate and destroy it afterwards.
- **Elevation:** add a role to an existing account for the lease duration.

Planned providers, in order: PostgreSQL, SSH certificates, temporary sudoers,
Kubernetes RoleBinding, GitHub team, AWS.

## Optional AI decision

A `Decider` receives the request plus context (who, what, time, justification,
history) and returns `{auto_approve | require_approval | deny, confidence,
reason}`. Rules: it can only make the outcome **stricter** than policy; low
confidence goes to a human; every decision is logged with its confidence. It is
swappable (decision API such as Jev, structured outputs of any LLM, local model or
none). Lease is complete without it.

## Agents

Agents request leases over the API with their workload identity and receive short
TTLs (minutes). Later: per-hop attenuated delegation (`IDEA-007`) and proof of
decommission (`IDEA-006`) on top of the reconciler.

## Storage and evidence

Single binary, SQLite. Entitlements, grants and hash-chained events are now
persisted by `internal/store` with explicit migrations and atomic state/event
commits. [Storage contract](../storage.md). There is no external anchor and no
shared code with Switchboard. The future executor must persist `granting` or
`revoking` before calling a provider; storage alone does not implement that executor.

## Relationship with other projects

- Trust plane, independent project. Switchboard may later ask “does this
  principal hold an active lease for this route?” (its `ACC` pillar). No
  dependency from Lease to Switchboard.
- Not Server Ops: no process, unit or port management.
- Custodia is unrelated: no data exchange without an approved contract.

## Non-goals

Password vault, session recording (PSM), identity provider, application RBAC.
