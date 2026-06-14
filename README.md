# skanda-go

`skanda-go` is a pure Go rewrite of the original C++ [Calorado/Skanda](https://github.com/Calorado/Skanda) compression library. It targets the Skanda v1.0 block format and provides Go-friendly APIs for compressing, decompressing, and reusing encoder/decoder scratch memory.

The implementation focuses on stream compatibility with the C++ project, not byte-for-byte identical compressed output. Streams produced by this package are intended to decode with Skanda v1.0 decoders, and Skanda v1.0 streams produced by the C++ implementation are intended to decode with this package.

## Features

- Pure Go implementation with no cgo dependency.
- Skanda v1.0 block-format encoder and decoder.
- Raw, RLE, and Huffman entropy stream support.
- Standard and advanced distance stream support.
- Literal delta and position-masked literal stream support.
- Slice-based APIs for simple one-shot calls.
- Reusable `Encoder` and `Decoder` types for repeated workloads.
- Compatibility, corpus, fuzz, and performance scripts for local verification.

## Status

The package passes the included Go tests and the available C++ interoperability test when the upstream header is supplied. Current public focused benchmark data is recorded in [verification_report.md](verification_report.md).

The current implementation is format-compatible with the tested Skanda v1.0 matrix, but it is not a claim that all workloads match C++ throughput. Review the verification report and run the benchmark scripts on representative data before replacing an existing production C++ deployment.

## Install

```sh
go get github.com/calorado/skanda-go
```

```go
import skanda "github.com/calorado/skanda-go"
```

## Quick Start

```go
compressed, err := skanda.Compress(input)
if err != nil {
    return err
}

output, err := skanda.Decompress(compressed, len(input))
if err != nil {
    return err
}
```

Use `Decode` when the caller already owns the destination buffer:

```go
output := make([]byte, originalSize)
if err := skanda.Decode(output, compressed); err != nil {
    return err
}
```

Use `Encode` when the caller wants to append compressed data into an existing buffer:

```go
buffer := make([]byte, 0, skanda.CompressBound(len(input)))
buffer, err = skanda.Encode(buffer, input, skanda.WithLevel(6), skanda.WithDecSpeedBias(0.05))
if err != nil {
    return err
}
```

For repeated calls with the same workload shape, `Encoder` and `Decoder` reuse internal scratch memory in addition to caller-owned input and output buffers:

```go
var encoder skanda.Encoder
defer encoder.Close()

buffer := make([]byte, 0, skanda.CompressBound(len(input)))
buffer, err = encoder.Encode(buffer, input, skanda.WithLevel(6), skanda.WithDecSpeedBias(0.05))
if err != nil {
    return err
}

var decoder skanda.Decoder
defer decoder.Close()

output := make([]byte, len(input))
if err := decoder.Decode(output, buffer); err != nil {
    return err
}
```

## API Overview

| API | Purpose |
|---|---|
| `Compress(src, options...)` | Compress into a new byte slice. |
| `Encode(dst, src, options...)` | Append compressed data to `dst`. |
| `Decompress(src, decompressedSize)` | Allocate and decode into a new byte slice. |
| `Decode(dst, src)` | Decode into a caller-owned destination buffer. |
| `Encoder.Encode` | Reusable compression workspace for repeated calls. |
| `Decoder.Decode` | Reusable decompression workspace for repeated calls. |
| `CompressBound(size)` | Conservative output-capacity bound for caller buffers. |
| `WithLevel(level)` | Select Skanda compression level `0..10`; out-of-range values are clamped. |
| `WithDecSpeedBias(value)` | Select decode-speed bias `0..1`; out-of-range values are clamped. |
| `WithProgress(fn)` | Observe compression progress. If the callback returns true before completion, compression stops with `ErrInterrupted`. |

## Format Compatibility

Skanda v1.0 streams do not contain a global magic value, original-size field, checksum, dictionary identifier, or streaming-frame metadata. Callers must know the decompressed size and pass it to `Decompress`, or allocate the exact destination length and call `Decode`.

The Go encoder may make different match-selection or entropy-mode choices from the C++ encoder. Different compressed bytes are valid as long as the decoded output is identical.

See [compatibility.md](compatibility.md) for the compatibility target and supported format features.

## Verification

Run the standard Go test suite:

```sh
go test ./...
```

Run the C++ interoperability test when the upstream `Skanda.h` header is available:

```sh
SKANDA_CPP_HEADER=/path/to/Skanda.h go test -run '^TestCppCompatibilityWhenHeaderAvailable$' ./...
```

Run focused benchmarks with a local corpus file:

```sh
SKANDA_BENCH_CORPUS=/path/to/input go test -run '^$' -bench 'BenchmarkExternal' -benchmem
```

The repository also includes shell scripts under [scripts](scripts) for corpus matrices, focused C++/Go performance comparisons, stream traces, and fuzz smoke runs. Generated evidence and chart inputs live under [verification_artifacts](verification_artifacts).

## Documentation

- [compatibility.md](compatibility.md): Skanda v1.0 compatibility contract.
- [performance_sla.md](performance_sla.md): Benchmark and release-gate metrics.
- [verification_report.md](verification_report.md): Current compatibility and performance evidence.
- [VERIFICATION.md](VERIFICATION.md): Expanded verification checklist for production adoption.
- [CONTRIBUTING.md](CONTRIBUTING.md): Development and contribution workflow.
- [SECURITY.md](SECURITY.md): Security policy and corrupt-stream guidance.

## Security Notes

Skanda v1.0 has no built-in checksum. A corrupted stream can remain structurally valid and decode to different bytes without an error. Applications that require tamper or corruption detection should wrap compressed data in an authenticated container or store an external checksum.

Malformed streams should return Go errors such as `ErrCorrupt`; they should not panic, read out of bounds, allocate unbounded memory, or loop forever.

## Relationship to Calorado/Skanda

This repository is a Go rewrite of [Calorado/Skanda](https://github.com/Calorado/Skanda), the original C++ implementation. The format target, compatibility terminology, and benchmark comparisons refer to that upstream implementation.

## License

`skanda-go` is available under the Apache License, Version 2.0. See [LICENSE](LICENSE).

The original C++ [Calorado/Skanda](https://github.com/Calorado/Skanda) project is licensed under the MIT License. Its copyright and permission notice are retained in [NOTICE](NOTICE).
