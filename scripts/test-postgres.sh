#!/usr/bin/env bash
# Creates a private disposable cluster; never uses the host's configured databases.
set -euo pipefail
cd "$(dirname "$0")/.."
pg_bin="${LEASE_PG_BIN:-/usr/lib/postgresql/18/bin}"
for tool in initdb pg_ctl; do
  if [[ ! -x "$pg_bin/$tool" ]]; then echo "Set LEASE_PG_BIN to a PostgreSQL 18+ bin directory" >&2; exit 1; fi
done
lab_dir="$(mktemp -d /tmp/lease-postgres.XXXXXX)"
mkdir -m 700 "$lab_dir/socket"
cleanup() {
  "$pg_bin/pg_ctl" -D "$lab_dir/data" -m immediate -w stop > /dev/null 2>&1 || true
  rm -rf -- "$lab_dir"
}
trap cleanup EXIT INT TERM
"$pg_bin/initdb" -D "$lab_dir/data" -U lease_lab_admin --auth=trust --no-locale > "$lab_dir/init.log"
if ! "$pg_bin/pg_ctl" -D "$lab_dir/data" -l "$lab_dir/server.log" -o "-k $lab_dir/socket -c listen_addresses=''" -w start > /dev/null; then
  cat "$lab_dir/server.log" >&2
  exit 1
fi
printf 'lease-disposable-postgres\n' > "$lab_dir/LEASE_DISPOSABLE"
export LEASE_PG_TEST_DIR="$lab_dir"
export LEASE_PG_TEST_DSN="host=$lab_dir/socket user=lease_lab_admin dbname=postgres sslmode=disable"
go test -race -count=1 -v ./internal/provider/postgres
