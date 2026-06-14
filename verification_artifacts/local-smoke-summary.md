# Local Smoke Verification Summary

Generated on 2026-06-14, Asia/Shanghai.

Command:

```shell
FUZZTIME_ROUNDTRIP=1s \
FUZZTIME_DECOMPRESS=1s \
FUZZTIME_CORRUPT=5s \
FUZZTIME_OPTIONS=1s \
FUZZTIME_HEADER=1s \
./scripts/verify_smoke.sh
```

Result: pass.

Covered gates:

| Gate | Result |
|---|---:|
| `go test ./...` | Pass |
| `go vet ./...` | Pass |
| `staticcheck ./...` | Pass |
| `go test -race ./...` | Pass |
| `TestCppCompatibilityWhenHeaderAvailable` | Pass |
| `scripts/compat_matrix.sh` | Pass |
| `FuzzRoundTrip`, 1 second | Pass |
| `FuzzDecompress`, 1 second | Pass |
| `FuzzCorruptStream`, 5 seconds | Pass |
| `FuzzOptions`, 1 second | Pass |
| `FuzzHeader`, 1 second | Pass |

Benchmark smoke, 8-count local run:

| Benchmark | Throughput | Allocations |
|---|---:|---:|
| `BenchmarkCompressMixed-16` | 760.17 MB/s average | ~65836 B/op, 2 allocs/op |
| `BenchmarkDecompressMixed-16` | 5405.41 MB/s average | ~2 B/op, 0 allocs/op |
