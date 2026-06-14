# Skanda Go Performance SLA

## Scope

This SLA applies to the pure Go Skanda v1.0 implementation in this module. Measurements must be taken on representative production hardware and on corpora that include small payloads, repeated data, structured text, binary data, high-entropy data, and large files.

## Correctness Gate

Performance results are valid only after these correctness gates pass:

- Go round-trip tests pass.
- C++ compressed streams decode correctly in Go.
- Go compressed streams decode correctly in C++.
- Corrupt-input fuzzing does not panic, run out of memory, or hang.
- Race tests pass.

## Minimum Metrics

Each benchmark report must record:

```text
input_name
input_size
compressed_size_cpp
compressed_size_go
decompress_artifact_origin
decompress_artifact_size
decompress_artifact_sha256
ratio_cpp
ratio_go
compress_MBps_cpp
compress_MBps_go
decompress_MBps_cpp
decompress_MBps_go
compress_speed_delta_pct
compress_reuse_speed_delta_pct
decompress_speed_delta_pct
decode_reuse_speed_delta_pct
allocs_per_op_go
bytes_per_op_go
```

The speed delta fields use `(Go throughput - C++ throughput) / C++ throughput`. Reports may retain Go/C++ speed ratio fields for automated threshold checks, but reader-facing comparisons and charts should use signed deltas so slower and faster cases are visually distinct around the C++ parity line.

## Initial Thresholds

| Metric | Threshold |
|---|---|
| Go round-trip correctness | 100% pass. |
| C++ -> Go compatibility | 100% pass for the supported matrix. |
| Go -> C++ compatibility | 100% pass for the supported matrix. |
| Decoder safety fuzz | No panic, out-of-memory failure, or hang in smoke fuzz. |
| Race test | No race detected. |
| Compression ratio | Must be reported against C++ for every corpus and level. |
| Compression throughput | Must be reported against C++ for every corpus and level. |
| Decompression throughput | Must be reported against C++ for every corpus and level. |

## Release Gate

A release candidate is production-ready only when the compatibility matrix passes and the benchmark report shows acceptable compression ratio, compression throughput, decompression throughput, and allocation behavior for the target workload.

If the Skanda stream is used without an external checksum, the integration must document that structurally valid corrupted streams may decode without an error.
