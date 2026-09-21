#!/usr/bin/env bash
# A single fail-closed entry point for PRs, automatic releases and release.sh.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
out="$(mkdir -p "${1:?usage: quality_gate.sh OUTPUT_DIR}" && cd "$1" && pwd)"
export GOMAXPROCS=4
export GOFLAGS=-mod=readonly
source_sha="$(git rev-parse HEAD)"
{ go version; go env GOOS GOARCH GOMOD; git rev-parse HEAD; } > "$out/environment.txt"

# Never mutate source during validation (in particular, never run go fmt).
unformatted="$(git ls-files -z -- '*.go' | xargs -0 gofmt -l)"
if [[ -n "$unformatted" ]]; then
  printf 'Unformatted Go files; fix and commit before release:\n%s\n' "$unformatted" >&2
  exit 1
fi
python3 -m unittest discover -s scripts -p 'test_*.py' 2>&1 | tee "$out/script-tests.txt"
go mod verify 2>&1 | tee "$out/modules.txt"
go test -count=1 -timeout=180s -covermode=atomic -coverpkg=./... -coverprofile="$out/coverage.out" ./... 2>&1 | tee "$out/test.txt"
go tool cover -func="$out/coverage.out" | tee "$out/coverage.txt"
python3 scripts/check_coverage.py "$out/coverage.out" | tee "$out/coverage.json"
go test -race -count=1 -timeout=180s ./... 2>&1 | tee "$out/race.txt"
go vet ./... 2>&1 | tee "$out/vet.txt"
go test -run '^$' -fuzz '^FuzzAuditRouteComponents$' -fuzztime=5s ./ 2>&1 | tee "$out/fuzz.txt"
(
  cd benchmarks
  go mod verify 2>&1 | tee "$out/benchmark-modules.txt"
  go test -race -count=1 -timeout=180s ./... 2>&1 | tee "$out/benchmark-correctness.txt"
  go vet ./... 2>&1 | tee "$out/benchmark-vet.txt"
  go test -run '^$' -bench . -benchmem -benchtime=200ms -count=5 -cpu=4 ./... 2>&1 | tee "$out/benchmarks.txt"
)
test "$(git rev-parse HEAD)" = "$source_sha"
git diff --exit-code "$source_sha" --
printf '%s\n' "$source_sha" > "$out/validated-sha.txt"
