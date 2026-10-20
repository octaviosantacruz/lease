#!/usr/bin/env bash
# Local gate: run before reporting any change as complete.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[1/5] gofmt"
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  echo "Files need gofmt:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "[2/5] go vet"
go vet ./...

echo "[3/5] go test -race"
go test -race -count=1 ./...

echo "[4/5] go build"
go build -o /dev/null ./cmd/lease

echo "[5/5] git diff --check"
git diff --check

echo "OK"
