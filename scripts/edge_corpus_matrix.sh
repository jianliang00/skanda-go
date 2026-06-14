#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
levels="${LEVELS:-0 1 2 5 10}"
biases="${BIASES:-1.0 0.5 0.05}"
output="${EDGE_CORPUS_MATRIX_OUTPUT:-}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

python3 - "$tmpdir" <<'PY'
import json
import pathlib
import random
import sqlite3
import sys

root = pathlib.Path(sys.argv[1])

def write(name, data):
    path = root / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)

for size in list(range(0, 65)) + [127, 128, 129, 1024, 4095, 4096, 4097]:
    write(f"small/size-{size}.bin", bytes((i * 31 + size) & 255 for i in range(size)))

write("repeated/zero-64k.bin", bytes(64 * 1024))
write("repeated/a-64k.bin", b"A" * (64 * 1024))
write("repeated/periodic-128k.bin", (b"abc123XYZ-" * 14000)[:128 * 1024])

rng = random.Random(12345)
write("random/random-64k.bin", bytes(rng.randrange(256) for _ in range(64 * 1024)))
write("random/random-1m.bin", bytes(rng.randrange(256) for _ in range(1024 * 1024)))

records = []
for i in range(3000):
    records.append({
        "id": i,
        "name": f"record-{i % 97}",
        "enabled": i % 3 == 0,
        "tags": [f"tag-{i % 11}", f"group-{i % 17}"],
        "value": (i * i) % 100000,
    })
write("structured/data.json", json.dumps(records, separators=(",", ":")).encode())
write("structured/page.html", (
    "<html><body>" + "".join(f"<section><h2>{i}</h2><p>skanda html payload {i % 19}</p></section>" for i in range(2000)) + "</body></html>"
).encode())
write("structured/log.txt", b"".join(
    f"2026-06-13T00:{i % 60:02d}:00Z level=info shard={i % 13} msg=processed item={i}\n".encode()
    for i in range(5000)
))
write("structured/table.csv", b"id,name,value\n" + b"".join(
    f"{i},item-{i % 101},{(i * 17) % 100003}\n".encode()
    for i in range(5000)
))

db_path = root / "binary/sample.sqlite"
db_path.parent.mkdir(parents=True, exist_ok=True)
conn = sqlite3.connect(db_path)
conn.execute("create table sample(id integer primary key, name text, value blob)")
for i in range(512):
    conn.execute("insert into sample(name, value) values (?, ?)", (f"row-{i % 31}", bytes([(i + j) & 255 for j in range(64)])))
conn.commit()
conn.close()

write("binary/pattern.bin", bytes((i ^ (i >> 3) ^ (i >> 11)) & 255 for i in range(512 * 1024)))
PY

if [[ -n "$output" ]]; then
  mkdir -p "$(dirname "$output")"
  CORPUS_MATRIX_STRIP_PREFIX="$tmpdir" LEVELS="$levels" BIASES="$biases" "$repo_root/scripts/corpus_matrix.sh" "$tmpdir" | tee "$output"
else
  CORPUS_MATRIX_STRIP_PREFIX="$tmpdir" LEVELS="$levels" BIASES="$biases" "$repo_root/scripts/corpus_matrix.sh" "$tmpdir"
fi
