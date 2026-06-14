# Security Policy

## Supported Versions

Security fixes are accepted for the current main branch. Tagged releases should document whether a security fix applies to them.

## Reporting a Vulnerability

Please report suspected vulnerabilities through GitHub Security Advisories when available. If advisories are not available, open a minimal public issue that describes the affected API and expected impact without publishing exploit details.

Useful reports include:

- The affected `skanda-go` version or commit.
- The Go version and platform.
- A minimized input stream or reproducer when it can be shared safely.
- Whether the issue is a panic, out-of-bounds behavior, unbounded allocation, hang, data corruption, or compatibility failure.

## Decoder Safety Expectations

Malformed input must not cause a panic, out-of-bounds read or write, unbounded memory allocation, or infinite loop. Structurally invalid streams should return an error such as `ErrCorrupt`.

Skanda v1.0 streams do not include an internal checksum or authentication tag. A mutated stream can remain structurally valid and decode to different bytes without returning an error. Applications that need tamper detection or corruption detection should wrap compressed data in an authenticated container or store an external checksum.
