package skanda

import (
	"bytes"
	"testing"
)

func TestHuffmanEntropyRoundTrip(t *testing.T) {
	cases := [][]byte{
		bytes.Repeat([]byte("aaaaabbbbcccdde"), 256),
		mixedCompatibilityCorpus()[:32768],
	}
	for _, tc := range cases {
		encoded, ok := encodeEntropyHuffman(tc, 0)
		if !ok {
			t.Fatal("huffman encoder declined test data")
		}
		pos := 0
		got, flags, err := decodeEntropy(encoded, &pos)
		if err != nil {
			t.Fatal(err)
		}
		if flags != 0 {
			t.Fatalf("flags = %d, want 0", flags)
		}
		if pos != len(encoded) {
			t.Fatalf("decoded %d bytes of stream, want %d", pos, len(encoded))
		}
		if !bytes.Equal(got, tc) {
			for i := range tc {
				if got[i] != tc[i] {
					t.Fatalf("huffman entropy mismatch at %d: got %d want %d encoded=%d raw=%d", i, got[i], tc[i], len(encoded), len(tc))
				}
			}
			t.Fatalf("huffman entropy length mismatch: got %d want %d", len(got), len(tc))
		}
	}
}

func TestPackageMergeHuffmanEntropyRoundTrip(t *testing.T) {
	src := packageMergeTestCorpus()
	encoded, ok := encodeEntropyHuffmanWithCodegen(src, streamLiteralsDelta, false)
	if !ok {
		t.Fatal("package-merge huffman encoder declined test data")
	}
	pos := 0
	got, flags, err := decodeEntropy(encoded, &pos)
	if err != nil {
		t.Fatal(err)
	}
	if flags != streamLiteralsDelta {
		t.Fatalf("flags = %d, want %d", flags, streamLiteralsDelta)
	}
	if pos != len(encoded) {
		t.Fatalf("decoded %d bytes of stream, want %d", pos, len(encoded))
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("package-merge huffman round trip mismatch")
	}
}

func TestPackageMergeHuffmanCodegenFillsCodeSpace(t *testing.T) {
	src := packageMergeTestCorpus()
	var hist [256]uint32
	for _, b := range src {
		hist[b]++
	}
	var symbols [256]huffmanSymbol
	packageMergeHuffmanCodegen(hist[:], symbols[:], 256, maxHuffmanCodeLength)

	usedCodeSpace := 0
	for i, count := range hist {
		switch {
		case count == 0:
			if symbols[i].bits != maxHuffmanCodeLength+1 {
				t.Fatalf("absent symbol %d bits = %d, want %d", i, symbols[i].bits, maxHuffmanCodeLength+1)
			}
		case symbols[i].bits == 0 || symbols[i].bits > maxHuffmanCodeLength:
			t.Fatalf("present symbol %d bits = %d, want 1..%d", i, symbols[i].bits, maxHuffmanCodeLength)
		default:
			usedCodeSpace += huffmanCodeSpace >> symbols[i].bits
		}
	}
	if usedCodeSpace != huffmanCodeSpace {
		t.Fatalf("used code space = %d, want %d", usedCodeSpace, huffmanCodeSpace)
	}
}

func TestFixedLog2ReferenceValues(t *testing.T) {
	for value := 1; value <= maxBlockSize; value++ {
		base := log2(value)
		scaled := value << 5
		scaled >>= base
		want := (base << 8) | int(fixedLog2FractionTable[scaled])
		if got := fixedLog2(value); got != want {
			t.Fatalf("fixedLog2(%d) = %d, want %d", value, got, want)
		}
	}
}

func TestPackageMergePrecodeHeaderRoundTrip(t *testing.T) {
	src := packageMergeTestCorpus()
	var hist [256]uint32
	for _, b := range src {
		hist[b]++
	}
	var symbols [256]huffmanSymbol
	packageMergeHuffmanCodegen(hist[:], symbols[:], 256, maxHuffmanCodeLength)
	createHuffmanCodes(symbols[:], 256, maxHuffmanCodeLength, huffmanCodeSpace)
	streamSizes, _ := encodeHuffmanStreams(src, &symbols)
	headerData := generateHuffmanHeaderData(&symbols, false)
	createHuffmanCodes(headerData.precodeSymbols[1:], maxHuffmanCodeLength+1, maxPrecodeCodeLength, precodeCodeSpace)
	usedPrecodeSpace := 0
	for i := 1; i <= maxHuffmanCodeLength+1; i++ {
		if bits := headerData.precodeSymbols[i].bits; bits >= 1 && bits <= maxPrecodeCodeLength {
			usedPrecodeSpace += precodeCodeSpace >> bits
		}
	}
	if usedPrecodeSpace != precodeCodeSpace {
		t.Fatalf("precode used code space = %d, want %d", usedPrecodeSpace, precodeCodeSpace)
	}
	encodedPrecodeSpace := 0
	for i := 1; i <= maxHuffmanCodeLength; i++ {
		if bits := headerData.precodeSymbols[i].bits; bits >= 1 && bits <= maxPrecodeCodeLength {
			encodedPrecodeSpace += precodeCodeSpace >> bits
		}
	}
	if encodedPrecodeSpace < precodeCodeSpace/2 {
		t.Fatalf("encoded precode space = %d, want at least %d; symbol12 bits=%d", encodedPrecodeSpace, precodeCodeSpace/2, headerData.precodeSymbols[maxHuffmanCodeLength+1].bits)
	}
	header := encodeHuffmanHeader(streamSizes, &headerData)
	if len(header) < 4 || len(header) > 127 {
		t.Fatalf("header size = %d, want encodable huffman header", len(header))
	}
}

