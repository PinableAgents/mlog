#!/usr/bin/env bash
# Use the supported toolchain selected by setup-go / GOTOOLCHAIN, not the minimum Go baseline.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
out="$(mkdir -p "${1:?usage: security_gate.sh OUTPUT_DIR}" && cd "$1" && pwd)"
export GOFLAGS=-mod=readonly
source_sha="$(git rev-parse HEAD)"
{ go version; git rev-parse HEAD; } > "$out/environment.txt"
go test -race -count=1 -timeout=180s ./... 2>&1 | tee "$out/race.txt"
# Isolated pinned scanner; do not accidentally execute an old binary from PATH.
mkdir -p "$out/tools"
GOBIN="$out/tools" go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
# Text mode and pipefail are intentional: any finding, scan or network error is fatal.
"$out/tools/govulncheck" ./... 2>&1 | tee "$out/govulncheck.txt"
test "$(git rev-parse HEAD)" = "$source_sha"
git diff --exit-code "$source_sha" --
printf '%s\n' "$source_sha" > "$out/validated-sha.txt"
