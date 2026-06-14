#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
upstream_header="${SKANDA_CPP_HEADER:-/tmp/skanda-upstream/Skanda.h}"
reference_commit="${SKANDA_CPP_COMMIT-650b34b17a25b89024b7d19820c17c95a9d7591c}"
levels="${LEVELS:-0 1 2 3 4 5 6 7 8 9 10}"
biases="${BIASES:-1.0 0.5 0.05}"
iterations="${PERF_ITERATIONS:-5}"
cxx_flags="${CXXFLAGS:--O2}"
size_gate_pct="${SIZE_DELTA_PCT_GATE:-}"
speed_gate_pct="${SPEED_RATIO_PCT_GATE:-}"
run_id="${PERF_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
host="$(hostname)"
os_info="$(uname -srm)"
go_version="$(go version)"
cxx_version="$(g++ --version | head -n 1)"
cpp_commit="$reference_commit"

if [[ "$#" -lt 1 ]]; then
  echo "usage: $0 <file-or-directory>..." >&2
  echo "env: SKANDA_CPP_HEADER=/path/to/Skanda.h SKANDA_CPP_COMMIT=<sha-or-empty> LEVELS='0 1' BIASES='1.0 0.5' PERF_ITERATIONS=<n>" >&2
  exit 2
fi

if [[ ! -f "$upstream_header" ]]; then
  echo "missing upstream header: $upstream_header" >&2
  exit 2
fi

if [[ "$iterations" -lt 1 ]]; then
  echo "PERF_ITERATIONS must be >= 1" >&2
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

cat > "$tmpdir/skanda_perf_cpp.cpp" <<CPP
#include <chrono>
#include <cstdint>
#include <cstdlib>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

#define SKANDA_IMPLEMENTATION
#include "$upstream_header"

