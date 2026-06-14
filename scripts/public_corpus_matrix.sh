#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cache_dir="${PUBLIC_CORPUS_CACHE:-/tmp/skanda-public-corpus}"
corpora="${PUBLIC_CORPORA:-canterbury}"
refresh="${PUBLIC_CORPUS_REFRESH:-0}"
matrix_output="${PUBLIC_CORPUS_MATRIX_OUTPUT:-}"
size_delta_gate_pct="${PUBLIC_CORPUS_SIZE_DELTA_PCT_GATE:-}"

need_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required tool: $1" >&2
    exit 2
  fi
}

need_tool curl
need_tool python3

mkdir -p "$cache_dir/downloads" "$cache_dir/corpora" "$cache_dir/reports"
cache_dir="$(cd "$cache_dir" && pwd)"
manifest="$cache_dir/reports/public-corpus-manifest.csv"
matrix_stdout="$cache_dir/reports/public-corpus-matrix.csv"
printf "corpus,path,size,sha256\n" > "$manifest"

download() {
  local url="$1"
  local output="$2"
  if [[ "$refresh" == "1" || ! -f "$output" ]]; then
    local tmp="${output}.tmp"
    rm -f "$tmp"
    curl -L --fail --retry 3 --connect-timeout 30 --output "$tmp" "$url"
    mv "$tmp" "$output"
  fi
}

safe_extract_zip() {
  local archive="$1"
  local output_dir="$2"
  if [[ "$refresh" == "1" || ! -d "$output_dir" ]]; then
    rm -rf "$output_dir"
    mkdir -p "$output_dir"
    python3 - "$archive" "$output_dir" <<'PY'
import os
import pathlib
import sys
import zipfile

archive = pathlib.Path(sys.argv[1])
output_dir = pathlib.Path(sys.argv[2]).resolve()

with zipfile.ZipFile(archive) as zf:
    for info in zf.infolist():
        name = info.filename
        if name.endswith("/"):
            continue
        target = (output_dir / name).resolve()
        if output_dir not in target.parents:
            raise SystemExit(f"unsafe zip path: {name}")
        target.parent.mkdir(parents=True, exist_ok=True)
        with zf.open(info) as src, open(target, "wb") as dst:
            dst.write(src.read())
PY
  fi
}

append_manifest() {
  local corpus="$1"
  local path="$2"
  python3 - "$corpus" "$path" "$manifest" <<'PY'
import csv
import hashlib
import pathlib
import sys

corpus, path, manifest = sys.argv[1:]
path_obj = pathlib.Path(path)
digest = hashlib.sha256(path_obj.read_bytes()).hexdigest()
with open(manifest, "a", newline="") as f:
    writer = csv.writer(f)
    writer.writerow([corpus, str(path_obj), path_obj.stat().st_size, digest])
PY
}

verify_canterbury() {
  local dir="$1"
  python3 - "$dir" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
expected = {
    "alice29.txt": 152089,
    "asyoulik.txt": 125179,
    "cp.html": 24603,
    "fields.c": 11150,
    "grammar.lsp": 3721,
    "kennedy.xls": 1029744,
    "lcet10.txt": 426754,
    "plrabn12.txt": 481861,
    "ptt5": 513216,
    "sum": 38240,
    "xargs.1": 4227,
}
for name, size in expected.items():
    matches = list(root.rglob(name))
    if len(matches) != 1:
        raise SystemExit(f"Canterbury file count mismatch for {name}: {len(matches)}")
    actual = matches[0].stat().st_size
    if actual != size:
        raise SystemExit(f"Canterbury size mismatch for {name}: got {actual}, want {size}")
PY
}

verify_silesia() {
  local dir="$1"
  python3 - "$dir" <<'PY'
import hashlib
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
expected = {
    "dickens": ("88334708559f6db57d79096bc0aca07e", 10192446),
    "mozilla": ("c7789a2097f1ff944b0c737430a339b3", 51220480),
    "mr": ("38e623e3093b7bf2003ca4b1bbc19927", 9970564),
    "nci": ("31f85bc8706f3c921104e7c169e2e2e1", 33553445),
    "ooffice": ("573c4ae915e36631d8f2dcffb9b9b66d", 6152192),
    "osdb": ("e734b0c48e6a982adfb5802da3032ecd", 10085684),
    "reymont": ("d8f54d78105079775f32d76dc55fc671", 6627202),
    "samba": ("154eaea7ea70e89f6339ff0abf4112ca", 21606400),
    "sao": ("79e95a22e18cd82b7e42bf91b380d30b", 7251944),
    "webster": ("474931ad907ac27bf962c75ded46c069", 41458703),
    "x-ray": ("9baec32ad14ec3eff487d254382cb91c", 8474240),
    "xml": ("9b09c0c80104adb8aae910b7d7db003e", 5345280),
}
for name, (digest, size) in expected.items():
    matches = list(root.rglob(name))
    if len(matches) != 1:
        raise SystemExit(f"Silesia file count mismatch for {name}: {len(matches)}")
    data = matches[0].read_bytes()
    if len(data) != size:
        raise SystemExit(f"Silesia size mismatch for {name}: got {len(data)}, want {size}")
    actual_digest = hashlib.md5(data).hexdigest()
    if actual_digest != digest:
        raise SystemExit(f"Silesia md5 mismatch for {name}: got {actual_digest}, want {digest}")
PY
}

