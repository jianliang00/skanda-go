package skanda

import (
	"bytes"
	"errors"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

func TestRoundTripSmallRawBlocks(t *testing.T) {
	for size := 0; size <= lastBytes+32; size++ {
		src := patterned(size)
		compressed, err := Compress(src)
		if err != nil {
			t.Fatalf("compress size %d: %v", size, err)
		}
		got, err := Decompress(compressed, len(src))
		if err != nil {
			t.Fatalf("decompress size %d: %v", size, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("size %d mismatch", size)
		}
	}
}

func TestRoundTripCompressedPattern(t *testing.T) {
	src := bytes.Repeat([]byte("skanda-go can encode skanda-compatible lz blocks; "), 2048)
	compressed, err := Compress(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) >= len(src) {
		t.Fatalf("expected compression, got compressed=%d raw=%d", len(compressed), len(src))
	}
	got, err := Decompress(compressed, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("round trip mismatch")
	}
}

func TestRoundTripRandomData(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, size := range []int{64, 1024, 65536, maxBlockSize + 4096} {
		src := make([]byte, size)
		if _, err := rng.Read(src); err != nil {
			t.Fatal(err)
		}
		compressed, err := Compress(src)
		if err != nil {
			t.Fatalf("compress size %d: %v", size, err)
		}
		got, err := Decompress(compressed, len(src))
		if err != nil {
			t.Fatalf("decompress size %d: %v", size, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("size %d mismatch", size)
		}
	}
}

func TestRoundTripBlockBoundaries(t *testing.T) {
	for _, size := range []int{
		maxBlockSize - 1,
		maxBlockSize,
		maxBlockSize + 1,
		2*maxBlockSize - 1,
		2 * maxBlockSize,
		2*maxBlockSize + 1,
	} {
		src := bytes.Repeat([]byte("boundary-data-0123456789"), size/24+1)[:size]
		compressed, err := Compress(src)
		if err != nil {
			t.Fatalf("compress size %d: %v", size, err)
		}
		got, err := Decompress(compressed, len(src))
		if err != nil {
			t.Fatalf("decompress size %d: %v", size, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("size %d mismatch", size)
		}
	}
}

func TestCompressionLevelAffectsOutput(t *testing.T) {
	src := mixedCompatibilityCorpus()
	level0, err := Compress(src, WithLevel(0))
	if err != nil {
		t.Fatal(err)
	}
	level2, err := Compress(src, WithLevel(2))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(level0, level2) {
		t.Fatal("compression level did not affect output")
	}
}

func TestEncodeAppendsAndReusesCapacity(t *testing.T) {
	src := bytes.Repeat([]byte("appendable encoder payload "), 512)
	want, err := Compress(src, WithLevel(6), WithDecSpeedBias(0.05))
	if err != nil {
		t.Fatal(err)
	}

	prefix := []byte("prefix:")
	dst := make([]byte, len(prefix), len(prefix)+CompressBound(len(src)))
	copy(dst, prefix)
	beforeCap := cap(dst)
	got, err := Encode(dst, src, WithLevel(6), WithDecSpeedBias(0.05))
	if err != nil {
		t.Fatal(err)
	}
	if cap(got) != beforeCap {
		t.Fatal("Encode did not reuse caller-provided capacity")
	}
	if !bytes.Equal(got[:len(prefix)], prefix) {
		t.Fatal("Encode did not preserve destination prefix")
	}
	if !bytes.Equal(got[len(prefix):], want) {
		t.Fatal("Encode output differs from Compress")
	}
	decoded, err := Decompress(got[len(prefix):], len(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, src) {
		t.Fatal("round trip mismatch")
	}
}

func TestEncodeProgressReportsAppendedBytes(t *testing.T) {
	src := bytes.Repeat([]byte("progress payload "), 256)
	prefix := []byte("existing data")
	var lastCompressed int
	got, err := Encode(append([]byte(nil), prefix...), src, WithProgress(func(_, compressedBytes int) bool {
		lastCompressed = compressedBytes
		return false
	}))
	if err != nil {
		t.Fatal(err)
	}
	if lastCompressed != len(got)-len(prefix) {
		t.Fatalf("progress compressed bytes = %d, want %d", lastCompressed, len(got)-len(prefix))
	}
	if !bytes.Equal(got[:len(prefix)], prefix) {
		t.Fatal("Encode did not preserve destination prefix")
	}
}

func TestEncoderReuseMatchesEncode(t *testing.T) {
	cases := []struct {
		src   []byte
		level int
		bias  float64
	}{
		{bytes.Repeat([]byte("fast reusable encoder payload "), 512), 0, 0.05},
		{bytes.Repeat([]byte("optimal reusable encoder payload "), 4096), 6, 0.05},
		{mixedCompatibilityCorpus()[:32768], 10, 0.5},
		{bytes.Repeat([]byte("smaller follow-up payload "), 256), 2, 1.0},
	}

	var encoder Encoder
	defer encoder.Close()
	for _, tc := range cases {
		want, err := Encode(nil, tc.src, WithLevel(tc.level), WithDecSpeedBias(tc.bias))
		if err != nil {
			t.Fatal(err)
		}
		prefix := []byte("prefix:")
		dst := append([]byte(nil), prefix...)
		got, err := encoder.Encode(dst, tc.src, WithLevel(tc.level), WithDecSpeedBias(tc.bias))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got[:len(prefix)], prefix) {
			t.Fatal("Encoder did not preserve destination prefix")
		}
		if !bytes.Equal(got[len(prefix):], want) {
			t.Fatalf("Encoder output differs from Encode at level %d bias %.2f", tc.level, tc.bias)
		}
		decoded, err := Decompress(got[len(prefix):], len(tc.src))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decoded, tc.src) {
			t.Fatal("round trip mismatch")
		}
	}
}

func TestDecoderReuseMatchesDecode(t *testing.T) {
	inputs := [][]byte{
		bytes.Repeat([]byte("decoder reuse payload "), 512),
		bytes.Repeat([]byte("decoder reuse different payload "), 2048),
		mixedCompatibilityCorpus()[:65536],
	}
	var compressed [][]byte
	for i, src := range inputs {
		data, err := Compress(src, WithLevel(i*3), WithDecSpeedBias(0.05))
		if err != nil {
			t.Fatal(err)
		}
		compressed = append(compressed, data)
	}

	var decoder Decoder
	defer decoder.Close()
	for i, src := range inputs {
		dst := make([]byte, len(src))
		if err := decoder.Decode(dst, compressed[i]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dst, src) {
			t.Fatal("decoded output mismatch")
		}
	}
}

func TestCorruptInput(t *testing.T) {
	if _, err := Decompress([]byte{1, 2}, 10); err == nil {
		t.Fatal("expected corrupt input error")
	}
}

func TestDecompressRejectsInvalidOutputSizes(t *testing.T) {
	src := bytes.Repeat([]byte("size-sensitive skanda payload "), 64)
	compressed, err := Compress(src, WithLevel(6), WithDecSpeedBias(0.05))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{-1, len(src) - 1, len(src) + 1} {
		if _, err := Decompress(compressed, size); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("decompress size %d error = %v, want ErrCorrupt", size, err)
		}
	}
}

func TestDecompressRejectsTruncatedStreams(t *testing.T) {
	src := bytes.Repeat([]byte("truncated stream payload "), 128)
	compressed, err := Compress(src, WithLevel(10), WithDecSpeedBias(0.05))
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(compressed); n++ {
		if _, err := Decompress(compressed[:n], len(src)); err == nil {
			t.Fatalf("truncated stream length %d decoded successfully", n)
		}
	}
}

func TestDecodeUsesExactDestinationSize(t *testing.T) {
	src := bytes.Repeat([]byte("direct decode payload "), 96)
	compressed, err := Compress(src, WithLevel(5), WithDecSpeedBias(0.5))
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, len(src))
	if err := Decode(dst, compressed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst, src) {
		t.Fatal("decoded output mismatch")
	}
	shortDst := make([]byte, len(src)-1)
	if err := Decode(shortDst, compressed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("short destination error = %v, want ErrCorrupt", err)
	}
	longDst := make([]byte, len(src)+1)
	if err := Decode(longDst, compressed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("long destination error = %v, want ErrCorrupt", err)
	}
}

func TestLengthStreamUsesSingleByteCodes(t *testing.T) {
	lengths := []byte{0, 1, 7, 31, 127, 128, 200, 223, 0, 12, 64}
	if !lengthStreamUsesSingleByteCodes(lengths) {
		t.Fatal("length stream with values <= 223 should use single-byte codes")
	}
	for i := range lengths {
		mutated := append([]byte(nil), lengths...)
		mutated[i] = 224
		if lengthStreamUsesSingleByteCodes(mutated) {
			t.Fatalf("length stream with value 224 at index %d used single-byte codes", i)
		}
	}
}

func TestOptionsClampToPublicRange(t *testing.T) {
	src := mixedCompatibilityCorpus()[:8192]
	low, err := Compress(src, WithLevel(-10), WithDecSpeedBias(-1))
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := Compress(src, WithLevel(0), WithDecSpeedBias(0))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(low, minimum) {
		t.Fatal("low options did not clamp to level 0 and bias 0")
	}

	high, err := Compress(src, WithLevel(99), WithDecSpeedBias(2))
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := Compress(src, WithLevel(10), WithDecSpeedBias(1))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(high, maximum) {
		t.Fatal("high options did not clamp to level 10 and bias 1")
	}
}

func TestExternalCorpusRoundTrip(t *testing.T) {
	path := os.Getenv("SKANDA_EXTERNAL_CORPUS")
	if path == "" {
		t.Skip("SKANDA_EXTERNAL_CORPUS is not set")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	level := envInt(t, "SKANDA_EXTERNAL_LEVEL", 0)
	bias := envFloat(t, "SKANDA_EXTERNAL_BIAS", 0.5)
	compressed, err := Compress(src, WithLevel(level), WithDecSpeedBias(bias))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decompress(compressed, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("round trip mismatch")
	}
}

func TestExternalCompressedStream(t *testing.T) {
	path := os.Getenv("SKANDA_EXTERNAL_STREAM")
	if path == "" {
		t.Skip("SKANDA_EXTERNAL_STREAM is not set")
	}
	size := envInt(t, "SKANDA_EXTERNAL_SIZE", -1)
	if size < 0 {
		t.Fatal("SKANDA_EXTERNAL_SIZE must be set")
	}
	compressed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decompress(compressed, size); err != nil {
		t.Fatal(err)
	}
}

func envInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("%s=%q is not an integer", key, value)
	}
	return parsed
}

func envFloat(t *testing.T, key string, fallback float64) float64 {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("%s=%q is not a float", key, value)
	}
	return parsed
}

func patterned(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i*31 + i/3)
	}
	return out
}
