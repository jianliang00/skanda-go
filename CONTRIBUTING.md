# Contributing

Thank you for improving `skanda-go`. This project is a Go rewrite of the original C++ `Calorado/Skanda` implementation, so compatibility and decoder safety are the primary review criteria.

Unless explicitly stated otherwise, contributions are submitted under the Apache License, Version 2.0. Upstream C++ copyright and license notices must remain intact.

## Development Setup

Requirements:

- Go 1.22 or newer.
- A C++ compiler for interoperability tests.
- The upstream `Skanda.h` header when running C++/Go compatibility checks.
- Python with Pillow when regenerating report charts.

Install optional chart dependency:

```sh
python3 -m pip install pillow
```

## Standard Checks

Run the Go test suite:

```sh
go test ./...
```

Run static checks:

```sh
go vet ./...
staticcheck ./...
```

Run C++ interoperability when the upstream header is available:

```sh
SKANDA_CPP_HEADER=/path/to/Skanda.h go test -run '^TestCppCompatibilityWhenHeaderAvailable$' ./...
```

Run focused benchmarks with a local corpus file:

```sh
SKANDA_BENCH_CORPUS=/path/to/input go test -run '^$' -bench 'BenchmarkExternal' -benchmem
```

## Compatibility Rules

- Go-compressed streams must decode to the original bytes with a Skanda v1.0 decoder.
- C++-compressed Skanda v1.0 streams must decode to the original bytes with this package.
- Compressed bytes do not need to match the C++ encoder byte-for-byte.
- Decoder changes must preserve corrupt-stream checks and must not introduce panics or unbounded allocation.
- Changes to compression heuristics must include compression-size and throughput evidence.

## Documentation Rules

Update documentation when a change affects:

- Public APIs.
- Supported format features.
- Compatibility behavior.
- Error semantics.
- Performance claims or benchmark data.
- Verification commands.

Reader-facing performance charts and tables should use signed Go-vs-C++ deltas: `(Go throughput - C++ throughput) / C++ throughput`.

## Pull Request Checklist

Before opening a pull request:

- Run `gofmt` on changed Go files.
- Run `go test ./...`.
- Run `go vet ./...`.
- Run `staticcheck ./...` when available.
- Run C++ interoperability tests for format-affecting changes.
- Update `verification_report.md` and `verification_artifacts` when publishing new performance or compatibility data.
- Keep generated temporary files out of the repository.