verify_enwik8() {
  local dir="$1"
  python3 - "$dir" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
matches = list(root.rglob("enwik8"))
if len(matches) != 1:
    raise SystemExit(f"enwik8 file count mismatch: {len(matches)}")
actual = matches[0].stat().st_size
if actual != 100000000:
    raise SystemExit(f"enwik8 size mismatch: got {actual}, want 100000000")
PY
}

collect_files() {
  local corpus="$1"
  local dir="$2"
  while IFS= read -r -d '' file; do
    append_manifest "$corpus" "$file"
    corpus_paths+=("$file")
  done < <(find "$dir" -type f ! -name '*.zip' -print0 | sort -z)
}

corpus_paths=()
for corpus in $corpora; do
  case "$corpus" in
    canterbury)
      archive="$cache_dir/downloads/cantrbry.zip"
      output_dir="$cache_dir/corpora/canterbury"
      download "https://corpus.canterbury.ac.nz/resources/cantrbry.zip" "$archive"
      safe_extract_zip "$archive" "$output_dir"
      verify_canterbury "$output_dir"
      collect_files "$corpus" "$output_dir"
      ;;
    silesia)
      archive="$cache_dir/downloads/silesia.zip"
      output_dir="$cache_dir/corpora/silesia"
      download "https://mattmahoney.net/dc/silesia.zip" "$archive"
      safe_extract_zip "$archive" "$output_dir"
      verify_silesia "$output_dir"
      collect_files "$corpus" "$output_dir"
      ;;
    enwik8)
      archive="$cache_dir/downloads/enwik8.zip"
      output_dir="$cache_dir/corpora/enwik8"
      download "https://mattmahoney.net/dc/enwik8.zip" "$archive"
      safe_extract_zip "$archive" "$output_dir"
      verify_enwik8 "$output_dir"
      collect_files "$corpus" "$output_dir"
      ;;
    *)
      echo "unknown public corpus: $corpus" >&2
      echo "supported: canterbury silesia enwik8" >&2
      exit 2
      ;;
  esac
done

if [[ "${#corpus_paths[@]}" -eq 0 ]]; then
  echo "no public corpus files selected" >&2
  exit 2
fi

if [[ -n "$matrix_output" ]]; then
  matrix_stdout="$matrix_output"
  mkdir -p "$(dirname "$matrix_stdout")"
fi

"$repo_root/scripts/corpus_matrix.sh" "${corpus_paths[@]}" | tee "$matrix_stdout"
python3 - "$matrix_stdout" "$manifest" "$size_delta_gate_pct" <<'PY' >&2
import csv
import pathlib
import sys
from collections import defaultdict

matrix_path = pathlib.Path(sys.argv[1])
manifest_path = pathlib.Path(sys.argv[2])
size_delta_gate_pct = sys.argv[3]

with open(matrix_path, newline="") as f:
    rows = list(csv.DictReader(f))

with open(manifest_path, newline="") as f:
    manifest_rows = list(csv.DictReader(f))

pairs = defaultdict(dict)
for row in rows:
    key = (row["input"], row["input_size"], row["level"], row["bias"])
    pairs[key][row["encoder"]] = int(row["compressed_size"])

max_delta = None
min_delta = None
for key, values in pairs.items():
    if "cpp" not in values or "go" not in values:
        raise SystemExit(f"incomplete matrix pair: {key}")
    cpp_size = values["cpp"]
    go_size = values["go"]
    delta_pct = ((go_size - cpp_size) / cpp_size * 100.0) if cpp_size else 0.0
    item = (delta_pct, key, cpp_size, go_size)
    if max_delta is None or delta_pct > max_delta[0]:
        max_delta = item
    if min_delta is None or delta_pct < min_delta[0]:
        min_delta = item

print(f"manifest_files={len(manifest_rows)}")
print(f"matrix_pairs={len(pairs)}")
if max_delta is not None:
    delta, key, cpp_size, go_size = max_delta
    input_path, input_size, level, bias = key
    print(
        "max_size_delta_pct={:.4f},input={},input_size={},level={},bias={},cpp_size={},go_size={}".format(
            delta, input_path, input_size, level, bias, cpp_size, go_size
        )
    )
    if size_delta_gate_pct:
        threshold = float(size_delta_gate_pct)
        result = "pass" if delta <= threshold else "fail"
        print(f"size_delta_gate={result},threshold_pct={threshold:.4f}")
        if result == "fail":
            raise SystemExit(1)
if min_delta is not None:
    delta, key, cpp_size, go_size = min_delta
    input_path, input_size, level, bias = key
    print(
        "min_size_delta_pct={:.4f},input={},input_size={},level={},bias={},cpp_size={},go_size={}".format(
            delta, input_path, input_size, level, bias, cpp_size, go_size
        )
    )
PY
echo "manifest: $manifest" >&2
echo "matrix: $matrix_stdout" >&2
