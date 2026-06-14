#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
upstream_header="${SKANDA_CPP_HEADER:-/tmp/skanda-upstream/Skanda.h}"
reference_commit="${SKANDA_CPP_COMMIT-650b34b17a25b89024b7d19820c17c95a9d7591c}"
cpp_input_guard="${SKANDA_CPP_INPUT_GUARD:-0}"

if [[ "$#" -lt 3 || "$#" -gt 4 ]]; then
  echo "usage: $0 <input-file> <level> <bias> [output-dir]" >&2
  echo "env: SKANDA_CPP_HEADER=/path/to/Skanda.h SKANDA_CPP_COMMIT=<sha-or-empty> SKANDA_CPP_INPUT_GUARD=0|1" >&2
  exit 2
fi

input="$1"
level="$2"
bias="$3"
output_dir="${4:-}"

if [[ ! -f "$input" ]]; then
  echo "missing input file: $input" >&2
  exit 2
fi
if [[ ! -f "$upstream_header" ]]; then
  echo "missing upstream header: $upstream_header" >&2
  exit 2
fi
if ! [[ "$cpp_input_guard" =~ ^[0-9]+$ ]]; then
  echo "SKANDA_CPP_INPUT_GUARD must be a non-negative integer" >&2
  exit 2
fi

if [[ -n "$reference_commit" ]]; then
  header_dir="$(cd "$(dirname "$upstream_header")" && pwd)"
  if git -C "$header_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    actual_commit="$(git -C "$header_dir" rev-parse HEAD)"
    if [[ "$actual_commit" != "$reference_commit" ]]; then
      echo "upstream header commit mismatch: got $actual_commit, want $reference_commit" >&2
      echo "set SKANDA_CPP_COMMIT= to the intended reference commit, or set it to an empty value for an explicitly unpinned run" >&2
      exit 2
    fi
  else
    echo "upstream header is not inside a git checkout: $upstream_header" >&2
    echo "set SKANDA_CPP_COMMIT= to an empty value for an explicitly unpinned run" >&2
    exit 2
  fi
fi

tmpdir="$(mktemp -d)"
if [[ -z "$output_dir" ]]; then
  trap 'rm -rf "$tmpdir"' EXIT
  output_dir="$tmpdir/out"
else
  trap 'rm -rf "$tmpdir"' EXIT
fi
mkdir -p "$output_dir"

cat > "$tmpdir/skanda_trace_cpp.cpp" <<CPP
#include <algorithm>
#include <cstdint>
#include <cstdlib>
#include <fstream>
#include <iostream>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

#define SKANDA_IMPLEMENTATION
#include "$upstream_header"

static constexpr size_t kInputGuardBytes = $cpp_input_guard;

static std::vector<uint8_t> read_file(const char* path) {
    std::ifstream in(path, std::ios::binary);
    if (!in) throw std::runtime_error("open input failed");
    return std::vector<uint8_t>((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
}

struct InputBytes {
    std::vector<uint8_t> storage;
    size_t logical_size;

    const uint8_t* data() const {
        return storage.data() + kInputGuardBytes;
    }

    size_t size() const {
        return logical_size;
    }
};

static InputBytes read_input_file(const char* path) {
    auto raw = read_file(path);
    size_t logical_size = raw.size();
    if (kInputGuardBytes == 0) return InputBytes{std::move(raw), logical_size};
    std::vector<uint8_t> guarded(kInputGuardBytes + logical_size);
    std::copy(raw.begin(), raw.end(), guarded.begin() + kInputGuardBytes);
    return InputBytes{std::move(guarded), logical_size};
}

static void write_file(const char* path, const std::vector<uint8_t>& data) {
    std::ofstream out(path, std::ios::binary);
    if (!out) throw std::runtime_error("open output failed");
    out.write(reinterpret_cast<const char*>(data.data()), data.size());
}

int main(int argc, char** argv) {
    try {
        if (argc != 5) return 2;
        auto input = read_input_file(argv[1]);
        int level = std::atoi(argv[3]);
        float bias = std::strtof(argv[4], nullptr);
        std::vector<uint8_t> compressed(skanda::compress_bound(input.size()));
        size_t n = skanda::compress(input.data(), input.size(), compressed.data(), level, bias);
        if (skanda::is_error(n)) return 1;
        compressed.resize(n);
        write_file(argv[2], compressed);
        return 0;
    } catch (const std::exception& e) {
        std::cerr << e.what() << "\\n";
        return 1;
    }
}
CPP

cat > "$tmpdir/skanda_trace_go.go" <<'GO'
package main

import (
	"os"
	"strconv"

	skanda "github.com/calorado/skanda-go"
)

func main() {
	if len(os.Args) != 5 {
		os.Exit(2)
	}
	input, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	level, err := strconv.Atoi(os.Args[3])
	if err != nil {
		panic(err)
	}
	bias, err := strconv.ParseFloat(os.Args[4], 64)
	if err != nil {
		panic(err)
	}
	compressed, err := skanda.Compress(input, skanda.WithLevel(level), skanda.WithDecSpeedBias(bias))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[2], compressed, 0o644); err != nil {
		panic(err)
	}
}
GO

g++ -std=c++17 -O2 "$tmpdir/skanda_trace_cpp.cpp" -o "$tmpdir/skanda_trace_cpp"
(cd "$repo_root" && go build -o "$tmpdir/skanda_trace_go" "$tmpdir/skanda_trace_go.go")

cpp_stream="$output_dir/cpp-level${level}-bias${bias}.skanda"
go_stream="$output_dir/go-level${level}-bias${bias}.skanda"
trace_csv="$output_dir/stream-trace-level${level}-bias${bias}.csv"
input_size="$(wc -c < "$input" | tr -d '[:space:]')"

"$tmpdir/skanda_trace_cpp" "$input" "$cpp_stream" "$level" "$bias"
"$tmpdir/skanda_trace_go" "$input" "$go_stream" "$level" "$bias"

trace_one() {
  local label="$1"
  local stream="$2"
  (cd "$repo_root" && go test -tags skandatrace -run '^TestTraceCompressedStream$' -count=1 -v \
    -args -skanda_trace_input="$stream" -skanda_trace_decoded_size="$input_size" -skanda_trace_label="$label") \
    | awk '/^(label|cpp|go),/'
}

{
  trace_one cpp "$cpp_stream"
  trace_one go "$go_stream" | awk 'NR > 1'
} > "$trace_csv"

printf "cpp_stream=%s\n" "$cpp_stream" >&2
printf "go_stream=%s\n" "$go_stream" >&2
printf "trace_csv=%s\n" "$trace_csv" >&2
cat "$trace_csv"
