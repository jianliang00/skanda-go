package skanda

import (
	"bytes"
	"testing"
)

func FuzzRoundTrip(f *testing.F) {
	seeds := [][]byte{
		nil,
		[]byte("small"),
		bytes.Repeat([]byte("abcdef0123456789"), 64),
		mixedCompatibilityCorpus()[:8192],
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		compressed, err := Compress(src)
		if err != nil {
			t.Fatalf("compress: %v", err)
		}
		got, err := Decompress(compressed, len(src))
		if err != nil {
			t.Fatalf("decompress: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatal("round trip mismatch")
		}
	})
}

func FuzzDecompress(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		{0, 0, 0},
		{byte(blockRaw | blockLast<<2), 0, 0},
		mustCompressForFuzz([]byte("seed data")),
	} {
		f.Add(seed, 0)
	}
	f.Fuzz(func(t *testing.T, compressed []byte, sizeHint int) {
		if sizeHint < 0 {
			sizeHint = -sizeHint
		}
		sizeHint %= 1 << 20
		_, _ = Decompress(compressed, sizeHint)
	})
}

func FuzzCorruptStream(f *testing.F) {
	seeds := [][]byte{
		bytes.Repeat([]byte("abcdef0123456789"), 64),
		mixedCompatibilityCorpus()[:4096],
	}
	for _, seed := range seeds {
		f.Add(seed, 0, 0)
	}
	f.Fuzz(func(t *testing.T, src []byte, flipIndex int, flipMask int) {
		compressed, err := Compress(src)
		if err != nil {
			t.Fatalf("compress: %v", err)
		}
		if len(compressed) == 0 {
			return
		}
		if flipIndex < 0 {
			flipIndex = -flipIndex
		}
		compressed[flipIndex%len(compressed)] ^= byte(flipMask)
		_, _ = Decompress(compressed, len(src))
	})
}

func FuzzOptions(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		[]byte("option seed"),
		bytes.Repeat([]byte("level-sensitive-data"), 128),
	} {
		f.Add(seed, 2, 0.5)
	}
	f.Fuzz(func(t *testing.T, src []byte, level int, bias float64) {
		compressed, err := Compress(src, WithLevel(level), WithDecSpeedBias(bias))
		if err != nil {
			t.Fatalf("compress: %v", err)
		}
		got, err := Decompress(compressed, len(src))
		if err != nil {
			t.Fatalf("decompress: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatal("round trip mismatch")
		}
	})
}

func FuzzHeader(f *testing.F) {
	for _, seed := range [][]byte{
		{0, 0, 0},
		{byte(blockRaw | blockLast<<2), 0, 0},
		{0xff, 0xff, 0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		pos := 0
		_, _, _, _ = readHeader(src, &pos)
		_, _ = Decompress(src, 0)
	})
}

func mustCompressForFuzz(src []byte) []byte {
	compressed, err := Compress(src)
	if err != nil {
		panic(err)
	}
	return compressed
}
