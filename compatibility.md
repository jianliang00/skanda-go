# Skanda Go Compatibility Baseline

## Compatibility Target

This package targets the Skanda v1.0 block format implemented by `Calorado/Skanda`.

The compressed stream starts directly with Skanda block headers. The format does not contain a global magic value, format version field, original-size field, checksum, dictionary identifier, or streaming frame metadata. Callers must provide the decompressed size when decoding.

## Required Interoperability

The Go implementation must satisfy both directions of interoperability:

- C++ compressed stream -> Go decoder -> original bytes.
- Go compressed stream -> C++ decoder -> original bytes.

Compressed output is not required to be byte-for-byte identical to the C++ encoder output. Skanda permits multiple valid encodings for the same input because match selection, block splitting, entropy mode selection, and Huffman code generation may vary while preserving the same decoded bytes.

## Supported Format Features

| Feature | Requirement |
|---|---|
| Raw blocks | Must decode and encode. |
| Compressed LZ blocks | Must decode and encode. |
| Raw entropy streams | Must decode and encode. |
| RLE entropy streams | Must decode and encode. |
| Huffman entropy streams | Must decode and encode. |
| Standard distance streams | Must decode and encode. |
| Advanced distance streams | Must decode C++ streams. Go encoding may choose standard or advanced distances per block. |
| Literal delta streams | Must decode C++ streams. Go encoding may choose raw or delta literals per block. |
| Position-masked literal streams | Must decode C++ streams. Go encoding may choose one or four literal streams per block. |
| Final 31 raw bytes | Must follow Skanda v1.0 block semantics. |

## Compression Parameters

| Parameter | C++ behavior | Go requirement |
|---|---|---|
| Level | C++ clamps to `0..10` and selects one of 11 parser configurations. | Go must accept `0..10`, clamp out-of-range values, and keep output format-compatible at every level. |
| DecSpeedBias | C++ clamps to `0..1` and uses thresholds for Huffman, advanced distance, and literal coding. | Go must accept `0..1`, clamp out-of-range values, and produce streams decodable by Skanda v1.0 decoders. |
| Original size | Not stored in stream. | Decoder API requires caller-provided decompressed size. |
| Dictionary | Not present in Skanda v1.0 public API. | Not supported. |
| Checksum | Not present in Skanda v1.0 public format. | Not supported. Corrupt streams may be detected structurally, but silent corruption is possible without an external checksum. |
| Streaming API | Not present in Skanda v1.0 public API. | Not supported by the baseline API. |

## Error Semantics

The Go decoder must never panic, read out of bounds, allocate unbounded memory, or loop forever when given malformed input. It returns `ErrCorrupt` for structurally invalid streams.

Because Skanda v1.0 has no checksum, a mutated compressed stream can remain structurally valid and decode to different bytes without an error. Applications that require corruption detection must wrap Skanda data with an external checksum or authenticated container.

## Compatibility Matrix

The required compatibility matrix covers:

- C++ levels `0..10`.
- `decSpeedBias` values `1.0`, `0.5`, and `0.05`.
- Repeated, mixed structured, random, small, and block-boundary corpora.

Additional production corpora can extend this matrix without changing the compatibility target.
