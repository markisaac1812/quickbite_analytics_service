#!/usr/bin/env bash
# Coverage gate: unit AND integration tests in one `go test` run, coverage
# merged across packages, fails below the threshold. Go analogue of
# order-service's `npm run test:coverage` (jest.coverage.config.js).
#
#   scripts/test-coverage.sh            # needs MongoDB (replica set) + RabbitMQ
#   COVERAGE_THRESHOLD=85 scripts/test-coverage.sh
#
# Measured: app/, lib/, pkg/. Excluded (like server.ts/worker.ts on the Node
# side): cmd/* entrypoints, play/ dev aids (gitignored), internal/testkit.
set -euo pipefail
cd "$(dirname "$0")/.."

THRESHOLD="${COVERAGE_THRESHOLD:-80}"
OUT="coverage.out"

COVER_PKGS=$(go list ./... | grep -v -E '/cmd/|/play/|/internal/testkit' | paste -sd, -)
TEST_PKGS=$(go list ./... | grep -v -E '/play/')

# -coverpkg instruments every measured package in every test binary, so a
# package exercised only through another package's tests (e.g. repositories
# through the lib/boot integration suite) still counts.
# (sed only shortens the per-package line "... of statements in <every package>")
go test -count=1 -tags integration -covermode=atomic -coverpkg="$COVER_PKGS" -coverprofile="$OUT" $TEST_PKGS |
  sed -E 's/ of statements in .*/ of statements (across measured packages)/'

go tool cover -html="$OUT" -o coverage.html
echo
go tool cover -func="$OUT" | awk '/^total:/ {print "total statements covered: " $3}'

TOTAL=$(go tool cover -func="$OUT" | awk '/^total:/ {sub("%", "", $3); print $3}')
awk -v t="$TOTAL" -v min="$THRESHOLD" 'BEGIN {
  if (t + 0 < min + 0) { printf "FAIL: coverage %.1f%% is below %s%%\n", t, min; exit 1 }
  printf "OK: coverage %.1f%% >= %s%% (report: coverage.html)\n", t, min
}'
