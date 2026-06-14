#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "$#" -ne 1 ]]; then
  echo "usage: $0 <legacy-manifest.csv>" >&2
  echo "required columns: id,compressed_path,compressed_size,compressed_sha256,decompressed_size,decoded_sha256" >&2
  echo "optional columns: format,producer,source_version,level,bias,original_path" >&2
  echo "env: SKANDA_LEGACY_MAX_DECOMPRESSED_SIZE=<bytes>, default 1073741824" >&2
  exit 2
fi

manifest="$1"
if [[ ! -f "$manifest" ]]; then
  echo "missing manifest: $manifest" >&2
  exit 2
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cat > "$tmpdir/legacy_decode.go" <<'GO'
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	skanda "github.com/calorado/skanda-go"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: legacy_decode <manifest.csv>")
		os.Exit(2)
	}
	manifestPath := os.Args[1]
	file, err := os.Open(manifestPath)
	if err != nil {
		fail(err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		fail(err)
	}
	if len(records) == 0 {
		fail(fmt.Errorf("empty manifest"))
	}

	columns := map[string]int{}
	for i, name := range records[0] {
		columns[strings.ToLower(strings.TrimSpace(name))] = i
	}
	requireColumn(columns, "compressed_path")
	requireColumn(columns, "compressed_size")
	requireColumn(columns, "compressed_sha256")
	requireColumn(columns, "id")
	if _, ok := columns["decompressed_size"]; !ok {
		requireColumn(columns, "original_size")
	}
	if _, ok := columns["decoded_sha256"]; !ok {
		requireColumn(columns, "sha256")
	}

	manifestDir := filepath.Dir(manifestPath)
	maxDecompressedSize, err := maxDecompressedSize()
	if err != nil {
		fail(err)
	}
	seenIDs := map[string]struct{}{}
	seenCompressedPaths := map[string]struct{}{}
	writer := csv.NewWriter(os.Stdout)
	writeCSV(writer, []string{"id", "compressed_path", "format", "producer", "source_version", "level", "bias", "decompressed_size", "compressed_size", "decoded_sha256", "result"})
	failures := 0
	for rowNumber, record := range records[1:] {
		if blankRecord(record) {
			continue
		}
		result, err := verifyRow(manifestDir, columns, record, rowNumber+2, maxDecompressedSize, seenIDs, seenCompressedPaths)
		if err != nil {
			failures++
			result.result = "fail:" + csvSafeMessage(err)
		}
		writeCSV(writer, []string{
			result.id,
			result.compressedPath,
			result.format,
			result.producer,
			result.sourceVersion,
			result.level,
			result.bias,
			strconv.Itoa(result.decompressedSize),
			strconv.Itoa(result.compressedSize),
			result.decodedSHA256,
			result.result,
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		fail(err)
	}
	if failures > 0 {
		os.Exit(1)
	}
}

type verifyResult struct {
	id               string
	compressedPath   string
	format           string
	producer         string
	sourceVersion    string
	level            string
	bias             string
	decompressedSize int
	compressedSize   int
	decodedSHA256    string
	result           string
}

func verifyRow(baseDir string, columns map[string]int, record []string, rowNumber int, maxDecompressedSize int, seenIDs, seenCompressedPaths map[string]struct{}) (verifyResult, error) {
	result := verifyResult{
		id:            value(columns, record, "id"),
		compressedPath: value(columns, record, "compressed_path"),
		format:        value(columns, record, "format"),
		producer:      value(columns, record, "producer"),
		sourceVersion: value(columns, record, "source_version"),
		level:         value(columns, record, "level"),
		bias:          value(columns, record, "bias"),
	}
	if result.id == "" {
		return result, fmt.Errorf("missing id")
	}
	if _, ok := seenIDs[result.id]; ok {
		return result, fmt.Errorf("duplicate id: %s", result.id)
	}
	seenIDs[result.id] = struct{}{}
	if result.compressedPath == "" {
		return result, fmt.Errorf("missing compressed_path")
	}
	compressedPath, err := resolveManifestPath(baseDir, result.compressedPath)
	if err != nil {
		return result, err
	}
	canonicalCompressedPath, err := filepath.Abs(compressedPath)
	if err != nil {
		return result, err
	}
	if _, ok := seenCompressedPaths[canonicalCompressedPath]; ok {
		return result, fmt.Errorf("duplicate compressed_path: %s", result.compressedPath)
	}
	seenCompressedPaths[canonicalCompressedPath] = struct{}{}
	compressed, err := os.ReadFile(compressedPath)
	if err != nil {
		return result, err
	}
	result.compressedSize = len(compressed)
	expectedCompressedSize, err := strconv.Atoi(value(columns, record, "compressed_size"))
	if err != nil || expectedCompressedSize < 0 {
		return result, fmt.Errorf("invalid compressed_size: %q", value(columns, record, "compressed_size"))
	}
	if result.compressedSize != expectedCompressedSize {
		return result, fmt.Errorf("compressed size mismatch: got %d want %d", result.compressedSize, expectedCompressedSize)
	}

	expectedCompressedSHA, err := normalizeHex(value(columns, record, "compressed_sha256"))
	if err != nil {
		return result, err
	}
	if expectedCompressedSHA != "" {
		actualCompressedSHA := sha256Hex(compressed)
		if actualCompressedSHA != expectedCompressedSHA {
			return result, fmt.Errorf("compressed sha256 mismatch: got %s want %s", actualCompressedSHA, expectedCompressedSHA)
		}
	}

	sizeText := value(columns, record, "decompressed_size")
	if sizeText == "" {
		sizeText = value(columns, record, "original_size")
	}
	decompressedSize, err := strconv.Atoi(sizeText)
	if err != nil || decompressedSize < 0 {
		return result, fmt.Errorf("invalid decompressed_size: %q", sizeText)
	}
	if decompressedSize > maxDecompressedSize {
		return result, fmt.Errorf("decompressed_size %d exceeds limit %d", decompressedSize, maxDecompressedSize)
	}
	result.decompressedSize = decompressedSize

	decoded, err := skanda.Decompress(compressed, decompressedSize)
	if err != nil {
		return result, err
	}
	result.decodedSHA256 = sha256Hex(decoded)

	expectedSHAText := value(columns, record, "decoded_sha256")
	if expectedSHAText == "" {
		expectedSHAText = value(columns, record, "sha256")
	}
	expectedSHA, err := normalizeHex(expectedSHAText)
	if err != nil {
		return result, err
	}
	originalPath := value(columns, record, "original_path")
	if expectedSHA == "" {
		return result, fmt.Errorf("missing decoded_sha256")
	}
	if expectedSHA != "" && result.decodedSHA256 != expectedSHA {
		return result, fmt.Errorf("decoded sha256 mismatch: got %s want %s", result.decodedSHA256, expectedSHA)
	}
	if originalPath != "" {
		resolvedOriginalPath, err := resolveManifestPath(baseDir, originalPath)
		if err != nil {
			return result, err
		}
		original, err := os.ReadFile(resolvedOriginalPath)
		if err != nil {
			return result, err
		}
		if !bytes.Equal(decoded, original) {
			return result, fmt.Errorf("decoded bytes differ from original_path")
		}
	}
	result.result = "pass"
	return result, nil
}

func requireColumn(columns map[string]int, name string) {
	if _, ok := columns[name]; !ok {
		fail(fmt.Errorf("missing required column: %s", name))
	}
}

func value(columns map[string]int, record []string, name string) string {
	index, ok := columns[name]
	if !ok || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

func resolveManifestPath(baseDir, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", path)
	}
	clean := filepath.Clean(path)
	if clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", fmt.Errorf("path escapes manifest directory: %s", path)
	}
	resolved := filepath.Clean(filepath.Join(baseDir, clean))
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symlinks are not allowed: %s", path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	return resolved, nil
}

func blankRecord(record []string) bool {
	for _, field := range record {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

func normalizeHex(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if strings.HasPrefix(value, "sha256:") {
		value = strings.TrimPrefix(value, "sha256:")
	}
	if value == "" {
		return "", nil
	}
	if _, err := hex.DecodeString(value); err != nil || len(value) != sha256.Size*2 {
		return "", fmt.Errorf("invalid sha256: %q", value)
	}
	return value, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func maxDecompressedSize() (int, error) {
	value := strings.TrimSpace(os.Getenv("SKANDA_LEGACY_MAX_DECOMPRESSED_SIZE"))
	if value == "" {
		return 1 << 30, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 0 {
		return 0, fmt.Errorf("invalid SKANDA_LEGACY_MAX_DECOMPRESSED_SIZE: %q", value)
	}
	return limit, nil
}

func csvSafeMessage(err error) string {
	return strings.ReplaceAll(err.Error(), "\n", " ")
}

func writeCSV(writer *csv.Writer, record []string) {
	if err := writer.Write(record); err != nil && err != io.ErrClosedPipe {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
GO

(cd "$repo_root" && go run "$tmpdir/legacy_decode.go" "$manifest")
