package skanda

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const referenceSkandaCommit = "650b34b17a25b89024b7d19820c17c95a9d7591c"

func TestCppCompatibilityWhenHeaderAvailable(t *testing.T) {
	header := os.Getenv("SKANDA_CPP_HEADER")
	if header == "" {
		header = "/tmp/skanda-upstream/Skanda.h"
	}
	if _, err := os.Stat(header); err != nil {
		t.Skip("upstream Skanda.h is not available")
	}
	requireReferenceSkandaHeader(t, header)
	cxx, err := exec.LookPath("g++")
	if err != nil {
		t.Skip("g++ is not available")
	}

	dir := t.TempDir()
	tool := filepath.Join(dir, "skanda_compat")
	source := filepath.Join(dir, "skanda_compat.cpp")
	sourceCode := strings.ReplaceAll(cppCompatSource, "__SKANDA_HEADER__", cxxIncludePath(header))
	if err := os.WriteFile(source, []byte(sourceCode), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(cxx, "-std=c++17", "-O2", source, "-o", tool).CombinedOutput(); err != nil {
		t.Fatalf("build compatibility tool: %v\n%s", err, output)
	}

	var cases []struct {
		level int
		bias  string
	}
	for level := 0; level <= 10; level++ {
		for _, bias := range []string{"1.0", "0.5", "0.05"} {
			cases = append(cases, struct {
				level int
				bias  string
			}{level: level, bias: bias})
		}
	}

	for corpusName, input := range map[string][]byte{
		"repeated": bytes.Repeat([]byte("0123456789abcdef skanda raw entropy compatibility\n"), 2048),
		"mixed":    mixedCompatibilityCorpus(),
	} {
		inputPath := filepath.Join(dir, corpusName+".bin")
		if err := os.WriteFile(inputPath, input, 0o644); err != nil {
			t.Fatal(err)
		}

		goCompressed, err := Compress(input)
		if err != nil {
			t.Fatal(err)
		}
		goCompressedPath := filepath.Join(dir, corpusName+"-go.skanda")
		goDecodedByCppPath := filepath.Join(dir, corpusName+"-go.decoded")
		if err := os.WriteFile(goCompressedPath, goCompressed, 0o644); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(tool, "decode", goCompressedPath, goDecodedByCppPath, strconv.Itoa(len(input))).CombinedOutput(); err != nil {
			t.Fatalf("C++ decode Go stream %s: %v\n%s", corpusName, err, output)
		}
		goDecodedByCpp, err := os.ReadFile(goDecodedByCppPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(goDecodedByCpp, input) {
			t.Fatalf("C++ decoder output mismatch for %s", corpusName)
		}

		for _, tc := range cases {
			name := corpusName + "-level-" + strconv.Itoa(tc.level) + "-bias-" + tc.bias
			cppCompressedPath := filepath.Join(dir, "cpp-"+name+".skanda")
			if output, err := exec.Command(tool, "compress", inputPath, cppCompressedPath, strconv.Itoa(tc.level), tc.bias).CombinedOutput(); err != nil {
				t.Fatalf("C++ compress %s: %v\n%s", name, err, output)
			}
			cppCompressed, err := os.ReadFile(cppCompressedPath)
			if err != nil {
				t.Fatal(err)
			}
			if tc.level == 2 && tc.bias == "0.05" && !hasAdvancedDistanceStream(t, cppCompressed) {
				t.Fatalf("C++ stream %s did not exercise advanced distance mode", name)
			}
			goDecoded, err := Decompress(cppCompressed, len(input))
			if err != nil {
				t.Fatalf("Go decode C++ stream %s: %v", name, err)
			}
			if !bytes.Equal(goDecoded, input) {
				t.Fatalf("Go decoder output mismatch for %s", name)
			}
		}
	}

	for _, tc := range []struct {
		name      string
		src       []byte
		blockEnd  int
		bias      float64
		wantFlags int
	}{
		{
			name:      "literal-delta",
			src:       incrementingBytes(160),
			blockEnd:  128,
			bias:      0.05,
			wantFlags: streamLiteralsDelta,
		},
		{
			name:      "literal-pos-mask",
			src:       positionalBytes(20064),
			blockEnd:  20032,
			bias:      0.5,
			wantFlags: streamLiteralsPosMask3,
		},
	} {
		compressed := encodeLiteralOnlyBlockForTest(t, tc.src, tc.blockEnd, tc.bias)
		if flags := literalFlagsForFirstBlock(t, compressed); flags&tc.wantFlags == 0 {
			t.Fatalf("%s literal flags = %d, want %d", tc.name, flags, tc.wantFlags)
		}
		compressedPath := filepath.Join(dir, tc.name+".skanda")
		decodedPath := filepath.Join(dir, tc.name+".decoded")
		if err := os.WriteFile(compressedPath, compressed, 0o644); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(tool, "decode", compressedPath, decodedPath, strconv.Itoa(len(tc.src))).CombinedOutput(); err != nil {
			t.Fatalf("C++ decode Go %s stream: %v\n%s", tc.name, err, output)
		}
		decoded, err := os.ReadFile(decodedPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decoded, tc.src) {
			t.Fatalf("C++ decoder output mismatch for %s", tc.name)
		}
	}
}

func requireReferenceSkandaHeader(t *testing.T, header string) {
	t.Helper()
	want, ok := os.LookupEnv("SKANDA_CPP_COMMIT")
	if !ok {
		want = referenceSkandaCommit
	}
	if want == "" {
		return
	}
	out, err := exec.Command("git", "-C", filepath.Dir(header), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("upstream Skanda.h is not in a verifiable git checkout: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("upstream Skanda.h commit = %s, want %s", got, want)
	}
}

func cxxIncludePath(path string) string {
	path = strings.ReplaceAll(path, `\`, `\\`)
	return strings.ReplaceAll(path, `"`, `\"`)
}

func mixedCompatibilityCorpus() []byte {
	out := make([]byte, 0, 192*1024)
	phrases := [][]byte{
		[]byte("alpha beta gamma delta "),
		[]byte("The quick brown fox jumps over the lazy dog. "),
		[]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	}
	for i := 0; len(out) < 192*1024; i++ {
		out = append(out, phrases[i%len(phrases)]...)
		if i%7 == 0 {
			base := len(out) - min(len(out), 4096)
			out = append(out, out[base:base+min(257, len(out)-base)]...)
		}
		if i%11 == 0 {
			for j := 0; j < 53; j++ {
				out = append(out, byte(i*j+j*j))
			}
		}
	}
	return out
}

func incrementingBytes(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}

func positionalBytes(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte('a' + (i & 3))
	}
	return out
}

func hasAdvancedDistanceStream(t *testing.T, src []byte) bool {
	t.Helper()
	cpos := 0
	dpos := 0
	for {
		blockSize, blockType, flags, err := readHeader(src, &cpos)
		if err != nil {
			t.Fatal(err)
		}
		switch blockType {
		case blockRaw:
			cpos += blockSize
			dpos += blockSize
			if flags&blockLast != 0 {
				return false
			}
		case blockCompressed:
			if dpos == 0 {
				cpos++
				dpos++
			}
			_, literalFlags, err := decodeEntropy(src, &cpos)
			if err != nil {
				t.Fatal(err)
			}
			if literalFlags&streamLiteralsPosMask3 != 0 {
				for i := 1; i < 4; i++ {
					if _, _, err := decodeEntropy(src, &cpos); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, _, err := decodeEntropy(src, &cpos); err != nil {
				t.Fatal(err)
			}
			distances, distanceFlags, err := decodeEntropy(src, &cpos)
			if err != nil {
				t.Fatal(err)
			}
			if distanceFlags&streamDistanceAdvanced != 0 {
				return true
			}
			if _, _, err := decodeEntropy(src, &cpos); err != nil {
				t.Fatal(err)
			}
			dpos += blockSize
			if flags&blockLast != 0 {
				return false
			}
			_ = distances
		default:
			t.Fatal(ErrCorrupt)
		}
	}
}

const cppCompatSource = `
#include <cstdint>
#include <cstdlib>
#include <fstream>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

#define SKANDA_IMPLEMENTATION
	#include "__SKANDA_HEADER__"

static std::vector<uint8_t> read_file(const char* path) {
    std::ifstream in(path, std::ios::binary);
    if (!in) {
        throw std::runtime_error("open input failed");
    }
    return std::vector<uint8_t>((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
}

static void write_file(const char* path, const std::vector<uint8_t>& data) {
    std::ofstream out(path, std::ios::binary);
    if (!out) {
        throw std::runtime_error("open output failed");
    }
    out.write(reinterpret_cast<const char*>(data.data()), data.size());
}

int main(int argc, char** argv) {
    try {
        if (argc < 4) {
            return 2;
        }
        std::string mode = argv[1];
        if (mode == "decode") {
            if (argc != 5) {
                return 2;
            }
            std::vector<uint8_t> compressed = read_file(argv[2]);
            size_t original_size = static_cast<size_t>(std::strtoull(argv[4], nullptr, 10));
            std::vector<uint8_t> output(original_size);
            size_t err = skanda::decompress(compressed.data(), compressed.size(), output.data(), output.size());
            if (skanda::is_error(err)) {
                std::cerr << "decode error: " << err << "\n";
                return 1;
            }
            write_file(argv[3], output);
            return 0;
        }
        if (mode == "compress") {
            std::vector<uint8_t> input = read_file(argv[2]);
            std::vector<uint8_t> compressed(skanda::compress_bound(input.size()));
            int level = argc >= 5 ? std::atoi(argv[4]) : 2;
            float bias = argc >= 6 ? std::strtof(argv[5], nullptr) : 0.5f;
            size_t compressed_size = skanda::compress(input.data(), input.size(), compressed.data(), level, bias);
            if (skanda::is_error(compressed_size)) {
                std::cerr << "compress error: " << compressed_size << "\n";
                return 1;
            }
            compressed.resize(compressed_size);
            write_file(argv[3], compressed);
            return 0;
        }
        return 2;
    } catch (const std::exception& e) {
        std::cerr << e.what() << "\n";
        return 1;
    }
}
`
