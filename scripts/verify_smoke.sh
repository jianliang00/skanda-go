#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

staticcheck_bin="${STATICCHECK:-}"
if [[ -z "$staticcheck_bin" ]]; then
  if command -v staticcheck >/dev/null 2>&1; then
    staticcheck_bin="staticcheck"
  elif [[ -x "$(go env GOPATH)/bin/staticcheck" ]]; then
    staticcheck_bin="$(go env GOPATH)/bin/staticcheck"
  else
    echo "missing staticcheck; install it or set STATICCHECK=/path/to/staticcheck" >&2
    exit 2
  fi
fi

go test ./...
go vet ./...
"$staticcheck_bin" ./...
go test -race ./...
go test -run TestCppCompatibilityWhenHeaderAvailable -count=1 -v
"$repo_root/scripts/compat_matrix.sh"
go test -run=^$ -fuzz=FuzzRoundTrip -fuzztime="${FUZZTIME_ROUNDTRIP:-10s}"
go test -run=^$ -fuzz=FuzzDecompress -fuzztime="${FUZZTIME_DECOMPRESS:-5s}"
go test -run=^$ -fuzz=FuzzCorruptStream -fuzztime="${FUZZTIME_CORRUPT:-5s}"
go test -run=^$ -fuzz=FuzzOptions -fuzztime="${FUZZTIME_OPTIONS:-5s}"
go test -run=^$ -fuzz=FuzzHeader -fuzztime="${FUZZTIME_HEADER:-5s}"
go test -bench=. -run '^$' -benchtime="${BENCHTIME:-200ms}"
