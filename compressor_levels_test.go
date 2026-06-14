package skanda

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"slices"
	"strconv"
	"testing"
)

func TestLevelBlockRoundTrip(t *testing.T) {
	src := levelTestCorpus()
	for _, tc := range []struct {
		name  string
		level int
	}{
		{name: "level0", level: 0},
		{name: "level1", level: 1},
		{name: "level2", level: 2},
		{name: "level3", level: 3},
		{name: "level4", level: 4},
		{name: "level5", level: 5},
		{name: "level6", level: 6},
		{name: "level7", level: 7},
		{name: "level8", level: 8},
		{name: "level9", level: 9},
		{name: "level10", level: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compressed := encodeLevelBlocksForTest(t, src, tc.level, len(src)-lastBytes)
			got, err := Decompress(compressed, len(src))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, src) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}

func TestLevelTwoBlockDiffersFromLevelOne(t *testing.T) {
	src := levelTestCorpus()
	blockEnd := len(src) - lastBytes
	level1 := encodeLevelBlocksForTest(t, src, 1, blockEnd)
	level2 := encodeLevelBlocksForTest(t, src, 2, blockEnd)

	if bytes.Equal(level1, level2) {
		t.Fatal("level 2 output matched level 1")
	}
	for _, tc := range []struct {
		name       string
		compressed []byte
	}{
		{name: "level1", compressed: level1},
		{name: "level2", compressed: level2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decompress(tc.compressed, len(src))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, src) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}

func TestLazyCacheLevelsDifferFromFastPath(t *testing.T) {
	src := levelTestCorpus()
	blockEnd := len(src) - lastBytes
	level2 := encodeLevelBlocksForTest(t, src, 2, blockEnd)
	level3 := encodeLevelBlocksForTest(t, src, 3, blockEnd)
	level4 := encodeLevelBlocksForTest(t, src, 4, blockEnd)

	if bytes.Equal(level2, level3) {
		t.Fatal("level 3 output matched level 2")
	}

	for _, tc := range []struct {
		name       string
		compressed []byte
	}{
		{name: "level3", compressed: level3},
		{name: "level4", compressed: level4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decompress(tc.compressed, len(src))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, src) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}

func TestLazyLevelOptions(t *testing.T) {
	for _, tc := range []struct {
		level          int
		parser         compressorLevelParser
		hashLog        int
		hashEntriesLog int
		niceLength     int
		maxArrivals    int
	}{
		{level: 2, parser: compressorParserLazyFast, hashLog: 17, hashEntriesLog: 0},
		{level: 3, parser: compressorParserLazy, hashLog: 17, hashEntriesLog: 1},
		{level: 4, parser: compressorParserLazy, hashLog: 18, hashEntriesLog: 2},
		{level: 5, parser: compressorParserOptimal1, hashLog: 18, hashEntriesLog: 2, niceLength: 32},
		{level: 6, parser: compressorParserOptimal1, hashLog: 19, hashEntriesLog: 2, niceLength: 64},
		{level: 7, parser: compressorParserOptimal2, hashLog: 24, hashEntriesLog: 4, niceLength: 128, maxArrivals: 2},
		{level: 8, parser: compressorParserOptimal2, hashLog: 25, hashEntriesLog: 5, niceLength: 256, maxArrivals: 4},
		{level: 9, parser: compressorParserOptimal3, hashLog: 26, hashEntriesLog: 6, niceLength: 512, maxArrivals: 8},
		{level: 10, parser: compressorParserOptimal3, hashLog: 27, hashEntriesLog: 7, niceLength: 1024, maxArrivals: 16},
	} {
		opts := compressorLevelOptionsForLevel(tc.level, 1)
		if opts.parser != tc.parser {
			t.Fatalf("level %d parser = %d, want %d", tc.level, opts.parser, tc.parser)
		}
		if opts.hashLog != tc.hashLog {
			t.Fatalf("level %d hash log = %d, want %d", tc.level, opts.hashLog, tc.hashLog)
		}
		if opts.hashEntriesLog != tc.hashEntriesLog {
			t.Fatalf("level %d hash entries log = %d, want %d", tc.level, opts.hashEntriesLog, tc.hashEntriesLog)
		}
		if opts.niceLength != tc.niceLength {
			t.Fatalf("level %d nice length = %d, want %d", tc.level, opts.niceLength, tc.niceLength)
		}
		if opts.maxArrivals != tc.maxArrivals {
			t.Fatalf("level %d max arrivals = %d, want %d", tc.level, opts.maxArrivals, tc.maxArrivals)
		}
	}
}

func TestOptimalOneMatchFinderHashLogFollowsWindow(t *testing.T) {
	opts := compressorLevelOptionsForSize(6, 0.05, 1<<20)
	opts.hashLog = 19
	opts.windowLog = 20

	finder := newLevelOptimalMatchFinder(opts)
	wantBuckets := 1 << (opts.windowLog - 3)
	if got := len(finder.hash4.table) / finder.hash4.entries; got != wantBuckets {
		t.Fatalf("hash4 buckets = %d, want %d", got, wantBuckets)
	}
	if got := len(finder.hash8.table) / finder.hash8.entries; got != wantBuckets {
		t.Fatalf("hash8 buckets = %d, want %d", got, wantBuckets)
	}
}

func TestLevelMatchFinderHashLogFollowsWindow(t *testing.T) {
	opts := compressorLevelOptionsForSize(4, 0.05, 1<<20)
	opts.hashLog = 19
	opts.windowLog = 20
	wantBuckets := 1 << (opts.windowLog - 3)

	hashFinder := newLevelHashMatchFinder(opts)
	if got := len(hashFinder.table); got != wantBuckets {
		t.Fatalf("hash finder buckets = %d, want %d", got, wantBuckets)
	}

	cacheFinder := newLevelCacheMatchFinder(opts, 4, 4)
	if got := len(cacheFinder.table) / cacheFinder.entries; got != wantBuckets {
		t.Fatalf("cache finder buckets = %d, want %d", got, wantBuckets)
	}
}

func TestLevel0Hash6HelpersMatchGenericFinder(t *testing.T) {
	src := levelTestCorpus()
	src = append(src, bytes.Repeat([]byte("level0 hash helper equivalence "), 1024)...)
	opts := compressorLevelOptionsForSize(0, 0.05, len(src))

	generic := newLevelHashMatchFinder(opts)
	fast := newLevelHashMatchFinder(opts)
	defer generic.release()
	defer fast.release()

	for _, blockEnd := range []int{64, 1024, 65536, len(src) - lastBytes} {
		generic.reset()
		fast.reset()
		for pos := 0; pos < blockEnd; pos += 3 {
			genericPos, genericLength := generic.findAndUpdate(src, pos, blockEnd)
			fastPos, fastLength := fast.findAndUpdateLevel0(src, pos, blockEnd)
			if genericPos != fastPos || genericLength != fastLength {
				t.Fatalf("findAndUpdate pos=%d blockEnd=%d generic=(%d,%d) fast=(%d,%d)", pos, blockEnd, genericPos, genericLength, fastPos, fastLength)
			}
			if !slices.Equal(generic.table, fast.table) {
				t.Fatalf("findAndUpdate table mismatch at pos=%d blockEnd=%d", pos, blockEnd)
			}
		}
	}
}

func TestLevel0Hash6AddHelpersMatchGenericFinder(t *testing.T) {
	src := levelTestCorpus()
	src = append(src, bytes.Repeat([]byte("level0 add helper equivalence "), 1024)...)
	opts := compressorLevelOptionsForSize(0, 0.05, len(src))

	generic := newLevelHashMatchFinder(opts)
	fast := newLevelHashMatchFinder(opts)
	defer generic.release()
	defer fast.release()

	for _, tc := range []struct {
		pos      int
		matchLen int
		blockEnd int
	}{
		{pos: 0, matchLen: 1, blockEnd: 16},
		{pos: 1, matchLen: 2, blockEnd: 16},
		{pos: 2, matchLen: 3, blockEnd: 16},
		{pos: 32, matchLen: 6, blockEnd: 40},
		{pos: 128, matchLen: 10, blockEnd: 4096},
		{pos: maxBlockSize - 16, matchLen: 12, blockEnd: maxBlockSize - 1},
	} {
		fillLevel0FinderTable(generic.table)
		copy(fast.table, generic.table)
		generic.addAfterMatch(src, tc.pos, tc.matchLen, tc.blockEnd)
		fast.addAfterMatchLevel0(src, tc.pos, tc.matchLen, tc.blockEnd)
		if !slices.Equal(generic.table, fast.table) {
			t.Fatalf("addAfterMatch table mismatch for %+v", tc)
		}
	}

	for _, tc := range []struct {
		pos      int
		blockEnd int
	}{
		{pos: -1, blockEnd: 16},
		{pos: 0, blockEnd: 6},
		{pos: 0, blockEnd: 7},
		{pos: 3, blockEnd: 32},
		{pos: 128, blockEnd: 4096},
		{pos: maxBlockSize - 16, blockEnd: maxBlockSize - 1},
	} {
		fillLevel0FinderTable(generic.table)
		copy(fast.table, generic.table)
		generic.addRepeatPositions(src, tc.pos, tc.blockEnd)
		fast.addRepeatPositionsLevel0(src, tc.pos, tc.blockEnd)
		if !slices.Equal(generic.table, fast.table) {
			t.Fatalf("addRepeatPositions table mismatch for %+v", tc)
		}
	}
}

func fillLevel0FinderTable(table []int) {
	for i := range table {
		table[i] = i%31 - 7
	}
}

func TestLevelCacheMatchFinderStartsAtReferenceZeroPosition(t *testing.T) {
	opts := compressorLevelOptionsForSize(4, 0.05, 1<<20)
	opts.hashLog = 12
	opts.windowLog = 20
	finder := newLevelCacheMatchFinder(opts, 4, 4)
	src := []byte("0123456789abcdef")
	candidates := []int{-1}

	finder.candidatesAndUpdate(src, 4, len(src), candidates)

	if got, want := candidates[0], 0; got != want {
		t.Fatalf("first cache candidate = %d, want %d", got, want)
	}
}

func TestNonOptimalMatchFinderPersistsAcrossBlocks(t *testing.T) {
	const blockSize = 96
	phrase := []byte("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	src := make([]byte, blockSize*2+lastBytes)
	for i := range src {
		src[i] = byte(33 + (i*37)%89)
	}
	copy(src[1:], phrase)
	copy(src[blockSize:], phrase)

	for _, level := range []int{1, 2, 4} {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			opts := compressorLevelOptionsForSize(level, 0.05, len(src))
			state := newCompressState()
			_ = compressBlockLevel(src, 0, blockSize, state, opts)

			withHistoryState := *state
			noHistoryState := *state
			noHistoryState.hashFinder = nil
			noHistoryState.lazyFastFinder = nil
			noHistoryState.lazyFinder = nil

			withHistory := compressBlockLevel(src, blockSize, blockSize+len(phrase), &withHistoryState, opts)
			noHistory := compressBlockLevel(src, blockSize, blockSize+len(phrase), &noHistoryState, opts)
			if len(withHistory.data) >= len(noHistory.data) {
				t.Fatalf("second block size with finder history = %d, without history = %d", len(withHistory.data), len(noHistory.data))
			}
		})
	}
}

func TestNonOptimalBlockSizeFollowsReferenceHuffmanLimit(t *testing.T) {
	if got, want := levelMaxBlockSize(compressorLevelOptionsForLevel(1, 0.05)), maxBlockSize/2; got != want {
		t.Fatalf("level 1 Huffman block size = %d, want %d", got, want)
	}
	if got, want := levelMaxBlockSize(compressorLevelOptionsForLevel(1, 1.0)), maxBlockSize; got != want {
		t.Fatalf("level 1 no-Huffman block size = %d, want %d", got, want)
	}
	if got, want := levelMaxBlockSize(compressorLevelOptionsForLevel(5, 0.05)), maxBlockSize; got != want {
		t.Fatalf("optimal block size = %d, want %d", got, want)
	}
}

func TestHashLevelBytesUsesReferenceHashInputs(t *testing.T) {
	src := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	tableSize := 1 << 12
	hashShift := uint(64 - bits.TrailingZeros(uint(tableSize)))
	hash4Input := uint64(binary.LittleEndian.Uint32(src))
	if got, want := hashLevelBytes(src, 0, 4, hashShift), hashUint(hash4Input, tableSize); got != want {
		t.Fatalf("hash4 input mismatch: got %d, want %d", got, want)
	}
	hash3Input := uint64(binary.LittleEndian.Uint32(src)) << 40
	if got, want := hashLevelBytes(src, 0, 3, hashShift), hashUint(hash3Input, tableSize); got != want {
		t.Fatalf("hash3 input mismatch: got %d, want %d", got, want)
	}
}

func TestLevelHashFinderHashMatchesMaskedWindow(t *testing.T) {
	src := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99}
	value := binary.LittleEndian.Uint64(src)
	for hashBytes := 3; hashBytes <= 8; hashBytes++ {
		opts := compressorLevelOptionsForLevel(0, 0.05)
		opts.hashBytes = hashBytes
		finder := newLevelHashMatchFinder(opts)
		mask, leftShift := hashWindow(hashBytes)
		want := hashUintShift((value&mask)<<leftShift, finder.hashShift)
		if got := finder.hashFromValue(value); got != want {
			t.Fatalf("hash%d from value = %d, want %d", hashBytes, got, want)
		}
		if got := finder.hash(src, 0); got != want {
			t.Fatalf("hash%d from src = %d, want %d", hashBytes, got, want)
		}
		finder.release()
	}
}

func TestHighLevelOptimalParsersDiffer(t *testing.T) {
	src := levelTestCorpus()
	blockEnd := len(src) - lastBytes
	level6 := encodeLevelBlocksForTest(t, src, 6, blockEnd)
	level7 := encodeLevelBlocksForTest(t, src, 7, blockEnd)
	level10 := encodeLevelBlocksForTest(t, src, 10, blockEnd)

	if bytes.Equal(level6, level7) {
		t.Fatal("level 7 output matched level 6")
	}
	if bytes.Equal(level7, level10) {
		t.Fatal("level 10 output matched level 7")
	}
}

func TestAdvancedDistanceEncoding(t *testing.T) {
	src := levelTestCorpus()
	opts := compressorLevelOptionsForLevel(10, 0.05)
	state := newCompressState()
	encoded := compressBlockLevel(src, 0, len(src)-lastBytes, state, opts)
	compressed := append([]byte{}, encoded.data...)
	compressed = writeHeader(compressed, lastBytes, blockRaw, blockLast)
	compressed = append(compressed, src[len(src)-lastBytes:]...)
	if !hasAdvancedDistanceStream(t, compressed) {
		t.Fatal("expected advanced distance stream")
	}
	got, err := Decompress(compressed, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("round trip mismatch")
	}
}

func TestAdvancedRepeatMatchUsesOlderRepOffset(t *testing.T) {
	src := []byte("ABCDABCD")
	literals := newBlockLiterals(src, 0, len(src), 0.05)
	var tokens []byte
	var distances []byte
	var bitWriter advancedDistanceBitWriter
	var lengths []byte
	lastDistance := 7
	repOffsets := [3]int{7, 4, 9}

	next, ok := appendAdvancedRepeatMatch(src, 4, 4, len(src), repOffsets[1], 4, maxDistance, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets)
	if !ok {
		t.Fatal("appendAdvancedRepeatMatch returned false")
	}
	if next != len(src) {
		t.Fatalf("next position = %d, want %d", next, len(src))
	}
	wantToken := byte(1<<3) | byte(4-minMatchLength)
	if len(tokens) != 1 || tokens[0] != wantToken {
		t.Fatalf("tokens = %v, want second-rep length-4 token", tokens)
	}
	if repOffsets != [3]int{4, 7, 9} {
		t.Fatalf("rep offsets = %v, want [4 7 9]", repOffsets)
	}
	if len(distances) != 0 || len(bitWriter.bytes) != 0 || bitWriter.count != 0 || len(lengths) != 0 {
		t.Fatalf("unexpected side streams: distances=%v bitWriter=%v/%d lengths=%v", distances, bitWriter.bytes, bitWriter.count, lengths)
	}
}

func TestNoHuffmanMatchLengthSplitsReferenceRange(t *testing.T) {
	for _, tc := range []struct {
		length int
		want   []byte
	}{
		{length: 9, want: []byte{5, 0}},
		{length: 10, want: []byte{6, 0}},
		{length: 16, want: []byte{6, 6}},
	} {
		var tokens []byte
		var lengths []byte
		tokens = append(tokens, 0)
		appendMatchLengthWithMode(tc.length, true, &tokens, &lengths)
		if !bytes.Equal(tokens, tc.want) {
			t.Fatalf("length %d tokens = %v, want %v", tc.length, tokens, tc.want)
		}
		if len(lengths) != 0 {
			t.Fatalf("length %d side lengths = %v, want empty", tc.length, lengths)
		}
	}
}

func TestLiteralDeltaEncoding(t *testing.T) {
	src := incrementingBytes(160)
	compressed := encodeLiteralOnlyBlockForTest(t, src, 128, 0.05)
	flags := literalFlagsForFirstBlock(t, compressed)
	if flags&streamLiteralsDelta == 0 {
		t.Fatalf("literal flags = %d, want delta", flags)
	}
	got, err := Decompress(compressed, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("round trip mismatch")
	}
}

func TestLiteralPositionMaskEncoding(t *testing.T) {
	src := positionalBytes(20064)
	compressed := encodeLiteralOnlyBlockForTest(t, src, 20032, 0.5)
	flags := literalFlagsForFirstBlock(t, compressed)
	if flags&streamLiteralsPosMask3 == 0 {
		t.Fatalf("literal flags = %d, want position mask", flags)
	}
	got, err := Decompress(compressed, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("round trip mismatch")
	}
}

func TestLevelBlockStateAcrossBlocks(t *testing.T) {
	src := bytes.Repeat([]byte("0123456789abcdef-level-state-"), 384)
	for _, level := range []int{1, 6, 10} {
		t.Run("level"+strconv.Itoa(level), func(t *testing.T) {
			compressed := encodeLevelBlocksForTest(t, src, level, 4096, len(src)-lastBytes)
			got, err := Decompress(compressed, len(src))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, src) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}

func TestEstimateMemoryIncreasesForHighLevels(t *testing.T) {
	size := maxBlockSize + lastBytes
	level1 := EstimateMemory(size, 1, 0.5)
	level10 := EstimateMemory(size, 10, 0.5)
	if level10 <= level1 {
		t.Fatalf("level 10 memory = %d, want more than level 1 %d", level10, level1)
	}
}

func TestEstimateMemoryIncludesBufferedMatches(t *testing.T) {
	size := 1 << 22
	buffered := EstimateMemory(size, 7, 0.98)
	direct := EstimateMemory(size, 7, 0.99)
	if buffered <= direct {
		t.Fatalf("buffered memory = %d, want more than direct %d", buffered, direct)
	}
}

func TestEstimateMemoryIncludesOptimalBlockSplitter(t *testing.T) {
	size := 128 * 1024
	withSplitter := EstimateMemory(size, 5, 0.98)
	withoutSplitter := EstimateMemory(size, 5, 0.99)
	if withSplitter <= withoutSplitter {
		t.Fatalf("splitter memory = %d, want more than without splitter %d", withSplitter, withoutSplitter)
	}
}

func TestOptimalBlockSplitterChoosesEarlierBoundary(t *testing.T) {
	opts := compressorLevelOptionsForSize(7, 0.5, maxBlockSize+lastBytes)
	splitter := newOptimalBlockSplitter(opts)
	if splitter == nil {
		t.Fatal("expected optimal block splitter")
	}
	maxSize := splitter.subdivisionSize * 32
	src := splitFriendlyCorpus(maxSize + lastBytes)
	blockSize := splitter.getBlockSize(src, 0, 0, maxSize, opts)
	if blockSize <= 0 || blockSize > maxSize {
		t.Fatalf("block size = %d, want within 1..%d", blockSize, maxSize)
	}
	if blockSize == maxSize {
		t.Fatalf("block splitter kept max block size %d; want an earlier boundary", maxSize)
	}
	if blockSize%splitter.subdivisionSize != 0 {
		t.Fatalf("block size = %d, want subdivision multiple %d", blockSize, splitter.subdivisionSize)
	}
}

func TestOptimalBlockSplitterDisabledWhenHuffmanDisabled(t *testing.T) {
	opts := compressorLevelOptionsForSize(7, 1.0, maxBlockSize+lastBytes)
	if splitter := newOptimalBlockSplitter(opts); splitter != nil {
		t.Fatal("did not expect splitter when Huffman is disabled")
	}
}

func TestOptimalBlockSplitterProvidesInitialCostModel(t *testing.T) {
	opts := compressorLevelOptionsForSize(7, 0.5, maxBlockSize+lastBytes)
	splitter := newOptimalBlockSplitter(opts)
	if splitter == nil {
		t.Fatal("expected optimal block splitter")
	}
	maxSize := splitter.subdivisionSize * 32
	src := splitFriendlyCorpus(maxSize + lastBytes)
	_ = splitter.getBlockSize(src, 0, 0, maxSize, opts)
	model := splitter.initialCostModel()
	if model == nil || !model.enabled {
		t.Fatal("expected splitter initial cost model")
	}
}

func TestBlockSplitterLiteralModeFollowsReferenceOrder(t *testing.T) {
	opts := compressorLevelOptionsForSize(10, 0.5, 1<<16)
	var hist blockSplitHistogram
	for i := 0; i < 256; i++ {
		hist.literal[i] = 2
	}
	for i := 0; i < 4; i++ {
		hist.literal[256+i] = 128
	}
	for stream := 0; stream < 4; stream++ {
		base := 512 + stream*256
		hist.literal[base] = 64
		hist.literal[base+1] = 64
		deltaBase := 1536 + stream*256
		for i := 0; i < 4; i++ {
			hist.literal[deltaBase+i] = 32
		}
	}

	_, model := estimateBlockSplitModel(&hist, opts)
	if model.literalMode&streamLiteralsDelta == 0 {
		t.Fatalf("literal mode = %d, want reference-ordered delta base", model.literalMode)
	}
}

func TestBlockSplitterLiteralDeltaApproximationKeepsCountsComplete(t *testing.T) {
	opts := compressorLevelOptionsForSize(6, 0.5, 1<<16)
	src := incrementingBytes(512)
	var hist blockSplitHistogram
	hist.encodeLiteralRun(src, 0, 256, 1, true)

	rawCount := histogramSymbolCount(hist.literal[0:256])
	if deltaCount := histogramSymbolCount(hist.literal[256:512]); deltaCount != rawCount {
		t.Fatalf("delta literal count = %d, want %d", deltaCount, rawCount)
	}
	posDeltaCount := 0
	for stream := 0; stream < 4; stream++ {
		start := 1536 + stream*256
		posDeltaCount += histogramSymbolCount(hist.literal[start : start+256])
	}
	if posDeltaCount != rawCount {
		t.Fatalf("pos-delta literal count = %d, want %d", posDeltaCount, rawCount)
	}

	_, model := estimateBlockSplitModel(&hist, opts)
	if model.literalMode&streamLiteralsDelta == 0 {
		t.Fatalf("literal mode = %d, want delta mode", model.literalMode)
	}
}

func TestBlockSplitterApproximationUsesReferenceWindow(t *testing.T) {
	opts := compressorLevelOptionsForSize(10, 0.5, 2<<20)
	if maxMatchDistance(opts) >= 2<<20 {
		t.Fatal("test requires a compressor window smaller than the reference splitter window")
	}
	gap := maxMatchDistance(opts) + 16
	phrase := bytes.Repeat([]byte("abcdefghijklmnop"), 4)
	src := make([]byte, 0, gap+len(phrase)*2+lastBytes)
	src = append(src, phrase...)
	src = append(src, bytes.Repeat([]byte{'x'}, gap-len(phrase))...)
	blockStart := len(src)
	src = append(src, phrase...)
	src = append(src, bytes.Repeat([]byte{'?'}, lastBytes)...)

	splitter := newOptimalBlockSplitter(opts)
	var hist blockSplitHistogram
	splitter.approximateSymbolHistogram(src, 0, 0, blockStart, opts, &blockSplitHistogram{})
	splitter.approximateSymbolHistogram(src, 0, blockStart, len(phrase), opts, &hist)
	if histogramSymbolCount(hist.token[:]) == 0 {
		t.Fatal("expected splitter approximator to see the reference-window match")
	}
}

func TestBlockSplitterDictStartsAtReferenceZeroPosition(t *testing.T) {
	dict := newBlockSplitterDict(4, 3)
	src := []byte("abcdefghijklmnop")

	prev, ok := dict.candidateAndUpdate(src, 0)
	if ok || prev != 0 {
		t.Fatalf("position 0 candidate = (%d, %v), want no usable candidate", prev, ok)
	}

	prev, ok = dict.candidateAndUpdate(src, 1)
	if !ok || prev != 0 {
		t.Fatalf("position 1 candidate = (%d, %v), want reference zero position", prev, ok)
	}
}

func TestLevelMatchFindersUseDifferentPrefixes(t *testing.T) {
	src := []byte("xabcdeyabcdezabcdeq12345678901234567890123456789012")
	end := len(src)

	level0Finder := newLevelHashMatchFinder(normalizeCompressorLevelOptions(compressorLevelOptionsForLevel(0, 1)))
	level1Finder := newLevelHashMatchFinder(normalizeCompressorLevelOptions(compressorLevelOptionsForLevel(1, 1)))

	_, _ = level0Finder.findAndUpdate(src, 1, end)
	_, _ = level1Finder.findAndUpdate(src, 1, end)

	if _, gotLength := level0Finder.findAndUpdate(src, 7, end); gotLength != 0 {
		t.Fatalf("level 0 match length = %d, want 0", gotLength)
	}
	if _, gotLength := level1Finder.findAndUpdate(src, 7, end); gotLength != 5 {
		t.Fatalf("level 1 match length = %d, want 5", gotLength)
	}
}

func encodeLevelBlocksForTest(t *testing.T, src []byte, level int, blockEnds ...int) []byte {
	t.Helper()
	if len(src) <= lastBytes {
		t.Fatalf("test source too small: %d", len(src))
	}
	opts := compressorLevelOptionsForSize(level, 1, len(src))
	opts.matchState = newOptimalMatchState(len(src), opts)
	state := newCompressState()
	out := make([]byte, 0, len(src))
	blockStart := 0
	for _, blockEnd := range blockEnds {
		if blockEnd <= blockStart || blockEnd > len(src)-lastBytes {
			t.Fatalf("invalid block end %d after %d", blockEnd, blockStart)
		}
		encoded := compressBlockLevel(src, blockStart, blockEnd, state, opts)
		if encoded.lastDistance != state.lastDistance {
			t.Fatalf("state distance = %d, encoded distance = %d", state.lastDistance, encoded.lastDistance)
		}
		out = append(out, encoded.data...)
		blockStart = blockEnd
	}
	out = writeHeader(out, len(src)-blockStart, blockRaw, blockLast)
	out = append(out, src[blockStart:]...)
	return out
}

func TestWindowLogMatchesReferenceTable(t *testing.T) {
	for _, tc := range []struct {
		name         string
		size         int
		decSpeedBias float64
		windowLog    int
	}{
		{name: "small", size: 33, decSpeedBias: 0.5, windowLog: 6},
		{name: "fastLarge", size: 1 << 25, decSpeedBias: 1, windowLog: 20},
		{name: "balancedLarge", size: 1 << 25, decSpeedBias: 0.5, windowLog: 22},
		{name: "ratioLarge", size: 1 << 25, decSpeedBias: 0, windowLog: 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := skandaWindowLog(tc.size, tc.decSpeedBias); got != tc.windowLog {
				t.Fatalf("window log = %d, want %d", got, tc.windowLog)
			}
		})
	}
}

func TestWindowLogLimitsMatchDistance(t *testing.T) {
	fast := compressorLevelOptionsForSize(10, 1, 1<<22)
	if got, want := maxMatchDistance(fast), (1<<20)-1; got != want {
		t.Fatalf("fast max distance = %d, want %d", got, want)
	}

	ratio := compressorLevelOptionsForSize(10, 0, 1<<26)
	if got, want := maxMatchDistance(ratio), (1<<26)-1; got != want {
		t.Fatalf("ratio max distance = %d, want %d", got, want)
	}
}

func TestMatchLengthHonorsWindowDistance(t *testing.T) {
	distance := 1 << 20
	src := bytes.Repeat([]byte{'x'}, distance+16)
	pos := distance + 4
	prev := 4
	if got := matchLengthAtWindow(src, pos, prev, len(src), 4, distance-1); got != 0 {
		t.Fatalf("match length outside window = %d, want 0", got)
	}
	if got := matchLengthAtWindow(src, pos, prev, len(src), 4, distance); got == 0 {
		t.Fatal("expected match when distance is within supplied limit")
	}
}

func TestBinaryMatchFinderFindsIncreasingMatches(t *testing.T) {
	src := []byte("xabcdefghijklmnop----abcdefghijklmnop")
	second := bytes.LastIndex(src, []byte("abcdefghijklmnop"))
	if second <= 0 {
		t.Fatal("test corpus missing second phrase")
	}
	opts := normalizeCompressorLevelOptions(compressorLevelOptionsForSize(7, 0.5, len(src)))
	finder := newBinaryMatchFinder(0, len(src), opts)
	for pos := 1; pos < second; pos++ {
		finder.updatePosition(src, pos, 0, len(src), opts)
	}

	matches := finder.findLZMatchesAndUpdate(src, second, 0, len(src), len(src), 3, opts, nil)
	if len(matches) == 0 {
		t.Fatal("expected binary finder match")
	}
	lastLength := 0
	for _, match := range matches {
		if match.length < lastLength {
			t.Fatalf("matches not increasing: %v", matches)
		}
		lastLength = match.length
	}
	best := matches[len(matches)-1]
	if best.length < len("abcdefghijklmnop") || best.distance != second-1 {
		t.Fatalf("best match = %+v, want length >= 16 distance %d", best, second-1)
	}
}

func TestBinaryMatchFinderClipsToBlockLimit(t *testing.T) {
	src := []byte("xabcdefghijklmnop----abcdefghijklmnop")
	second := bytes.LastIndex(src, []byte("abcdefghijklmnop"))
	opts := normalizeCompressorLevelOptions(compressorLevelOptionsForSize(7, 0.5, len(src)))
	finder := newBinaryMatchFinder(0, len(src), opts)
	for pos := 1; pos < second; pos++ {
		finder.updatePosition(src, pos, 0, len(src), opts)
	}

	blockLimit := second + 8
	matches := finder.findLZMatchesAndUpdate(src, second, 0, len(src), blockLimit, 3, opts, nil)
	if len(matches) == 0 {
		t.Fatal("expected clipped binary finder match")
	}
	if best := matches[len(matches)-1]; best.length != 8 {
		t.Fatalf("clipped length = %d, want 8", best.length)
	}
}

func TestBinaryMatchFinderHonorsWindowLog(t *testing.T) {
	src := []byte("xabcdefghijklmnop----abcdefghijklmnop")
	second := bytes.LastIndex(src, []byte("abcdefghijklmnop"))
	opts := normalizeCompressorLevelOptions(compressorLevelOptionsForSize(7, 0.5, len(src)))
	opts.windowLog = 4
	finder := newBinaryMatchFinder(0, len(src), opts)
	for pos := 1; pos < second; pos++ {
		finder.updatePosition(src, pos, 0, len(src), opts)
	}

	matches := finder.findLZMatchesAndUpdate(src, second, 0, len(src), len(src), 3, opts, nil)
	if len(matches) != 0 {
		t.Fatalf("matches outside window: %v", matches)
	}
}

func TestUseBufferedMatches(t *testing.T) {
	if !useBufferedMatches(compressorLevelOptionsForSize(7, 0.5, 1<<16)) {
		t.Fatal("expected buffered matches for OPTIMAL2 with Huffman-enabled bias")
	}
	if useBufferedMatches(compressorLevelOptionsForSize(7, 1.0, 1<<16)) {
		t.Fatal("did not expect buffered matches for no-Huffman bias")
	}
	if useBufferedMatches(compressorLevelOptionsForSize(6, 0.5, 1<<16)) {
		t.Fatal("did not expect buffered matches for OPTIMAL1")
	}
}

func TestMatchBufferUsesBlockLocalPositions(t *testing.T) {
	src := []byte("prefix-abcdefghijklmnop----abcdefghijklmnop")
	blockStart := len("prefix-")
	second := bytes.LastIndex(src, []byte("abcdefghijklmnop"))
	opts := normalizeCompressorLevelOptions(compressorLevelOptionsForSize(7, 0.5, len(src)))
	finder := newBinaryMatchFinder(blockStart, len(src), opts)
	buffer := newMatchBuffer(src, blockStart, len(src), len(src), opts, finder)

	matches := buffer.findLZMatchesAndUpdate(src, second, 0, len(src), len(src), 3, opts, nil)
	if len(matches) == 0 {
		t.Fatal("expected buffered match at block-local position")
	}
	if best := matches[len(matches)-1]; best.length < len("abcdefghijklmnop") || best.distance != second-blockStart {
		t.Fatalf("best buffered match = %+v, want length >= 16 distance %d", best, second-blockStart)
	}
}

func TestMatchBufferFillsAfterNiceLengthMatch(t *testing.T) {
	src := []byte("xabcdefghijklmnop----abcdefghijklmnop")
	second := bytes.LastIndex(src, []byte("abcdefghijklmnop"))
	opts := normalizeCompressorLevelOptions(compressorLevelOptionsForSize(7, 0.5, len(src)))
	opts.niceLength = 8
	finder := newBinaryMatchFinder(0, len(src), opts)
	buffer := newMatchBuffer(src, 0, len(src), len(src), opts, finder)

	matches := buffer.findLZMatchesAndUpdate(src, second+1, 0, len(src), len(src), 3, opts, nil)
	if len(matches) == 0 {
		t.Fatal("expected filled match after nice-length hit")
	}
	if got, want := matches[len(matches)-1].length, len("abcdefghijklmnop")-1; got != want {
		t.Fatalf("filled match length = %d, want %d", got, want)
	}
}

type recordingMatchSource struct {
	calls     int
	positions []int
	minLength []int
	matches   map[int][]lzMatch
}

func (s *recordingMatchSource) findLZMatchesAndUpdate(_ []byte, pos, _, _, _, minLength int, _ compressorLevelOptions, dst []lzMatch) []lzMatch {
	s.calls++
	s.positions = append(s.positions, pos)
	s.minLength = append(s.minLength, minLength)
	for _, match := range s.matches[pos] {
		if match.length >= minLength {
			dst = append(dst, match)
		}
	}
	return dst
}

func TestParseOptimalBlockUsesPrecomputedMatchSource(t *testing.T) {
	src := append(levelTestCorpus(), bytes.Repeat([]byte{'?'}, lastBytes)...)
	blockEnd := len(src) - lastBytes
	opts := compressorLevelOptionsForSize(7, 0.5, len(src))
	source := &recordingMatchSource{}

	_ = parseOptimalBlockWithPrecomputedSource(src, 0, blockEnd, [3]int{1, 1, 1}, false, opts, nil, false, source)

	if source.calls == 0 {
		t.Fatal("expected parser to read the precomputed match source")
	}
	for _, pos := range source.positions {
		if pos < 1 || pos >= blockEnd {
			t.Fatalf("precomputed source queried outside block: %d", pos)
		}
	}
}

func TestMultiArrivalUsesRepLengthAsNormalMatchFloor(t *testing.T) {
	src := append([]byte("abcdefabcdef"), bytes.Repeat([]byte{'?'}, lastBytes)...)
	opts := compressorLevelOptionsForSize(7, 0.5, len(src))
	opts.maxArrivals = 4
	source := &recordingMatchSource{
		matches: map[int][]lzMatch{
			6: {{length: 5, distance: 9}, {length: 7, distance: 8}},
		},
	}

	_ = multiArrivalOptimalParse(src, 6, 12, 12, source, [3]int{6, 1, 1}, false, opts, nil, false)

	for i, pos := range source.positions {
		if pos == 6 {
			if source.minLength[i] != 6 {
				t.Fatalf("normal match minLength at rep position = %d, want 6", source.minLength[i])
			}
			return
		}
	}
	t.Fatal("expected match source query at rep position")
}

func TestMultiArrivalReferenceLengthWindows(t *testing.T) {
	const maxArrivals = 2
	const maxInt = int(^uint(0) >> 1)
	states := make([]optimalParseState, 12*maxArrivals)
	for i := range states {
		states[i].cost = maxInt
	}
	current := optimalParseState{repOffsets: [3]int{4, 8, 16}}
	relaxMultiArrivalRepMatches(states, maxArrivals, 0, 0, 10, 6, 4, current, false, nil, false)
	for length := 6; length <= 10; length++ {
		if states[length*maxArrivals].cost == maxInt {
			t.Fatalf("rep length %d was not relaxed", length)
		}
	}
	if states[5*maxArrivals].cost != maxInt {
		t.Fatal("rep length below nextExpectedLength was relaxed")
	}

	for i := range states {
		states[i].cost = maxInt
	}
	relaxMultiArrivalNormalMatches(states, maxArrivals, 0, 0, 10, 6, 5, current, false, nil, false)
	for length := 7; length <= 10; length++ {
		if states[length*maxArrivals].cost == maxInt {
			t.Fatalf("normal length %d was not relaxed", length)
		}
	}
	if states[6*maxArrivals].cost != maxInt {
		t.Fatal("normal length at previous match length was relaxed")
	}
}

func TestOptimalParseChunkSizeMatchesReferenceFamilies(t *testing.T) {
	optimal1 := compressorLevelOptionsForSize(5, 0.5, 1<<16)
	optimal1.optimalBlockSize = 4096
	if got := optimalParseChunkSize(optimal1); got != 4096 {
		t.Fatalf("OPTIMAL1 chunk size = %d, want 4096", got)
	}

	optimal2 := compressorLevelOptionsForSize(7, 0.5, 1<<16)
	optimal2.optimalBlockSize = 4096
	if got := optimalParseChunkSize(optimal2); got != 4095 {
		t.Fatalf("OPTIMAL2 chunk size = %d, want 4095", got)
	}
}

func TestOptimalParseAllowsMatchPastChunkEnd(t *testing.T) {
	main := []byte("xabcdefabcdef")
	src := append(append([]byte{}, main...), bytes.Repeat([]byte{'?'}, lastBytes)...)
	opts := compressorLevelOptionsForSize(5, 0.5, len(src))
	finder := newLevelOptimalMatchFinder(opts)

	steps := optimalParse(src, 1, 10, len(main), &finder, [3]int{1, 1, 1}, false, opts, nil, false)
	for _, step := range steps {
		if step.pos == 7 && step.length == 6 {
			return
		}
	}
	t.Fatalf("expected match crossing chunk end, got %+v", steps)
}

func TestOptimalParseTerminalNormalMatchConsumesMatchEnd(t *testing.T) {
	main := []byte("xabcdefghYYabcdefghQQQQ")
	src := append(append([]byte{}, main...), bytes.Repeat([]byte{'?'}, lastBytes)...)
	opts := compressorLevelOptionsForSize(5, 0.5, len(src))
	opts.niceLength = 8
	finder := newLevelOptimalMatchFinder(opts)

	result := optimalParseDetailed(src, 1, len(main)-2, len(main), &finder, [3]int{1, 1, 1}, false, opts, nil, false)
	wantConsumed := 19
	if result.consumed != wantConsumed {
		t.Fatalf("consumed = %d, want %d; steps %+v", result.consumed, wantConsumed, result.steps)
	}
	for _, step := range result.steps {
		if step.pos == 11 && step.length == 8 {
			return
		}
	}
	t.Fatalf("expected terminal match at 11 length 8, got %+v", result.steps)
}

func TestOptimalFinderHonorsLastLength(t *testing.T) {
	src := []byte("xabcdefghYYabcdefghZZZZZZZZ")
	opts := compressorLevelOptionsForSize(5, 0.5, len(src))

	finder := newLevelOptimalMatchFinder(opts)
	_ = finder.findMatchesAndUpdate(src, 1, len(src), 1)
	matches := finder.findMatchesAndUpdate(src, 11, len(src), 7)
	if len(matches) == 0 || matches[0].length != 8 {
		t.Fatalf("last-length 7 matches = %+v, want first length 8", matches)
	}

	finder = newLevelOptimalMatchFinder(opts)
	_ = finder.findMatchesAndUpdate(src, 1, len(src), 1)
	matches = finder.findMatchesAndUpdate(src, 11, len(src), 8)
	if len(matches) != 0 {
		t.Fatalf("last-length 8 matches = %+v, want none", matches)
	}
}

func TestOptimalFinderChecksCacheBucketsInReferenceLockstep(t *testing.T) {
	src := bytes.Repeat([]byte{'x'}, 80)
	opts := compressorLevelOptionsForSize(5, 0.5, len(src))
	finder := newLevelOptimalMatchFinder(opts)
	pos := 32
	prev4 := 4
	prev8 := 8
	finder.hash4.table[finder.hash4.hash(src, pos)*finder.hash4.entries] = prev4
	finder.hash8.table[finder.hash8.hash(src, pos)*finder.hash8.entries] = prev8

	matches := finder.findMatchesAndUpdate(src, pos, len(src), 4)
	if len(matches) != 1 || matches[0].pos != prev8 {
		t.Fatalf("matches = %+v, want single hash8-prioritized match at %d", matches, prev8)
	}
}

func TestOptimalFinderUpdatesHash3WhenLastLengthSuppressesSearch(t *testing.T) {
	src := []byte("abcxabcYabcZZZZZZZZ")
	opts := compressorLevelOptionsForSize(5, 0.5, len(src))
	finder := newLevelOptimalMatchFinder(opts)

	_ = finder.findMatchesAndUpdate(src, 0, len(src), 1)
	if matches := finder.findMatchesAndUpdate(src, 4, len(src), 3); len(matches) != 0 {
		t.Fatalf("last-length 3 matches = %+v, want none", matches)
	}

	matches := finder.findMatchesAndUpdate(src, 8, len(src), 2)
	if len(matches) == 0 || matches[0].pos != 4 || matches[0].length != 3 {
		t.Fatalf("hash3 update matches = %+v, want pos 4 length 3", matches)
	}
}

func TestMultiArrivalAllowsMatchPastChunkEnd(t *testing.T) {
	src := append([]byte("abcdefghij"), bytes.Repeat([]byte{'?'}, lastBytes)...)
	opts := compressorLevelOptionsForSize(7, 0.5, len(src))
	opts.maxArrivals = 2
	opts.niceLength = 16
	source := &recordingMatchSource{
		matches: map[int][]lzMatch{
			4: {{length: 6, distance: 3}},
		},
	}
	model := newRawOptimalCostModel()
	model.enabled = true
	for i := 0; i < 256; i++ {
		model.literal[0][i] = 100
		model.token[i] = 0
		model.distance[i] = 0
		model.length[i] = 0
	}

	steps := multiArrivalOptimalParse(src, 1, 5, 10, source, [3]int{1, 1, 1}, false, opts, &model, false)
	for _, step := range steps {
		if step.pos == 4 && step.length == 6 {
			return
		}
	}
	t.Fatalf("expected multi-arrival match crossing chunk end, got %+v", steps)
}

func TestMultiArrivalTerminalNormalMatchConsumesMatchEnd(t *testing.T) {
	src := append([]byte("abcdefghijQQQQ"), bytes.Repeat([]byte{'?'}, lastBytes)...)
	opts := compressorLevelOptionsForSize(7, 0.5, len(src))
	opts.maxArrivals = 2
	opts.niceLength = 5
	source := &recordingMatchSource{
		matches: map[int][]lzMatch{
			4: {{length: 5, distance: 3}},
		},
	}

	result := multiArrivalOptimalParseDetailed(src, 1, 8, 10, source, [3]int{1, 1, 1}, false, opts, nil, false)
	if result.consumed != 9 {
		t.Fatalf("consumed = %d, want 9; steps %+v", result.consumed, result.steps)
	}
	for _, step := range result.steps {
		if step.pos == 4 && step.length == 5 && step.distance == 3 {
			return
		}
	}
	t.Fatalf("expected terminal multi-arrival match, got %+v", result.steps)
}

func TestMultiArrivalCostPruningMatchesReferenceThreshold(t *testing.T) {
	if !multiArrivalCostPruned(10*optimalCostScale, optimalCostScale, 2*optimalCostScale) {
		t.Fatal("expected high-cost arrival to be pruned")
	}
	if !multiArrivalCostPruned(3*optimalCostScale, optimalCostScale, 2*optimalCostScale) {
		t.Fatal("expected equal-threshold cost to be pruned")
	}
	if multiArrivalCostPruned(3*optimalCostScale-1, optimalCostScale, 2*optimalCostScale) {
		t.Fatal("did not expect below-threshold cost to be pruned")
	}
	const maxInt = int(^uint(0) >> 1)
	if multiArrivalCostPruned(10*optimalCostScale, maxInt, 2*optimalCostScale) {
		t.Fatal("did not expect pruning against an unreachable next position")
	}
}

func TestExtendOptimalMatchLeftStopsAtLiteralRunAndInputStart(t *testing.T) {
	src := []byte("zabczabc")
	pos, length := extendOptimalMatchLeft(src, 0, 5, 4, 3, 2)
	if pos != 4 || length != 4 {
		t.Fatalf("extended match = pos:%d length:%d, want pos:4 length:4", pos, length)
	}

	pos, length = extendOptimalMatchLeft(src, 0, 5, 4, 3, 0)
	if pos != 5 || length != 3 {
		t.Fatalf("no-literal-run extension = pos:%d length:%d, want pos:5 length:3", pos, length)
	}

	pos, length = extendOptimalMatchLeft(src, 0, 4, 4, 4, 4)
	if pos != 4 || length != 4 {
		t.Fatalf("input-start extension = pos:%d length:%d, want pos:4 length:4", pos, length)
	}
}

func TestOptimalCostModelUsesHuffmanBits(t *testing.T) {
	data := append(bytes.Repeat([]byte{'a'}, 220), bytes.Repeat([]byte{'z'}, 12)...)
	costs := estimateHuffmanCosts(data, 0.5)
	if costs['a'] == 8 {
		t.Fatalf("frequent symbol cost = %d, want Huffman-derived cost", costs['a'])
	}
	if costs['a'] >= costs[0] {
		t.Fatalf("frequent symbol cost = %d, absent symbol cost = %d; want frequent lower", costs['a'], costs[0])
	}
	if costs[0] != maxHuffmanCodeLength+1 {
		t.Fatalf("absent symbol cost = %d, want unseen Huffman cost", costs[0])
	}
	rawCosts := estimateHuffmanCosts(data, 1.0)
	if rawCosts['a'] != 8 || rawCosts['z'] != 8 {
		t.Fatalf("raw costs = a:%d z:%d, want 8-bit costs", rawCosts['a'], rawCosts['z'])
	}
	if rawCosts[0] != 8 {
		t.Fatalf("raw absent symbol cost = %d, want 8-bit cost", rawCosts[0])
	}

	rleCosts := estimateHuffmanCosts(bytes.Repeat([]byte{'q'}, 64), 0.5)
	if rleCosts['q'] != 0 {
		t.Fatalf("RLE symbol cost = %d, want 0", rleCosts['q'])
	}
	if rleCosts['r'] != maxHuffmanCodeLength+1 {
		t.Fatalf("RLE absent symbol cost = %d, want unseen Huffman cost", rleCosts['r'])
	}
}

func TestEstimateEntropyFromHistogramUsesReferenceAbsentCosts(t *testing.T) {
	var hist [256]uint32
	hist['x'] = 64
	estimate := estimateEntropyFromHistogram(hist[:], 0.5)
	if estimate.costs['x'] != 0 {
		t.Fatalf("RLE symbol cost = %d, want 0", estimate.costs['x'])
	}
	if estimate.costs['y'] != maxHuffmanCodeLength+1 {
		t.Fatalf("RLE absent symbol cost = %d, want unseen Huffman cost", estimate.costs['y'])
	}

	hist['y'] = 64
	estimate = estimateEntropyFromHistogram(hist[:], 0.5)
	if estimate.uncompressed {
		t.Fatal("expected Huffman estimate")
	}
	if estimate.costs['z'] != maxHuffmanCodeLength+1 {
		t.Fatalf("Huffman absent symbol cost = %d, want unseen Huffman cost", estimate.costs['z'])
	}
}

func TestEstimateEntropyFromHistogramMatchesReferenceSizes(t *testing.T) {
	cases := []struct {
		name         string
		data         []byte
		bias         float64
		wantSize     int
		wantCosts    map[byte]int
		uncompressed bool
	}{
		{
			name:      "empty",
			data:      nil,
			bias:      0.5,
			wantSize:  3,
			wantCosts: map[byte]int{0: 0, 'a': maxHuffmanCodeLength + 1},
		},
		{
			name:      "rle",
			data:      bytes.Repeat([]byte{'x'}, 64),
			bias:      0.5,
			wantSize:  67,
			wantCosts: map[byte]int{'x': 0, 'z': maxHuffmanCodeLength + 1},
		},
		{
			name:     "small-huffman",
			data:     append(append(bytes.Repeat([]byte{'a'}, 128), bytes.Repeat([]byte{'b'}, 64)...), bytes.Repeat([]byte{'z'}, 16)...),
			bias:     0.5,
			wantSize: 55,
			wantCosts: map[byte]int{
				'a': 1,
				'b': 2,
				'z': 2,
				0:   maxHuffmanCodeLength + 1,
			},
		},
		{
			name:         "raw-bias",
			data:         append(append(bytes.Repeat([]byte{'a'}, 128), bytes.Repeat([]byte{'b'}, 64)...), bytes.Repeat([]byte{'z'}, 16)...),
			bias:         1.0,
			wantSize:     211,
			wantCosts:    map[byte]int{'a': 8, 'b': 8, 'z': 8, 0: 8},
			uncompressed: true,
		},
		{
			name:     "repeat-huffman",
			data:     bytes.Repeat([]byte("aaaaabbbbcccdde"), 256),
			bias:     0.5,
			wantSize: 1077,
			wantCosts: map[byte]int{
				'a': 2,
				'b': 2,
				0:   maxHuffmanCodeLength + 1,
				'z': maxHuffmanCodeLength + 1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hist [256]uint32
			for _, b := range tc.data {
				hist[b]++
			}
			estimate := estimateEntropyFromHistogram(hist[:], tc.bias)
			if estimate.size != tc.wantSize {
				t.Fatalf("size = %d, want %d", estimate.size, tc.wantSize)
			}
			if estimate.uncompressed != tc.uncompressed {
				t.Fatalf("uncompressed = %v, want %v", estimate.uncompressed, tc.uncompressed)
			}
			for symbol, want := range tc.wantCosts {
				if estimate.costs[symbol] != want {
					t.Fatalf("cost[%d] = %d, want %d", symbol, estimate.costs[symbol], want)
				}
			}
		})
	}
}

func TestEstimateHuffmanCostsUsesReferenceBiasFallback(t *testing.T) {
	data := make([]byte, 128)
	for i := range data {
		data[i] = byte(i)
	}
	costs := estimateHuffmanCosts(data, 0.5)
	for i := range data {
		if costs[data[i]] != 8 {
			t.Fatalf("cost[%d] = %d, want raw fallback cost 8", data[i], costs[data[i]])
		}
	}
}

func TestEstimateHuffmanCostsMatchesHistogramEstimate(t *testing.T) {
	data := append(append(bytes.Repeat([]byte{'a'}, 128), bytes.Repeat([]byte{'b'}, 64)...), bytes.Repeat([]byte{'z'}, 16)...)
	var hist [256]uint32
	for _, b := range data {
		hist[b]++
	}
	estimate := estimateEntropyFromHistogram(hist[:], 0.5)
	costs := estimateHuffmanCosts(data, 0.5)
	if costs != estimate.costs {
		t.Fatalf("costs differ from histogram estimate")
	}
}

func TestOptimalLiteralCostUsesLiteralMode(t *testing.T) {
	src := []byte("abxx")
	repOffsets := [3]int{1, 1, 1}
	model := newRawOptimalCostModel()
	model.enabled = true
	model.literalMode = streamLiteralsDelta
	model.literal[0][int(byte('b')-byte('a'))] = 1
	if got := optimalLiteralCost(src, 1, 0, repOffsets, &model, false); got != optimalCostScale {
		t.Fatalf("delta literal cost = %d, want %d", got, optimalCostScale)
	}

	model = newRawOptimalCostModel()
	model.enabled = true
	model.literalMode = streamLiteralsPosMask3
	model.literal[1]['b'] = 2
	if got := optimalLiteralCost(src, 1, 0, repOffsets, &model, false); got != 2*optimalCostScale {
		t.Fatalf("pos-mask literal cost = %d, want %d", got, 2*optimalCostScale)
	}
}

func TestNoHuffmanParserCostsMatchRawStreamShape(t *testing.T) {
	src := []byte("abcdefghijk")
	repOffsets := [3]int{1, 4, 8}
	if got := optimalLiteralCost(src, 1, 5, repOffsets, nil, true); got != 8*optimalCostScale {
		t.Fatalf("literal run 5 no-Huffman cost = %d, want %d", got, 8*optimalCostScale)
	}
	if got := optimalLiteralCost(src, 1, 6, repOffsets, nil, true); got != 16*optimalCostScale+2 {
		t.Fatalf("literal run 6 no-Huffman cost = %d, want %d", got, 16*optimalCostScale+2)
	}
	if got := noHuffmanStoredMatchLength(12, true); got != 8 {
		t.Fatalf("stored no-Huffman length = %d, want 8", got)
	}
	if got := noHuffmanMatchCost(8, repOffsets[0], repOffsets, false); got != 8*optimalCostScale+1 {
		t.Fatalf("rep no-Huffman cost = %d, want %d", got, 8*optimalCostScale+1)
	}
	if got := noHuffmanMatchCost(17, 255, repOffsets, false); got != 24*optimalCostScale+3 {
		t.Fatalf("standard no-Huffman cost = %d, want %d", got, 24*optimalCostScale+3)
	}
	if got := noHuffmanMatchCost(17, 32, repOffsets, true); got != 26*optimalCostScale+3 {
		t.Fatalf("advanced no-Huffman cost = %d, want %d", got, 26*optimalCostScale+3)
	}
}

func TestRepMatchCostOmitsLongDistanceSpeedPenalty(t *testing.T) {
	repOffsets := [3]int{1 << 17, 4, 8}
	normal := optimalMatchCostWithDistancePenalty(8, repOffsets[0], 0, repOffsets, false, nil, false, true)
	rep := optimalMatchCostWithDistancePenalty(8, repOffsets[0], 0, repOffsets, false, nil, false, false)
	if normal-rep != 1 {
		t.Fatalf("long-distance rep penalty delta = %d, want 1", normal-rep)
	}

	normalRaw := noHuffmanMatchCostWithDistancePenalty(8, repOffsets[0], repOffsets, false, true)
	repRaw := noHuffmanMatchCostWithDistancePenalty(8, repOffsets[0], repOffsets, false, false)
	if normalRaw-repRaw != 1 {
		t.Fatalf("long-distance no-Huffman rep penalty delta = %d, want 1", normalRaw-repRaw)
	}
}

func TestNoHuffmanRelaxStoresShortenedMatchLength(t *testing.T) {
	const maxInt = int(^uint(0) >> 1)
	current := optimalParseState{repOffsets: [3]int{1, 4, 8}}

	states := make([]optimalParseState, 20)
	for i := range states {
		states[i].cost = maxInt
	}
	relaxOptimalMatchWithDistancePenalty(states, 0, 12, 5, current, false, nil, true, true)
	if states[8].matchLen != 8 {
		t.Fatalf("forward no-Huffman matchLen at 8 = %d, want 8", states[8].matchLen)
	}
	if states[12].matchLen != 0 {
		t.Fatalf("forward no-Huffman matchLen at 12 = %d, want 0", states[12].matchLen)
	}

	const maxArrivals = 2
	arrivals := make([]optimalParseState, 20*maxArrivals)
	for i := range arrivals {
		arrivals[i].cost = maxInt
	}
	relaxMultiArrivalMatch(arrivals, maxArrivals, 0, 0, 12, 5, current, false, nil, true)
	if arrivals[8*maxArrivals].matchLen != 8 {
		t.Fatalf("multi-arrival no-Huffman matchLen at 8 = %d, want 8", arrivals[8*maxArrivals].matchLen)
	}
	if arrivals[12*maxArrivals].matchLen != 0 {
		t.Fatalf("multi-arrival no-Huffman matchLen at 12 = %d, want 0", arrivals[12*maxArrivals].matchLen)
	}
}

func TestOptimalOneRelaxesOnlyReferenceLength(t *testing.T) {
	const maxInt = int(^uint(0) >> 1)
	current := optimalParseState{repOffsets: [3]int{4, 8, 16}}

	states := make([]optimalParseState, 16)
	for i := range states {
		states[i].cost = maxInt
	}

	if !relaxOptimalMatchWithDistancePenalty(states, 0, 10, 4, current, false, nil, false, false) {
		t.Fatal("relaxOptimalMatchWithDistancePenalty returned false")
	}
	for i := 1; i < 10; i++ {
		if states[i].cost != maxInt {
			t.Fatalf("state %d cost = %d, want untouched", i, states[i].cost)
		}
	}
	if states[10].matchLen != 10 || states[10].distance != 4 {
		t.Fatalf("state 10 match = len %d distance %d, want len 10 distance 4", states[10].matchLen, states[10].distance)
	}
}

func TestOptimalMatchStatePersistsAcrossBlocks(t *testing.T) {
	phrase := []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	firstBlock := append([]byte("!"), phrase...)
	src := make([]byte, 0, len(firstBlock)+len(phrase)+lastBytes)
	src = append(src, firstBlock...)
	secondStart := len(src)
	src = append(src, phrase...)
	src = append(src, bytes.Repeat([]byte{'?'}, lastBytes)...)

	opts := compressorLevelOptionsForSize(7, 0.5, len(src))
	opts.matchState = newOptimalMatchState(len(src), opts)
	repOffsets := [3]int{1, 1, 1}
	_ = parseOptimalBlock(src, 0, secondStart, repOffsets, false, opts, nil, false)
	steps := parseOptimalBlock(src, secondStart, secondStart+len(phrase), repOffsets, false, opts, nil, false)

	for _, step := range steps {
		if step.distance >= len(phrase) && step.length >= len(phrase)/2 {
			return
		}
	}
	t.Fatalf("expected cross-block match, got steps: %+v", steps)
}

func TestBuildOptimalCostModelRespectsBias(t *testing.T) {
	src := levelTestCorpus()
	blockEnd := len(src) - lastBytes
	opts := compressorLevelOptionsForSize(10, 0.5, len(src))
	repOffsets := [3]int{1, 1, 1}
	steps := parseOptimalBlock(src, 0, blockEnd, repOffsets, false, opts, nil, false)
	model := buildOptimalCostModel(src, 0, blockEnd, steps, repOffsets, false, opts, optimalEntropyPlan{}, nil)
	if !model.enabled {
		t.Fatal("expected Huffman-enabled cost model")
	}

	opts = compressorLevelOptionsForSize(10, 1.0, len(src))
	steps = parseOptimalBlock(src, 0, blockEnd, repOffsets, false, opts, nil, true)
	model = buildOptimalCostModel(src, 0, blockEnd, steps, repOffsets, false, opts, optimalEntropyPlan{}, nil)
	if model.enabled {
		t.Fatal("did not expect cost model when Huffman is disabled")
	}
}

func TestOptimalEntropyPlanUsesSeedStreamModes(t *testing.T) {
	opts := compressorLevelOptionsForSize(10, 0.5, 1<<16)
	model := newRawOptimalCostModel()
	model.enabled = true
	model.literalMode = streamLiteralsPosMask3
	model.literal[0][0] = 1
	model.literal[2][2] = 1
	model.token[7] = 1
	model.length[9] = 1

	plan := optimalEntropyPlanFromModel(opts, &model)
	if !plan.enabled {
		t.Fatal("expected forced entropy plan")
	}
	if plan.literalMode != streamLiteralsPosMask3 {
		t.Fatalf("literal mode = %d, want pos-mask", plan.literalMode)
	}
	if plan.literal[0] != 0 || plan.literal[1] != 1 || plan.literal[2] != 0 || plan.literal[3] != 1 {
		t.Fatalf("literal biases = %v, want huffman/raw/huffman/raw", plan.literal)
	}
	if plan.token != 0 || plan.distance != 1 || plan.length != 0 {
		t.Fatalf("stream biases token=%v distance=%v length=%v, want 0/1/0", plan.token, plan.distance, plan.length)
	}
}

func TestEncodeOptimalLiteralsUsesModelModeAndPlan(t *testing.T) {
	opts := compressorLevelOptionsForSize(10, 0.5, 1<<16)
	literals := blockLiterals{deltaOK: true, collectAdvanced: true}
	literals.raw = bytes.Repeat([]byte{'A', 'B'}, 40)
	literals.delta = bytes.Repeat([]byte{0}, len(literals.raw))

	model := newRawOptimalCostModel()
	model.enabled = true
	model.literalMode = streamLiteralsDelta
	plan := optimalEntropyPlan{enabled: true, literal: [4]float64{0, 0.5, 0.5, 0.5}}

	encoded := encodeOptimalLiterals(literals, opts, model, plan, true)
	cpos := 0
	_, typ, flags, err := readHeader(encoded, &cpos)
	if err != nil {
		t.Fatal(err)
	}
	if flags&streamLiteralsDelta == 0 {
		t.Fatalf("literal flags = %d, want delta flag", flags)
	}
	if typ != entropyRLE && typ != entropyHuffman {
		t.Fatalf("literal entropy type = %d, want compressed delta stream", typ)
	}
}

func TestForcedLiteralCostsPreservePosMaskChoice(t *testing.T) {
	opts := compressorLevelOptionsForSize(10, 0.5, 1<<16)
	literals := blockLiterals{
		raw:             bytes.Repeat([]byte{'A'}, 96),
		delta:           bytes.Repeat([]byte{'B'}, 96),
		deltaOK:         true,
		collectAdvanced: true,
		collectPos:      true,
	}
	for stream := 0; stream < 4; stream++ {
		literals.pos[stream] = bytes.Repeat([]byte{byte('a' + stream)}, 24)
		literals.posDelta[stream] = bytes.Repeat([]byte{byte('z' - stream)}, 24)
	}

	model := newRawOptimalCostModel()
	plan := optimalEntropyPlan{enabled: true, literalMode: streamLiteralsPosMask3}
	for stream := range plan.literal {
		plan.literal[stream] = 1
	}
	model.applyLiteralCosts(literals, opts, plan)
	if model.literalMode&streamLiteralsPosMask3 == 0 {
		t.Fatalf("literal mode = %d, want to preserve pos-mask bit", model.literalMode)
	}
}

func TestLiteralDeltaRetestPenaltyMatchesReferenceFormula(t *testing.T) {
	opts := compressorLevelOptionsForSize(10, 0.5, 1<<16)
	got := literalDeltaRetestPenalty(96, opts)
	want := int(float64(96) * (opts.decSpeedBias/4 + 0.05/8))
	if got != want {
		t.Fatalf("delta retest penalty = %d, want %d", got, want)
	}
	if got := literalDeltaRetestPenalty(0, opts); got != 0 {
		t.Fatalf("zero-symbol delta retest penalty = %d, want 0", got)
	}
}

func TestOptimalParserIterationsFollowReferenceGate(t *testing.T) {
	opts := compressorLevelOptionsForSize(10, 0.5, 1<<16)
	rawModel := newRawOptimalCostModel()
	rawModel.enabled = true
	if got := optimalParserIterations(opts, &rawModel); got != 1 {
		t.Fatalf("raw-stream iterations = %d, want 1", got)
	}

	huffmanModel := rawModel
	huffmanModel.token[0] = 1
	if got := optimalParserIterations(opts, &huffmanModel); got != 3 {
		t.Fatalf("level 10 iterations = %d, want 3", got)
	}

	lowBias := compressorLevelOptionsForSize(10, 0.05, 1<<16)
	if got := optimalParserIterations(lowBias, &huffmanModel); got != 2 {
		t.Fatalf("low-bias level 10 iterations = %d, want 2", got)
	}

	optimal1 := compressorLevelOptionsForSize(6, 0.5, 1<<16)
	if got := optimalParserIterations(optimal1, &huffmanModel); got != 1 {
		t.Fatalf("OPTIMAL1 iterations = %d, want 1", got)
	}

	fast := compressorLevelOptionsForSize(10, 1, 1<<16)
	if got := optimalParserIterations(fast, &huffmanModel); got != 1 {
		t.Fatalf("no-Huffman-bias iterations = %d, want 1", got)
	}
}

func TestOptimalParserFirstIterationUsesLightOptions(t *testing.T) {
	opts := compressorLevelOptionsForSize(10, 0.5, 1<<16)
	first := optimalParserIterationOptions(opts, 0, 3)
	if first.maxArrivals != 4 || first.niceLength != 128 {
		t.Fatalf("first iteration options = arrivals %d nice %d, want 4/128", first.maxArrivals, first.niceLength)
	}

	later := optimalParserIterationOptions(opts, 1, 3)
	if later.maxArrivals != opts.maxArrivals || later.niceLength != opts.niceLength {
		t.Fatalf("later iteration options = arrivals %d nice %d, want %d/%d",
			later.maxArrivals, later.niceLength, opts.maxArrivals, opts.niceLength)
	}
}

func encodeLiteralOnlyBlockForTest(t *testing.T, src []byte, blockEnd int, decSpeedBias float64) []byte {
	t.Helper()
	if blockEnd <= 1 || blockEnd > len(src)-lastBytes {
		t.Fatalf("invalid block end %d for size %d", blockEnd, len(src))
	}
	literals := newBlockLiterals(src, 0, blockEnd, decSpeedBias)
	var tokens []byte
	var lengths []byte
	literals.appendRun(src, 1, blockEnd, 1, &tokens, &lengths)

	out := writeHeader(nil, blockEnd, blockCompressed, 0)
	out = append(out, src[0])
	out = append(out, literals.encode(decSpeedBias)...)
	out = append(out, encodeEntropy(tokens, 0, decSpeedBias)...)
	out = append(out, encodeEntropy(nil, 0, decSpeedBias)...)
	out = append(out, encodeEntropy(lengths, 0, decSpeedBias)...)
	out = writeHeader(out, len(src)-blockEnd, blockRaw, blockLast)
	out = append(out, src[blockEnd:]...)
	return out
}

func literalFlagsForFirstBlock(t *testing.T, compressed []byte) int {
	t.Helper()
	cpos := 0
	blockSize, blockType, _, err := readHeader(compressed, &cpos)
	if err != nil {
		t.Fatal(err)
	}
	if blockType != blockCompressed || blockSize == 0 {
		t.Fatalf("unexpected first block type=%d size=%d", blockType, blockSize)
	}
	cpos++
	_, flags, err := decodeEntropy(compressed, &cpos)
	if err != nil {
		t.Fatal(err)
	}
	return flags
}

func levelTestCorpus() []byte {
	out := make([]byte, 0, 64*1024)
	for len(out) < 64*1024 {
		out = append(out, "alpha-beta-gamma-delta-"...)
		out = append(out, byte(len(out)), byte(len(out)>>8))
		if len(out) > 2048 {
			start := len(out) - 1536
			out = append(out, out[start:start+192]...)
		}
		out = append(out, "abcdeXabcdeYabcdeZ"...)
	}
	return out
}

func splitFriendlyCorpus(size int) []byte {
	out := make([]byte, size)
	phrase := []byte("alpha beta gamma delta alpha beta gamma delta ")
	for i := range out {
		if i < size/2 {
			out[i] = phrase[i%len(phrase)]
		} else {
			x := uint32(i + 1)
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			out[i] = byte(x)
		}
	}
	return out
}
