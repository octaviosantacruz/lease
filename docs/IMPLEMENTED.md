# Implemented — Lease

## Scaffold — 2026-10-20

Repository layout, design document, trackers, verification gate, `Provider`
interface and the specification tests for `LEASE-001` and `LEASE-002`. No
functional code yet.

## LEASE-003 — SQLite intent store — 2026-10-22

Explicit checksummed migration; immutable entitlement creation; durable request
and versioned transition storage; atomic state/event commits; SHA-256 chained
events and head verification; non-terminal recovery after reopening and abrupt
process exits. No real provider is called. See [storage contract](storage.md).

CLI foundation for LEASE-005: `lease db up/status/verify --db PATH`. It never
initializes a database implicitly on status, verify or normal store opening.
The request/approval/revoke CLI is still pending.

The state-machine and diff exercises remain unchanged. Storage tests use
private doubles to isolate transactions; production always uses `grant.Next`.
This closes storage infrastructure only, not policy or revocation guarantees.

Verification observed on 2026-10-22:

- `go test -race -count=1 ./internal/store ./cmd/lease`: green, including crash,
  concurrent writer, rollback, corruption, handle and expiry-boundary cases.
- `scripts/verify.sh`: gofmt and vet green; the race stage passes storage/CLI
  and fails only `TestNextValid` and `TestDiff` in the two untouched exercises.
  Its later build/diff stages do not run after that expected failure.
- `go build -o /dev/null ./cmd/lease` and `git diff --check`: green separately.
- Built CLI smoke on a disposable `/tmp/lease-lab.*` database: status 0 before
  migration, explicit up to schema 1, audit verification OK, status 1 afterwards;
  the file is mode 0600. These schema versions are not process exit codes.
- Local Markdown links validated. No live provider, production database,
  credential, Custodia checkout or service was used. Lease remains uncommitted.