func TestHuffmanHeaderLargeStreamSizeDeltaRoundTrip(t *testing.T) {
	src := packageMergeTestCorpus()
	var hist [256]uint32
	for _, b := range src {
		hist[b]++
	}
	var symbols [256]huffmanSymbol
	fastHuffmanCodegen(hist[:], symbols[:], len(src), 256, maxHuffmanCodeLength)
	createHuffmanCodes(symbols[:], 256, maxHuffmanCodeLength, huffmanCodeSpace)
	headerData := generateHuffmanHeaderData(&symbols, true)
	createHuffmanCodes(headerData.precodeSymbols[1:], maxHuffmanCodeLength+1, maxPrecodeCodeLength, precodeCodeSpace)

	streamSizes := [6]int{4596, 23141, 4571, 23152, 4612, 23169}
	header := encodeHuffmanHeader(streamSizes, &headerData)
	if len(header) < 4 || len(header) > 127 {
		t.Fatalf("header size = %d, want encodable huffman header", len(header))
	}
	stream := writeHeader(nil, len(src), entropyHuffman, streamLiteralsDelta)
	stream = append(stream, byte(len(header)))
	stream = append(stream, header...)
	var table [huffmanCodeSpace]huffmanEntry
	decodedStreamSizes, err := decodeHuffmanHeader(stream, 4, len(stream), &table)
	if err != nil {
		t.Fatal(err)
	}
	if decodedStreamSizes != streamSizes {
		t.Fatalf("stream sizes = %v, want %v", decodedStreamSizes, streamSizes)
	}
}

func TestHuffmanDirectTableRoundTrip(t *testing.T) {
	src := bytes.Repeat([]byte("aaaaabbbbcccdde"), 256)
	var hist [256]uint32
	for _, b := range src {
		hist[b]++
	}
	var symbols [256]huffmanSymbol
	fastHuffmanCodegen(hist[:], symbols[:], len(src), 256, maxHuffmanCodeLength)
	createHuffmanCodes(symbols[:], 256, maxHuffmanCodeLength, huffmanCodeSpace)

	var decodedSymbols [maxHuffmanCodeLength + 2][256]byte
	var decodedCounts [maxHuffmanCodeLength + 2]int
	for symbol, h := range symbols {
		if h.bits >= 1 && h.bits <= maxHuffmanCodeLength {
			decodedSymbols[h.bits][decodedCounts[h.bits]] = byte(symbol)
			decodedCounts[h.bits]++
		}
	}
	var table [huffmanCodeSpace]huffmanEntry
	if err := buildDecodeTable(&table, &decodedSymbols, &decodedCounts, maxHuffmanCodeLength); err != nil {
		t.Fatal(err)
	}
	streamSizes, streams := encodeHuffmanStreams(src, &symbols)
	combined := make([]byte, 8)
	for _, stream := range streams {
		combined = append(combined, stream.data...)
	}
	releaseEncodedHuffmanStreams(streams)
	out := make([]byte, len(src)+30)
	if err := decodeHuffmanSymbols(combined, 8, streamSizes, len(src), &table, out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out[:len(src)], src) {
		for i := range src {
			if out[i] != src[i] {
				t.Fatalf("direct huffman mismatch at %d: got %d want %d", i, out[i], src[i])
			}
		}
	}
}

func packageMergeTestCorpus() []byte {
	const targetSize = 8192
	out := make([]byte, 0, targetSize)
	for symbol := 0; symbol < 64; symbol++ {
		repeats := 1 + (symbol*symbol+17*symbol)%113
		for i := 0; i < repeats; i++ {
			out = append(out, byte(symbol*3))
		}
	}
	seed := append([]byte(nil), out[:257]...)
	for len(out) < targetSize {
		n := min(len(seed), targetSize-len(out))
		out = append(out, seed[:n]...)
	}
	return out
}
