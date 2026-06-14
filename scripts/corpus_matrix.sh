#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
upstream_header="${SKANDA_CPP_HEADER:-/tmp/skanda-upstream/Skanda.h}"
reference_commit="${SKANDA_CPP_COMMIT-650b34b17a25b89024b7d19820c17c95a9d7591c}"
levels="${LEVELS:-0 1 2 3 4 5 6 7 8 9 10}"
biases="${BIASES:-1.0 0.5 0.05}"
format="${CORPUS_MATRIX_FORMAT:-detailed}"
cpp_input_guard="${SKANDA_CPP_INPUT_GUARD:-0}"
strip_prefix="${CORPUS_MATRIX_STRIP_PREFIX:-}"

if [[ "$#" -lt 1 ]]; then
  echo "usage: $0 <file-or-directory>..." >&2
  echo "env: SKANDA_CPP_HEADER=/path/to/Skanda.h SKANDA_CPP_COMMIT=<sha-or-empty> LEVELS='0 1' BIASES='1.0 0.5' CORPUS_MATRIX_FORMAT=detailed|compact CORPUS_MATRIX_STRIP_PREFIX=/path SKANDA_CPP_INPUT_GUARD=0|1" >&2
  exit 2
fi

if ! [[ "$cpp_input_guard" =~ ^[0-9]+$ ]]; then
  echo "SKANDA_CPP_INPUT_GUARD must be a non-negative integer" >&2
  exit 2
fi

if [[ ! -f "$upstream_header" ]]; then
  echo "missing upstream header: $upstream_header" >&2
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

files=()
for path in "$@"; do
  if [[ -d "$path" ]]; then
    while IFS= read -r -d '' file; do
      files+=("$file")
    done < <(find "$path" -type f -print0)
  elif [[ -f "$path" ]]; then
    files+=("$path")
  else
    echo "missing corpus path: $path" >&2
    exit 2
  fi
done

if [[ "${#files[@]}" -eq 0 ]]; then
  echo "no corpus files found" >&2
  exit 2
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cat > "$tmpdir/skanda_corpus_matrix.cpp" <<CPP
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
        if (argc < 6) return 2;
        std::string mode = argv[1];
        if (mode == "compress") {
            auto input = read_input_file(argv[2]);
            int level = std::atoi(argv[4]);
            float bias = std::strtof(argv[5], nullptr);
            std::vector<uint8_t> compressed(skanda::compress_bound(input.size()));
            size_t n = skanda::compress(input.data(), input.size(), compressed.data(), level, bias);
            if (skanda::is_error(n)) return 1;
            compressed.resize(n);
            write_file(argv[3], compressed);
            std::cout << n << "\\n";
            return 0;
        }
        if (mode == "decode") {
            if (argc < 7) return 2;
            auto compressed = read_file(argv[2]);
            size_t original_size = static_cast<size_t>(std::strtoull(argv[6], nullptr, 10));
            std::vector<uint8_t> output(original_size);
            size_t err = skanda::decompress(compressed.data(), compressed.size(), output.data(), output.size());
            if (skanda::is_error(err)) return 1;
            write_file(argv[3], output);
            return 0;
        }
        return 2;
    } catch (const std::exception& e) {
        std::cerr << e.what() << "\\n";
        return 1;
    }
}
CPP

cat > "$tmpdir/go_corpus_matrix.go" <<'GO'
package main

import (
	"fmt"
	"os"
	"strconv"

	skanda "github.com/calorado/skanda-go"
)

func main() {
	if len(os.Args) < 6 {
		os.Exit(2)
	}
	mode := os.Args[1]
	inputPath := os.Args[2]
	outputPath := os.Args[3]
	level, err := strconv.Atoi(os.Args[4])
	if err != nil {
		panic(err)
	}
	bias, err := strconv.ParseFloat(os.Args[5], 64)
	if err != nil {
		panic(err)
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		panic(err)
	}
	switch mode {
	case "compress":
		compressed, err := skanda.Compress(input, skanda.WithLevel(level), skanda.WithDecSpeedBias(bias))
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(outputPath, compressed, 0o644); err != nil {
			panic(err)
		}
		fmt.Fprintln(os.Stdout, len(compressed))
	case "decode":
		if len(os.Args) < 7 {
			os.Exit(2)
		}
		originalSize, err := strconv.Atoi(os.Args[6])
		if err != nil {
			panic(err)
		}
		decoded, err := skanda.Decompress(input, originalSize)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(outputPath, decoded, 0o644); err != nil {
			panic(err)
		}
	default:
		os.Exit(2)
	}
}
GO

