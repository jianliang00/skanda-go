#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

python3 - "$tmpdir/mixed.bin" <<'PY'
import sys

phrases = [
    b"alpha beta gamma delta ",
    b"The quick brown fox jumps over the lazy dog. ",
    bytes(range(10)),
]
out = bytearray()
i = 0
while len(out) < 192 * 1024:
    out.extend(phrases[i % len(phrases)])
    if i % 7 == 0:
        base = len(out) - min(len(out), 4096)
        out.extend(out[base:base + min(257, len(out) - base)])
    if i % 11 == 0:
        for j in range(53):
            out.append((i * j + j * j) & 255)
    i += 1

open(sys.argv[1], "wb").write(out)
PY

CORPUS_MATRIX_FORMAT=compact "$repo_root/scripts/corpus_matrix.sh" "$tmpdir/mixed.bin"