static std::vector<uint8_t> read_file(const char* path) {
    std::ifstream in(path, std::ios::binary);
    if (!in) throw std::runtime_error("open input failed");
    return std::vector<uint8_t>((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
}

static void write_file(const char* path, const std::vector<uint8_t>& data) {
    std::ofstream out(path, std::ios::binary);
    if (!out) throw std::runtime_error("open output failed");
    out.write(reinterpret_cast<const char*>(data.data()), data.size());
}

static double mbps(size_t bytes, double seconds, int iterations) {
    if (seconds <= 0) return 0;
    return (static_cast<double>(bytes) * static_cast<double>(iterations)) / seconds / 1000000.0;
}

static std::vector<uint8_t> compress_bytes(const std::vector<uint8_t>& input, int level, float bias) {
    std::vector<uint8_t> compressed(skanda::compress_bound(input.size()));
    size_t compressed_size = skanda::compress(input.data(), input.size(), compressed.data(), level, bias);
    if (skanda::is_error(compressed_size)) throw std::runtime_error("compress failed");
    compressed.resize(compressed_size);
    return compressed;
}

static void verify_decode(const std::vector<uint8_t>& compressed, const std::vector<uint8_t>& expected) {
    std::vector<uint8_t> output(expected.size());
    size_t err = skanda::decompress(compressed.data(), compressed.size(), output.data(), output.size());
    if (skanda::is_error(err) || output != expected) throw std::runtime_error("decode mismatch");
}

int main(int argc, char** argv) {
    try {
        if (argc < 2) return 2;
        std::string mode = argv[1];
        if (mode == "compress-file") {
            if (argc != 6) return 2;
            auto input = read_file(argv[2]);
            int level = std::atoi(argv[4]);
            float bias = std::strtof(argv[5], nullptr);
            auto compressed = compress_bytes(input, level, bias);
            verify_decode(compressed, input);
            write_file(argv[3], compressed);
            std::cout << compressed.size() << "\\n";
            return 0;
        }
        if (mode == "bench-compress") {
            if (argc != 7) return 2;
            auto input = read_file(argv[2]);
            int level = std::atoi(argv[3]);
            float bias = std::strtof(argv[4], nullptr);
            int iterations = std::atoi(argv[5]);
            size_t decompressed_size = static_cast<size_t>(std::strtoull(argv[6], nullptr, 10));
            if (iterations < 1 || decompressed_size != input.size()) return 2;

            auto compress_start = std::chrono::steady_clock::now();
            size_t last_size = 0;
            for (int i = 0; i < iterations; ++i) {
                auto compressed = compress_bytes(input, level, bias);
                last_size = compressed.size();
            }
            auto compress_end = std::chrono::steady_clock::now();
            double compress_seconds = std::chrono::duration<double>(compress_end - compress_start).count();
            std::cout << last_size << ","
                      << std::fixed << std::setprecision(2)
                      << mbps(input.size(), compress_seconds, iterations) << "\\n";
            return 0;
        }
        if (mode == "bench-compress-reuse") {
            if (argc != 7) return 2;
            auto input = read_file(argv[2]);
            int level = std::atoi(argv[3]);
            float bias = std::strtof(argv[4], nullptr);
            int iterations = std::atoi(argv[5]);
            size_t decompressed_size = static_cast<size_t>(std::strtoull(argv[6], nullptr, 10));
            if (iterations < 1 || decompressed_size != input.size()) return 2;

            std::vector<uint8_t> compressed(skanda::compress_bound(input.size()));
            auto compress_start = std::chrono::steady_clock::now();
            size_t last_size = 0;
            for (int i = 0; i < iterations; ++i) {
                last_size = skanda::compress(input.data(), input.size(), compressed.data(), level, bias);
                if (skanda::is_error(last_size)) throw std::runtime_error("compress failed");
            }
            auto compress_end = std::chrono::steady_clock::now();
            compressed.resize(last_size);
            verify_decode(compressed, input);
            double compress_seconds = std::chrono::duration<double>(compress_end - compress_start).count();
            std::cout << last_size << ","
                      << std::fixed << std::setprecision(2)
                      << mbps(input.size(), compress_seconds, iterations) << "\\n";
            return 0;
        }
        if (mode == "bench-decompress") {
            if (argc != 6) return 2;
            auto expected = read_file(argv[2]);
            auto compressed = read_file(argv[3]);
            int iterations = std::atoi(argv[4]);
            size_t decompressed_size = static_cast<size_t>(std::strtoull(argv[5], nullptr, 10));
            if (iterations < 1 || decompressed_size != expected.size()) return 2;

            verify_decode(compressed, expected);
            std::vector<uint8_t> last_output;
            auto decompress_start = std::chrono::steady_clock::now();
            for (int i = 0; i < iterations; ++i) {
                std::vector<uint8_t> output(expected.size());
                size_t err = skanda::decompress(compressed.data(), compressed.size(), output.data(), output.size());
                if (skanda::is_error(err)) throw std::runtime_error("decode failed");
                last_output.swap(output);
            }
            auto decompress_end = std::chrono::steady_clock::now();
            if (last_output != expected) throw std::runtime_error("decode mismatch");
            double decompress_seconds = std::chrono::duration<double>(decompress_end - decompress_start).count();
            std::cout << std::fixed << std::setprecision(2)
                      << mbps(expected.size(), decompress_seconds, iterations) << "\\n";
            return 0;
        }
        if (mode == "bench-decode-reuse") {
            if (argc != 6) return 2;
            auto expected = read_file(argv[2]);
            auto compressed = read_file(argv[3]);
            int iterations = std::atoi(argv[4]);
            size_t decompressed_size = static_cast<size_t>(std::strtoull(argv[5], nullptr, 10));
            if (iterations < 1 || decompressed_size != expected.size()) return 2;

            verify_decode(compressed, expected);
            std::vector<uint8_t> output(expected.size());
            auto decompress_start = std::chrono::steady_clock::now();
            for (int i = 0; i < iterations; ++i) {
                size_t err = skanda::decompress(compressed.data(), compressed.size(), output.data(), output.size());
                if (skanda::is_error(err)) throw std::runtime_error("decode failed");
            }
            auto decompress_end = std::chrono::steady_clock::now();
            if (output != expected) throw std::runtime_error("decode mismatch");
            double decompress_seconds = std::chrono::duration<double>(decompress_end - decompress_start).count();
            std::cout << std::fixed << std::setprecision(2)
                      << mbps(expected.size(), decompress_seconds, iterations) << "\\n";
            return 0;
        }
        return 2;
    } catch (const std::exception& e) {
        std::cerr << e.what() << "\\n";
        return 1;
    }
}
CPP

cat > "$tmpdir/skanda_perf_go.go" <<'GO'
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"

	skanda "github.com/calorado/skanda-go"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "compress-file":
		if len(os.Args) != 6 {
			os.Exit(2)
		}
		input := readFile(os.Args[2])
		level := parseInt(os.Args[4])
		bias := parseFloat(os.Args[5])
		compressed := compress(input, level, bias)
		verifyDecode(compressed, input)
		if err := os.WriteFile(os.Args[3], compressed, 0o644); err != nil {
			panic(err)
		}
		fmt.Println(len(compressed))
	case "bench-compress":
		if len(os.Args) != 7 {
			os.Exit(2)
		}
		input := readFile(os.Args[2])
		level := parseInt(os.Args[3])
		bias := parseFloat(os.Args[4])
		iterations := parseInt(os.Args[5])
		decompressedSize := parseInt(os.Args[6])
		if iterations < 1 || len(input) != decompressedSize {
			os.Exit(2)
		}
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		lastSize := 0
		for i := 0; i < iterations; i++ {
			compressed := compress(input, level, bias)
			lastSize = len(compressed)
		}
		elapsed := time.Since(start).Seconds()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		bytesPerOp := uint64(0)
		if iterations > 0 {
			bytesPerOp = (after.TotalAlloc - before.TotalAlloc) / uint64(iterations)
		}
		fmt.Printf("%d,%.2f,%d\n", lastSize, mbps(len(input), elapsed, iterations), bytesPerOp)
	case "bench-compress-reuse":
		if len(os.Args) != 7 {
			os.Exit(2)
		}
		input := readFile(os.Args[2])
		level := parseInt(os.Args[3])
		bias := parseFloat(os.Args[4])
		iterations := parseInt(os.Args[5])
		decompressedSize := parseInt(os.Args[6])
		if iterations < 1 || len(input) != decompressedSize {
			os.Exit(2)
		}
		dst := make([]byte, 0, skanda.CompressBound(len(input)))
		var encoder skanda.Encoder
		defer encoder.Close()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		lastSize := 0
		for i := 0; i < iterations; i++ {
			var err error
			dst = dst[:0]
			dst, err = encoder.Encode(dst, input, skanda.WithLevel(level), skanda.WithDecSpeedBias(bias))
			if err != nil {
				panic(err)
			}
			lastSize = len(dst)
		}
		elapsed := time.Since(start).Seconds()
		verifyDecode(dst, input)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		bytesPerOp := uint64(0)
		if iterations > 0 {
			bytesPerOp = (after.TotalAlloc - before.TotalAlloc) / uint64(iterations)
		}
		fmt.Printf("%d,%.2f,%d\n", lastSize, mbps(len(input), elapsed, iterations), bytesPerOp)
	case "bench-decompress":
		if len(os.Args) != 6 {
			os.Exit(2)
		}
		expected := readFile(os.Args[2])
		compressed := readFile(os.Args[3])
		iterations := parseInt(os.Args[4])
		decompressedSize := parseInt(os.Args[5])
		if iterations < 1 || len(expected) != decompressedSize {
			os.Exit(2)
		}
		verifyDecode(compressed, expected)
		var last []byte
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		for i := 0; i < iterations; i++ {
			decoded, err := skanda.Decompress(compressed, len(expected))
			if err != nil {
				panic(err)
			}
			last = decoded
		}
		elapsed := time.Since(start).Seconds()
		if !bytes.Equal(last, expected) {
			panic("decoded bytes differ")
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		bytesPerOp := uint64(0)
		if iterations > 0 {
			bytesPerOp = (after.TotalAlloc - before.TotalAlloc) / uint64(iterations)
		}
		fmt.Printf("%.2f,%d\n", mbps(len(expected), elapsed, iterations), bytesPerOp)
	case "bench-decode-reuse":
		if len(os.Args) != 6 {
			os.Exit(2)
		}
		expected := readFile(os.Args[2])
		compressed := readFile(os.Args[3])
		iterations := parseInt(os.Args[4])
		decompressedSize := parseInt(os.Args[5])
		if iterations < 1 || len(expected) != decompressedSize {
			os.Exit(2)
		}
		verifyDecode(compressed, expected)
		dst := make([]byte, len(expected))
		var decoder skanda.Decoder
		defer decoder.Close()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		for i := 0; i < iterations; i++ {
			if err := decoder.Decode(dst, compressed); err != nil {
				panic(err)
			}
		}
		elapsed := time.Since(start).Seconds()
		if !bytes.Equal(dst, expected) {
			panic("decoded bytes differ")
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		bytesPerOp := uint64(0)
		if iterations > 0 {
			bytesPerOp = (after.TotalAlloc - before.TotalAlloc) / uint64(iterations)
		}
		fmt.Printf("%.2f,%d\n", mbps(len(expected), elapsed, iterations), bytesPerOp)
	default:
		os.Exit(2)
	}
}

func readFile(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

func parseInt(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil {
		panic(err)
	}
	return n
}

func parseFloat(value string) float64 {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		panic(err)
	}
	return n
}

func compress(input []byte, level int, bias float64) []byte {
	compressed, err := skanda.Compress(input, skanda.WithLevel(level), skanda.WithDecSpeedBias(bias))
	if err != nil {
		panic(err)
	}
	return compressed
}

func verifyDecode(compressed, expected []byte) {
	decoded, err := skanda.Decompress(compressed, len(expected))
	if err != nil {
		panic(err)
	}
	if !bytes.Equal(decoded, expected) {
		panic("decoded bytes differ")
	}
}

func mbps(bytes int, seconds float64, iterations int) float64 {
	if seconds <= 0 {
		return 0
	}
	return float64(bytes*iterations) / seconds / 1000000
}
GO

g++ -std=c++17 $cxx_flags "$tmpdir/skanda_perf_cpp.cpp" -o "$tmpdir/skanda_perf_cpp"
(cd "$repo_root" && go build -o "$tmpdir/skanda_perf_go" "$tmpdir/skanda_perf_go.go")

printf "run_id,timestamp,host,os,go_version,cxx_version,cxx_flags,cpp_commit,input,input_sha256,input_size,level,bias,iterations,compressed_size_cpp,compressed_size_go,ratio_cpp,ratio_go,size_delta_bytes,size_delta_pct,size_gate,compress_mbps_cpp,compress_mbps_go,compress_speed_pct,compress_gate,decompress_artifact_origin,decompress_artifact_size,decompress_artifact_sha256,decompress_mbps_cpp,decompress_mbps_go,decompress_speed_pct,decompress_gate,go_compress_bytes_per_op,go_decompress_bytes_per_op,decode_reuse_mbps_cpp,decode_reuse_mbps_go,decode_reuse_speed_pct,go_decode_reuse_bytes_per_op,compress_reuse_mbps_cpp,compress_reuse_mbps_go,compress_reuse_speed_pct,go_compress_reuse_bytes_per_op,compress_speed_delta_pct,decompress_speed_delta_pct,decode_reuse_speed_delta_pct,compress_reuse_speed_delta_pct\n"

index=0
for input in "${files[@]}"; do
  input_size="$(wc -c < "$input" | tr -d '[:space:]')"
  input_sha256="$(shasum -a 256 "$input" | awk '{print $1}')"
  for level in $levels; do
    for bias in $biases; do
      cpp_artifact="$tmpdir/cpp-${index}-${level}-${bias}.skanda"
      go_artifact="$tmpdir/go-${index}-${level}-${bias}.skanda"
      cpp_size="$("$tmpdir/skanda_perf_cpp" compress-file "$input" "$cpp_artifact" "$level" "$bias")"
      go_size="$("$tmpdir/skanda_perf_go" compress-file "$input" "$go_artifact" "$level" "$bias")"
      cpp_artifact_sha256="$(shasum -a 256 "$cpp_artifact" | awk '{print $1}')"
      go_artifact_sha256="$(shasum -a 256 "$go_artifact" | awk '{print $1}')"
      cpp_compress_metrics="$("$tmpdir/skanda_perf_cpp" bench-compress "$input" "$level" "$bias" "$iterations" "$input_size")"
      go_compress_metrics="$("$tmpdir/skanda_perf_go" bench-compress "$input" "$level" "$bias" "$iterations" "$input_size")"
      cpp_compress_reuse_metrics="$("$tmpdir/skanda_perf_cpp" bench-compress-reuse "$input" "$level" "$bias" "$iterations" "$input_size")"
      go_compress_reuse_metrics="$("$tmpdir/skanda_perf_go" bench-compress-reuse "$input" "$level" "$bias" "$iterations" "$input_size")"
      cpp_decompress_cpp_artifact="$("$tmpdir/skanda_perf_cpp" bench-decompress "$input" "$cpp_artifact" "$iterations" "$input_size")"
      go_decompress_go_artifact="$("$tmpdir/skanda_perf_go" bench-decompress "$input" "$go_artifact" "$iterations" "$input_size")"
      cpp_decompress_go_artifact="$("$tmpdir/skanda_perf_cpp" bench-decompress "$input" "$go_artifact" "$iterations" "$input_size")"
      go_decompress_cpp_artifact="$("$tmpdir/skanda_perf_go" bench-decompress "$input" "$cpp_artifact" "$iterations" "$input_size")"
      cpp_decode_reuse_cpp_artifact="$("$tmpdir/skanda_perf_cpp" bench-decode-reuse "$input" "$cpp_artifact" "$iterations" "$input_size")"
      go_decode_reuse_go_artifact="$("$tmpdir/skanda_perf_go" bench-decode-reuse "$input" "$go_artifact" "$iterations" "$input_size")"
      cpp_decode_reuse_go_artifact="$("$tmpdir/skanda_perf_cpp" bench-decode-reuse "$input" "$go_artifact" "$iterations" "$input_size")"
      go_decode_reuse_cpp_artifact="$("$tmpdir/skanda_perf_go" bench-decode-reuse "$input" "$cpp_artifact" "$iterations" "$input_size")"
      python3 - "$run_id" "$timestamp" "$host" "$os_info" "$go_version" "$cxx_version" "$cxx_flags" "$cpp_commit" "$input" "$input_sha256" "$input_size" "$level" "$bias" "$iterations" "$cpp_size" "$go_size" "$cpp_artifact_sha256" "$go_artifact_sha256" "$cpp_compress_metrics" "$go_compress_metrics" "$cpp_compress_reuse_metrics" "$go_compress_reuse_metrics" "$cpp_decompress_cpp_artifact" "$go_decompress_go_artifact" "$cpp_decompress_go_artifact" "$go_decompress_cpp_artifact" "$cpp_decode_reuse_cpp_artifact" "$go_decode_reuse_go_artifact" "$cpp_decode_reuse_go_artifact" "$go_decode_reuse_cpp_artifact" "$size_gate_pct" "$speed_gate_pct" <<'PY'
import csv
import sys

(
    run_id,
    timestamp,
    host,
    os_info,
    go_version,
    cxx_version,
    cxx_flags,
    cpp_commit,
    input_path,
    input_sha256,
    input_size,
    level,
    bias,
    iterations,
    cpp_size,
    go_size,
    cpp_artifact_sha256,
    go_artifact_sha256,
    cpp_compress_metrics,
    go_compress_metrics,
    cpp_compress_reuse_metrics,
    go_compress_reuse_metrics,
    cpp_decompress_cpp_artifact,
    go_decompress_go_artifact,
    cpp_decompress_go_artifact,
    go_decompress_cpp_artifact,
    cpp_decode_reuse_cpp_artifact,
    go_decode_reuse_go_artifact,
    cpp_decode_reuse_go_artifact,
    go_decode_reuse_cpp_artifact,
    size_gate_pct,
    speed_gate_pct,
) = sys.argv[1:]

input_size_i = int(input_size)
cpp_size_i = int(cpp_size)
go_size_i = int(go_size)
cpp_compress_size, cpp_comp = cpp_compress_metrics.split(",")
go_compress_size, go_comp, go_comp_alloc = go_compress_metrics.split(",")
cpp_compress_reuse_size, cpp_comp_reuse = cpp_compress_reuse_metrics.split(",")
go_compress_reuse_size, go_comp_reuse, go_comp_reuse_alloc = go_compress_reuse_metrics.split(",")
go_decomp_go, go_decomp_go_alloc = go_decompress_go_artifact.split(",")
go_decomp_cpp, go_decomp_cpp_alloc = go_decompress_cpp_artifact.split(",")
go_decode_reuse_go, go_decode_reuse_go_alloc = go_decode_reuse_go_artifact.split(",")
go_decode_reuse_cpp, go_decode_reuse_cpp_alloc = go_decode_reuse_cpp_artifact.split(",")

if int(cpp_compress_size) != cpp_size_i:
    raise SystemExit(f"C++ compress size mismatch: artifact={cpp_size_i} bench={cpp_compress_size}")
if int(go_compress_size) != go_size_i:
    raise SystemExit(f"Go compress size mismatch: artifact={go_size_i} bench={go_compress_size}")
if int(cpp_compress_reuse_size) != cpp_size_i:
    raise SystemExit(f"C++ reuse compress size mismatch: artifact={cpp_size_i} bench={cpp_compress_reuse_size}")
if int(go_compress_reuse_size) != go_size_i:
    raise SystemExit(f"Go reuse compress size mismatch: artifact={go_size_i} bench={go_compress_reuse_size}")

ratio_cpp = cpp_size_i / input_size_i if input_size_i else 1.0
ratio_go = go_size_i / input_size_i if input_size_i else 1.0
size_delta_bytes = go_size_i - cpp_size_i
size_delta_pct = (size_delta_bytes / cpp_size_i * 100.0) if cpp_size_i else 0.0
cpp_comp_f = float(cpp_comp)
go_comp_f = float(go_comp)
cpp_comp_reuse_f = float(cpp_comp_reuse)
go_comp_reuse_f = float(go_comp_reuse)
cpp_decomp_cpp_f = float(cpp_decompress_cpp_artifact)
go_decomp_go_f = float(go_decomp_go)
cpp_decomp_go_f = float(cpp_decompress_go_artifact)
go_decomp_cpp_f = float(go_decomp_cpp)
compress_speed_pct = (go_comp_f / cpp_comp_f * 100.0) if cpp_comp_f else 0.0
compress_reuse_speed_pct = (go_comp_reuse_f / cpp_comp_reuse_f * 100.0) if cpp_comp_reuse_f else 0.0
compress_speed_delta_pct = ((go_comp_f - cpp_comp_f) / cpp_comp_f * 100.0) if cpp_comp_f else 0.0
compress_reuse_speed_delta_pct = ((go_comp_reuse_f - cpp_comp_reuse_f) / cpp_comp_reuse_f * 100.0) if cpp_comp_reuse_f else 0.0

def pct_gate(value, threshold, higher_is_better):
    if not threshold:
        return "not_evaluated"
    threshold_f = float(threshold)
    if higher_is_better:
        return "pass" if value >= threshold_f else "fail"
    return "pass" if value <= threshold_f else "fail"

def emit(origin, artifact_size, artifact_sha, cpp_decomp, go_decomp, go_decomp_alloc, cpp_decode_reuse, go_decode_reuse, go_decode_reuse_alloc):
    cpp_decomp_f = float(cpp_decomp)
    go_decomp_f = float(go_decomp)
    decompress_speed_pct = (go_decomp_f / cpp_decomp_f * 100.0) if cpp_decomp_f else 0.0
    decompress_speed_delta_pct = ((go_decomp_f - cpp_decomp_f) / cpp_decomp_f * 100.0) if cpp_decomp_f else 0.0
    cpp_decode_reuse_f = float(cpp_decode_reuse)
    go_decode_reuse_f = float(go_decode_reuse)
    decode_reuse_speed_pct = (go_decode_reuse_f / cpp_decode_reuse_f * 100.0) if cpp_decode_reuse_f else 0.0
    decode_reuse_speed_delta_pct = ((go_decode_reuse_f - cpp_decode_reuse_f) / cpp_decode_reuse_f * 100.0) if cpp_decode_reuse_f else 0.0
    writer.writerow([
        run_id,
        timestamp,
        host,
        os_info,
        go_version,
        cxx_version,
        cxx_flags,
        cpp_commit,
        input_path,
        input_sha256,
        input_size,
        level,
        bias,
        iterations,
        cpp_size,
        go_size,
        f"{ratio_cpp:.6f}",
        f"{ratio_go:.6f}",
        str(size_delta_bytes),
        f"{size_delta_pct:.2f}",
        pct_gate(size_delta_pct, size_gate_pct, False),
        cpp_comp,
        go_comp,
        f"{compress_speed_pct:.2f}",
        pct_gate(compress_speed_pct, speed_gate_pct, True),
        origin,
        artifact_size,
        artifact_sha,
        cpp_decomp,
        go_decomp,
        f"{decompress_speed_pct:.2f}",
        pct_gate(decompress_speed_pct, speed_gate_pct, True),
        go_comp_alloc,
        go_decomp_alloc,
        cpp_decode_reuse,
        go_decode_reuse,
        f"{decode_reuse_speed_pct:.2f}",
        go_decode_reuse_alloc,
        cpp_comp_reuse,
        go_comp_reuse,
        f"{compress_reuse_speed_pct:.2f}",
        go_comp_reuse_alloc,
        f"{compress_speed_delta_pct:.2f}",
        f"{decompress_speed_delta_pct:.2f}",
        f"{decode_reuse_speed_delta_pct:.2f}",
        f"{compress_reuse_speed_delta_pct:.2f}",
    ])

writer = csv.writer(sys.stdout)
emit("cpp", cpp_size, cpp_artifact_sha256, cpp_decompress_cpp_artifact, go_decomp_cpp, go_decomp_cpp_alloc, cpp_decode_reuse_cpp_artifact, go_decode_reuse_cpp, go_decode_reuse_cpp_alloc)
emit("go", go_size, go_artifact_sha256, cpp_decompress_go_artifact, go_decomp_go, go_decomp_go_alloc, cpp_decode_reuse_go_artifact, go_decode_reuse_go, go_decode_reuse_go_alloc)
PY
    done
  done
  index=$((index + 1))
done