g++ -std=c++17 -O2 "$tmpdir/skanda_corpus_matrix.cpp" -o "$tmpdir/skanda_corpus_matrix"
(cd "$repo_root" && go build -o "$tmpdir/go_corpus_matrix" "$tmpdir/go_corpus_matrix.go")

csv_field() {
  local value="${1//\"/\"\"}"
  printf '"%s"' "$value"
}

emit_row() {
  local input="$1"
  local input_size="$2"
  local encoder="$3"
  local level="$4"
  local bias="$5"
  local compressed_size="$6"
  if [[ "$format" == "compact" ]]; then
    printf "%s,%s,%s,%s\n" "$encoder" "$level" "$bias" "$compressed_size"
  else
    csv_field "$input"
    printf ",%s,%s,%s,%s,%s\n" "$input_size" "$encoder" "$level" "$bias" "$compressed_size"
  fi
}

run_checked() {
  local description="$1"
  shift
  if ! "$@"; then
    echo "failed: $description" >&2
    return 1
  fi
}

if [[ "$format" == "compact" ]]; then
  printf "encoder,level,bias,size\n"
elif [[ "$format" == "detailed" ]]; then
  printf "input,input_size,encoder,level,bias,compressed_size\n"
else
  echo "unknown CORPUS_MATRIX_FORMAT: $format" >&2
  exit 2
fi

index=0
for input in "${files[@]}"; do
  input_size="$(wc -c < "$input" | tr -d '[:space:]')"
  display_input="$input"
  if [[ -n "$strip_prefix" ]]; then
    prefix="$strip_prefix"
    if [[ "${prefix: -1}" != "/" ]]; then
      prefix="$prefix/"
    fi
    if [[ "$display_input" == "$prefix"* ]]; then
      display_input="${display_input#"$prefix"}"
    fi
  fi
  for level in $levels; do
    for bias in $biases; do
      cpp_out="$tmpdir/cpp-${index}-${level}-${bias}.skanda"
      go_out="$tmpdir/go-${index}-${level}-${bias}.skanda"
      cpp_decoded="$tmpdir/cpp-decoded-${index}-${level}-${bias}.bin"
      go_decoded="$tmpdir/go-decoded-${index}-${level}-${bias}.bin"

      if ! cpp_size="$("$tmpdir/skanda_corpus_matrix" compress "$input" "$cpp_out" "$level" "$bias")"; then
        echo "failed: C++ compress input=$input level=$level bias=$bias" >&2
        exit 1
      fi
      if ! go_size="$("$tmpdir/go_corpus_matrix" compress "$input" "$go_out" "$level" "$bias")"; then
        echo "failed: Go compress input=$input level=$level bias=$bias" >&2
        exit 1
      fi

      run_checked "Go decode C++ stream input=$input level=$level bias=$bias" "$tmpdir/go_corpus_matrix" decode "$cpp_out" "$go_decoded" "$level" "$bias" "$input_size"
      if ! cmp -s "$go_decoded" "$input"; then
        echo "Go decoder mismatch: input=$input level=$level bias=$bias" >&2
        exit 1
      fi

      run_checked "C++ decode Go stream input=$input level=$level bias=$bias" "$tmpdir/skanda_corpus_matrix" decode "$go_out" "$cpp_decoded" "$level" "$bias" "$input_size"
      if ! cmp -s "$cpp_decoded" "$input"; then
        echo "C++ decoder mismatch: input=$input level=$level bias=$bias" >&2
        exit 1
      fi

      emit_row "$display_input" "$input_size" cpp "$level" "$bias" "$cpp_size"
      emit_row "$display_input" "$input_size" go "$level" "$bias" "$go_size"
    done
  done
  index=$((index + 1))
done
