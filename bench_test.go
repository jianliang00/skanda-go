package skanda

import (
	"os"
	"testing"
)

func BenchmarkCompressMixed(b *testing.B) {
	src := mixedCompatibilityCorpus()
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Compress(src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompressMixed(b *testing.B) {
	src := mixedCompatibilityCorpus()
	compressed, err := Compress(src)
	if err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, len(src))
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Decode(dst, compressed); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExternalCompressLevel0(b *testing.B) {
	benchmarkExternalCompress(b, 0, 0.05)
}

func BenchmarkExternalCompressLevel6(b *testing.B) {
	benchmarkExternalCompress(b, 6, 0.05)
}

func BenchmarkExternalCompressLevel10(b *testing.B) {
	benchmarkExternalCompress(b, 10, 0.05)
}

func BenchmarkExternalDecompressLevel0(b *testing.B) {
	benchmarkExternalDecompress(b, 0, 0.05)
}

func BenchmarkExternalDecompressLevel6(b *testing.B) {
	benchmarkExternalDecompress(b, 6, 0.05)
}

func BenchmarkExternalDecompressLevel10(b *testing.B) {
	benchmarkExternalDecompress(b, 10, 0.05)
}

func benchmarkExternalCompress(b *testing.B, level int, bias float64) {
	path := os.Getenv("SKANDA_BENCH_CORPUS")
	if path == "" {
		b.Skip("SKANDA_BENCH_CORPUS is not set")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compress(src, WithLevel(level), WithDecSpeedBias(bias)); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkExternalDecompress(b *testing.B, level int, bias float64) {
	path := os.Getenv("SKANDA_BENCH_CORPUS")
	if path == "" {
		b.Skip("SKANDA_BENCH_CORPUS is not set")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	compressed, err := Compress(src, WithLevel(level), WithDecSpeedBias(bias))
	if err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, len(src))
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Decode(dst, compressed); err != nil {
			b.Fatal(err)
		}
	}
}
