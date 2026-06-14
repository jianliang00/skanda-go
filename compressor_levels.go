package skanda

import (
	"encoding/binary"
	"math/bits"
)

type compressorLevelParser uint8

const (
	compressorParserUltraFast compressorLevelParser = iota
	compressorParserGreedy
	compressorParserLazyFast
	compressorParserLazy
	compressorParserOptimal1
	compressorParserOptimal2
	compressorParserOptimal3
)

type compressorLevelOptions struct {
	level            int
	parser           compressorLevelParser
	windowLog        int
	hashLog          int
	hashBytes        int
	minMatchLength   int
	hashEntriesLog   int
	niceLength       int
	optimalBlockSize int
	maxArrivals      int
	parserIterations int
	decSpeedBias     float64
	initialCostModel *optimalCostModel
	matchState       *optimalMatchState
}

type compressState struct {
	lastDistance            int
	repOffsets              [3]int
	acceleration            int
	blockScratch            []byte
	literalRawScratch       []byte
	literalDeltaScratch     []byte
	literalPosScratch       [4][]byte
	literalPosDeltaScratch  [4][]byte
	tokensScratch           []byte
	distancesScratch        []byte
	advancedDistanceScratch []byte
	lengthsScratch          []byte
	hashFinder              *levelHashMatchFinder
	lazyFastFinder          *levelLazyFastMatchFinder
	lazyFinder              *levelLazyMatchFinder
}

type blockEncoding struct {
	data         []byte
	lastDistance int
}

type levelMatch struct {
	pos    int
	length int
}

type levelHashMatchFinder struct {
	table          []int
	hashBytes      int
	hashShift      uint
	hashMask       uint64
	hashLeftShift  uint
	minMatchLength int
	maxDistance    int
}

type levelLazyFastMatchFinder struct {
	hash4 *levelHashMatchFinder
	hash8 *levelHashMatchFinder
}

type levelCacheMatchFinder struct {
	table          []int
	hashShift      uint
	hashMask       uint64
	hashLeftShift  uint
	minMatchLength int
	entries        int
	maxDistance    int
}

type levelLazyMatchFinder struct {
	hash4 *levelCacheMatchFinder
	hash8 *levelCacheMatchFinder
}

type levelOptimalMatchFinder struct {
	hash3       *levelHashMatchFinder
	hash4       *levelCacheMatchFinder
	hash8       *levelCacheMatchFinder
	matches     []levelMatch
	states      []optimalParseState
	stepScratch []optimalMatchStep
}

type lzMatch struct {
	length   int
	distance int
}

type optimalMatchSource interface {
	findLZMatchesAndUpdate(src []byte, pos, inputStart, compressionLimit, blockLimit, minLength int, opts compressorLevelOptions, dst []lzMatch) []lzMatch
}

type binaryCacheTable struct {
	table     []uint32
	entries   int
	hashShift uint
}

type binaryMatchFinder struct {
	chain3          binaryCacheTable
	nodeLookup      []uint32
	nodeLookupShift uint
	nodes           []uint32
	base            int
	nodeListSize    int
	directNodes     bool
}

type matchBuffer struct {
	matches    []lzMatch
	counts     []int
	maxPerPos  int
	blockStart int
}

type optimalMatchState struct {
	optimalFinder    *levelOptimalMatchFinder
	binaryFinder     *binaryMatchFinder
	steps            []optimalMatchStep
	backtrackScratch []optimalMatchStep
	matchBuffer      matchBuffer
	matchScratch     []lzMatch
}

type optimalParseState struct {
	cost       int
	prev       int
	prevPath   int
	matchLen   int
	distance   int
	litRun     int
	repOffsets [3]int
}

type optimalMatchStep struct {
	pos      int
	length   int
	distance int
}

type optimalParseResult struct {
	steps        []optimalMatchStep
	consumed     int
	acceleration int
}

type optimalBlockParseResult struct {
	steps        []optimalMatchStep
	acceleration int
}

type optimalCostModel struct {
	enabled     bool
	literalMode int
	literal     [4][256]int
	token       [256]int
	distance    [256]int
	length      [256]int
}

type optimalEntropyPlan struct {
	enabled     bool
	literalMode int
	literal     [4]float64
	token       float64
	distance    float64
	length      float64
}

const maxPooledIntTableLog = 20
const maxPooledByteBufferLog = 19
const maxPooledUint32BufferLog = 17
const maxPooledOptimalParseStateLog = 17
const maxPooledOptimalMatchStepLog = 16
const pooledSlicesPerClass = 16

var pooledIntTables [maxPooledIntTableLog + 1]chan []int
var pooledByteBuffers [maxPooledByteBufferLog + 1]chan []byte
var pooledUint32Buffers [maxPooledUint32BufferLog + 1]chan []uint32
var pooledOptimalParseStateBuffers [maxPooledOptimalParseStateLog + 1]chan []optimalParseState
var pooledOptimalMatchStepBuffers [maxPooledOptimalMatchStepLog + 1]chan []optimalMatchStep

func init() {
	for i := range pooledIntTables {
		pooledIntTables[i] = make(chan []int, pooledSlicesPerClass)
	}
	for i := range pooledByteBuffers {
		pooledByteBuffers[i] = make(chan []byte, pooledSlicesPerClass)
	}
	for i := range pooledUint32Buffers {
		pooledUint32Buffers[i] = make(chan []uint32, pooledSlicesPerClass)
	}
	for i := range pooledOptimalParseStateBuffers {
		pooledOptimalParseStateBuffers[i] = make(chan []optimalParseState, pooledSlicesPerClass)
	}
	for i := range pooledOptimalMatchStepBuffers {
		pooledOptimalMatchStepBuffers[i] = make(chan []optimalMatchStep, pooledSlicesPerClass)
	}
}

type levelLazyBlockFinder interface {
	find(src []byte, pos, blockEnd, repLength int) levelMatch
	findLazy1(src []byte, pos, blockEnd, currentLength int) levelMatch
	findLazy2(src []byte, pos, blockEnd, currentLength int) levelMatch
	addLongRepeatPositions(src []byte, pos, blockEnd int)
	addShortRepeatPositions(src []byte, pos, blockEnd int)
	addAdvancedRepeatPositions(src []byte, pos, blockEnd int)
	addAfterMatch(src []byte, searchPos, matchPos, matchLength, blockEnd int)
}

func newCompressState() *compressState {
	return &compressState{
		lastDistance: 1,
		repOffsets:   [3]int{1, 1, 1},
		acceleration: optimalAccelerationBase,
	}
}

func (state *compressState) resetForEncode() {
	if state == nil {
		return
	}
	state.lastDistance = 1
	state.repOffsets = [3]int{1, 1, 1}
	state.acceleration = optimalAccelerationBase
	if state.hashFinder != nil {
		state.hashFinder.reset()
	}
	if state.lazyFastFinder != nil {
		state.lazyFastFinder.reset()
	}
	if state.lazyFinder != nil {
		state.lazyFinder.reset()
	}
}

func (state *compressState) release() {
	if state == nil {
		return
	}
	releaseByteBuffer(state.blockScratch)
	state.blockScratch = nil
	releaseCompressionStreamScratch(state)
	if state.hashFinder != nil {
		state.hashFinder.release()
		state.hashFinder = nil
	}
	if state.lazyFastFinder != nil {
		state.lazyFastFinder.release()
		state.lazyFastFinder = nil
	}
	if state.lazyFinder != nil {
		state.lazyFinder.release()
		state.lazyFinder = nil
	}
}

func releaseUnusedCompressState(unused, current *compressState) {
	if unused == nil {
		return
	}
	var currentBlock []byte
	if current != nil {
		currentBlock = current.blockScratch
	}
	releaseUnusedByteBuffer(&unused.blockScratch, currentBlock)
	releaseUnusedStreamScratch(unused, current)
	if unused.hashFinder != nil && (current == nil || unused.hashFinder != current.hashFinder) {
		unused.hashFinder.release()
		unused.hashFinder = nil
	}
	if unused.lazyFastFinder != nil && (current == nil || unused.lazyFastFinder != current.lazyFastFinder) {
		unused.lazyFastFinder.release()
		unused.lazyFastFinder = nil
	}
	if unused.lazyFinder != nil && (current == nil || unused.lazyFinder != current.lazyFinder) {
		unused.lazyFinder.release()
		unused.lazyFinder = nil
	}
}

func compressionBlockBuffer(state *compressState, size int) []byte {
	if state == nil {
		return make([]byte, 0, size)
	}
	return compressionByteBuffer(&state.blockScratch, size)
}

func compressionByteBuffer(buffer *[]byte, size int) []byte {
	if buffer == nil {
		return make([]byte, 0, size)
	}
	if cap(*buffer) < size {
		releaseByteBuffer(*buffer)
		*buffer = acquireByteBuffer(size)
	}
	return (*buffer)[:0]
}

func sameByteBuffer(a, b []byte) bool {
	return cap(a) != 0 && cap(b) != 0 && &a[:1][0] == &b[:1][0]
}

func releaseUnusedByteBuffer(unused *[]byte, current []byte) {
	if unused == nil || cap(*unused) == 0 {
		return
	}
	if sameByteBuffer(*unused, current) {
		return
	}
	releaseByteBuffer(*unused)
	*unused = nil
}

func keepCompressionBlockBuffer(state *compressState, out []byte) {
	if state != nil {
		state.blockScratch = out[:0]
	}
}

func compressionTokenBuffer(state *compressState, blockSize int) []byte {
	if state == nil {
		return nil
	}
	return compressionSizedStreamBuffer(state, &state.tokensScratch, blockSize, 8, 64)
}

func compressionDistanceBuffer(state *compressState, blockSize int) []byte {
	if state == nil {
		return nil
	}
	return compressionSizedStreamBuffer(state, &state.distancesScratch, blockSize, 16, 32)
}

func compressionLengthBuffer(state *compressState, blockSize int) []byte {
	if state == nil {
		return nil
	}
	return compressionSizedStreamBuffer(state, &state.lengthsScratch, blockSize, 16, 32)
}

func compressionAdvancedDistanceBitWriter(state *compressState, blockSize int, advanced bool) advancedDistanceBitWriter {
	if !advanced {
		return advancedDistanceBitWriter{}
	}
	size := blockSize / 16
	if size < 64 {
		size = 64
	}
	if size > blockSize {
		size = blockSize
	}
	if state == nil {
		return advancedDistanceBitWriter{bytes: make([]byte, 0, size), enabled: true}
	}
	return advancedDistanceBitWriter{bytes: compressionByteBuffer(&state.advancedDistanceScratch, size), enabled: true}
}

func compressionSizedStreamBuffer(state *compressState, buffer *[]byte, blockSize, divisor, minimum int) []byte {
	if state == nil {
		return nil
	}
	size := blockSize / divisor
	if size < minimum {
		size = minimum
	}
	if size > blockSize {
		size = blockSize
	}
	return compressionByteBuffer(buffer, size)
}

func keepCompressionStreamBuffers(state *compressState, literals *blockLiterals, tokens, distances []byte, bitWriter *advancedDistanceBitWriter, lengths []byte) {
	if state == nil {
		return
	}
	state.literalRawScratch = literals.raw[:0]
	state.literalDeltaScratch = literals.delta[:0]
	for i := range literals.pos {
		state.literalPosScratch[i] = literals.pos[i][:0]
		state.literalPosDeltaScratch[i] = literals.posDelta[i][:0]
	}
	state.tokensScratch = tokens[:0]
	state.distancesScratch = distances[:0]
	if bitWriter != nil && bitWriter.enabled {
		state.advancedDistanceScratch = bitWriter.bytes[:0]
	}
	state.lengthsScratch = lengths[:0]
}

func releaseCompressionStreamScratch(state *compressState) {
	releaseByteBuffer(state.literalRawScratch)
	state.literalRawScratch = nil
	releaseByteBuffer(state.literalDeltaScratch)
	state.literalDeltaScratch = nil
	for i := range state.literalPosScratch {
		releaseByteBuffer(state.literalPosScratch[i])
		state.literalPosScratch[i] = nil
		releaseByteBuffer(state.literalPosDeltaScratch[i])
		state.literalPosDeltaScratch[i] = nil
	}
	releaseByteBuffer(state.tokensScratch)
	state.tokensScratch = nil
	releaseByteBuffer(state.distancesScratch)
	state.distancesScratch = nil
	releaseByteBuffer(state.advancedDistanceScratch)
	state.advancedDistanceScratch = nil
	releaseByteBuffer(state.lengthsScratch)
	state.lengthsScratch = nil
}

func releaseUnusedStreamScratch(unused, current *compressState) {
	var currentRaw, currentDelta, currentTokens, currentDistances, currentAdvancedDistances, currentLengths []byte
	if current != nil {
		currentRaw = current.literalRawScratch
		currentDelta = current.literalDeltaScratch
		currentTokens = current.tokensScratch
		currentDistances = current.distancesScratch
		currentAdvancedDistances = current.advancedDistanceScratch
		currentLengths = current.lengthsScratch
	}
	releaseUnusedByteBuffer(&unused.literalRawScratch, currentRaw)
	releaseUnusedByteBuffer(&unused.literalDeltaScratch, currentDelta)
	for i := range unused.literalPosScratch {
		var currentPos, currentPosDelta []byte
		if current != nil {
			currentPos = current.literalPosScratch[i]
			currentPosDelta = current.literalPosDeltaScratch[i]
		}
		releaseUnusedByteBuffer(&unused.literalPosScratch[i], currentPos)
		releaseUnusedByteBuffer(&unused.literalPosDeltaScratch[i], currentPosDelta)
	}
	releaseUnusedByteBuffer(&unused.tokensScratch, currentTokens)
	releaseUnusedByteBuffer(&unused.distancesScratch, currentDistances)
	releaseUnusedByteBuffer(&unused.advancedDistanceScratch, currentAdvancedDistances)
	releaseUnusedByteBuffer(&unused.lengthsScratch, currentLengths)
}

func repOffsetsFromState(state *compressState) [3]int {
	if state == nil || state.repOffsets[0] <= 0 {
		return [3]int{1, 1, 1}
	}
	return state.repOffsets
}

func optimalAccelerationFromState(state *compressState) int {
	if state == nil || state.acceleration <= 0 {
		return optimalAccelerationBase
	}
	return state.acceleration
}

func levelHashFinderFromState(state *compressState, opts compressorLevelOptions) *levelHashMatchFinder {
	if state == nil {
		return newLevelHashMatchFinder(opts)
	}
	if state.hashFinder == nil {
		state.hashFinder = newLevelHashMatchFinder(opts)
	}
	return state.hashFinder
}

func levelLazyFastFinderFromState(state *compressState, opts compressorLevelOptions) *levelLazyFastMatchFinder {
	if state == nil {
		return newLevelLazyFastMatchFinder(opts)
	}
	if state.lazyFastFinder == nil {
		state.lazyFastFinder = newLevelLazyFastMatchFinder(opts)
	}
	return state.lazyFastFinder
}

func levelLazyFinderFromState(state *compressState, opts compressorLevelOptions) *levelLazyMatchFinder {
	if state == nil {
		return newLevelLazyMatchFinder(opts)
	}
	if state.lazyFinder == nil {
		state.lazyFinder = newLevelLazyMatchFinder(opts)
	}
	return state.lazyFinder
}

func compressorLevelOptionsForSize(level int, decSpeedBias float64, size int) compressorLevelOptions {
	opts := compressorLevelOptionsForLevel(level, decSpeedBias)
	opts.windowLog = skandaWindowLog(size, opts.decSpeedBias)
	return opts
}

func compressorLevelOptionsForLevel(level int, decSpeedBias float64) compressorLevelOptions {
	if level < 0 {
		level = 0
	}
	if level > 10 {
		level = 10
	}
	if decSpeedBias < 0 {
		decSpeedBias = 0
	}
	if decSpeedBias > 1 {
		decSpeedBias = 1
	}

	if level == 0 {
		return compressorLevelOptions{
			level:          level,
			parser:         compressorParserUltraFast,
			hashLog:        14,
			hashBytes:      6,
			minMatchLength: 6,
			decSpeedBias:   decSpeedBias,
		}
	}
	if level == 1 {
		return compressorLevelOptions{
			level:          level,
			parser:         compressorParserGreedy,
			hashLog:        16,
			hashBytes:      5,
			minMatchLength: 5,
			decSpeedBias:   decSpeedBias,
		}
	}
	if level == 2 {
		return compressorLevelOptions{
			level:          level,
			parser:         compressorParserLazyFast,
			hashLog:        17,
			hashBytes:      4,
			minMatchLength: 4,
			hashEntriesLog: 0,
			decSpeedBias:   decSpeedBias,
		}
	}
	if level == 3 {
		return compressorLevelOptions{
			level:          level,
			parser:         compressorParserLazy,
			hashLog:        17,
			hashBytes:      4,
			minMatchLength: 4,
			hashEntriesLog: 1,
			decSpeedBias:   decSpeedBias,
		}
	}
	if level == 4 {
		return compressorLevelOptions{
			level:          level,
			parser:         compressorParserLazy,
			hashLog:        18,
			hashBytes:      4,
			minMatchLength: 4,
			hashEntriesLog: 2,
			decSpeedBias:   decSpeedBias,
		}
	}
	if level == 5 {
		return compressorLevelOptions{
			level:            level,
			parser:           compressorParserOptimal1,
			hashLog:          18,
			hashBytes:        4,
			minMatchLength:   4,
			hashEntriesLog:   2,
			niceLength:       32,
			optimalBlockSize: 1024,
			decSpeedBias:     decSpeedBias,
		}
	}
	if level == 6 {
		return compressorLevelOptions{
			level:            level,
			parser:           compressorParserOptimal1,
			hashLog:          19,
			hashBytes:        4,
			minMatchLength:   4,
			hashEntriesLog:   2,
			niceLength:       64,
			optimalBlockSize: 1024,
			decSpeedBias:     decSpeedBias,
		}
	}
	if level <= 8 {
		if level == 7 {
			return compressorLevelOptions{
				level:            level,
				parser:           compressorParserOptimal2,
				hashLog:          24,
				hashBytes:        4,
				minMatchLength:   4,
				hashEntriesLog:   4,
				niceLength:       128,
				optimalBlockSize: 4096,
				maxArrivals:      2,
				parserIterations: 2,
				decSpeedBias:     decSpeedBias,
			}
		}
		return compressorLevelOptions{
			level:            level,
			parser:           compressorParserOptimal2,
			hashLog:          25,
			hashBytes:        4,
			minMatchLength:   4,
			hashEntriesLog:   5,
			niceLength:       256,
			optimalBlockSize: 4096,
			maxArrivals:      4,
			parserIterations: 2,
			decSpeedBias:     decSpeedBias,
		}
	}
	if level == 9 {
		return compressorLevelOptions{
			level:            level,
			parser:           compressorParserOptimal3,
			hashLog:          26,
			hashBytes:        4,
			minMatchLength:   4,
			hashEntriesLog:   6,
			niceLength:       512,
			optimalBlockSize: 4096,
			maxArrivals:      8,
			parserIterations: 2,
			decSpeedBias:     decSpeedBias,
		}
	}
	return compressorLevelOptions{
		level:            level,
		parser:           compressorParserOptimal3,
		hashLog:          27,
		hashBytes:        4,
		minMatchLength:   4,
		hashEntriesLog:   7,
		niceLength:       1024,
		optimalBlockSize: 4096,
		maxArrivals:      16,
		parserIterations: 3,
		decSpeedBias:     decSpeedBias,
	}
}

func compressBlockLevel(src []byte, blockStart, blockEnd int, state *compressState, opts compressorLevelOptions) blockEncoding {
	opts = normalizeCompressorLevelOptions(opts)
	if opts.parser == compressorParserLazyFast {
		return compressBlockLazyFast(src, blockStart, blockEnd, state, opts)
	}
	if opts.parser == compressorParserLazy {
		return compressBlockLazy(src, blockStart, blockEnd, state, opts)
	}
	if opts.parser == compressorParserOptimal1 || opts.parser == compressorParserOptimal2 || opts.parser == compressorParserOptimal3 {
		return compressBlockOptimal(src, blockStart, blockEnd, state, opts)
	}

	lastDistance := 1
	if state != nil && state.lastDistance > 0 {
		lastDistance = state.lastDistance
	}
	repOffsets := repOffsetsFromState(state)
	advancedDistance := opts.decSpeedBias < 0.1
	noHuffman := opts.decSpeedBias >= 0.99
	matchDistanceLimit := maxMatchDistance(opts)

	blockSize := blockEnd - blockStart
	literals := newCompressionBlockLiterals(state, src, blockStart, blockEnd, opts.decSpeedBias, false)
	tokens := compressionTokenBuffer(state, blockSize)
	distances := compressionDistanceBuffer(state, blockSize)
	lengths := compressionLengthBuffer(state, blockSize)
	bitWriter := compressionAdvancedDistanceBitWriter(state, blockSize, advancedDistance)

	out := compressionBlockBuffer(state, CompressBound(blockEnd-blockStart))
	out = writeHeader(out, blockEnd-blockStart, blockCompressed, 0)

	pos := blockStart
	if blockStart == 0 && pos < blockEnd {
		out = append(out, src[pos])
		pos++
	}

	finder := levelHashFinderFromState(state, opts)
	litStart := pos
	accelerationThreshold := finder.accelerationThreshold(opts.parser)
	acceleration := 1 << accelerationThreshold
	fastRepeatLiterals := advancedDistance && literals.preallocated && !literals.collectPos && literals.collectAdvanced
	level0Hash6Fast := opts.level == 0 &&
		opts.parser == compressorParserUltraFast &&
		opts.hashBytes == 6 &&
		opts.minMatchLength == 6 &&
		finder.hashBytes == 6 &&
		finder.minMatchLength == 6 &&
		finder.hashLeftShift == 16

	for pos < blockEnd {
		if opts.parser == compressorParserGreedy {
			if rep := findRepeatDistanceMatch(src, pos+1, blockEnd, repOffsets[0], 3, matchDistanceLimit); rep.length > 0 {
				matchStart := pos + 1
				literals.appendRun(src, litStart, matchStart, repOffsets[0], &tokens, &lengths)
				finder.add(src, matchStart, blockEnd)
				if advancedDistance {
					appendAdvancedBlockDistance(repOffsets[0], &distances, &bitWriter, &tokens, &lastDistance, &repOffsets)
				} else {
					appendStandardBlockDistance(repOffsets[0], &distances, &tokens, &lastDistance, &repOffsets)
				}
				appendMatchLengthWithMode(rep.length, noHuffman, &tokens, &lengths)
				pos = matchStart + rep.length
				litStart = pos
				acceleration = 1 << accelerationThreshold
				continue
			}
		}

		var matchPos, matchLength int
		if level0Hash6Fast {
			matchPos, matchLength = finder.findAndUpdateLevel0(src, pos, blockEnd)
		} else {
			matchPos, matchLength = finder.findAndUpdate(src, pos, blockEnd)
		}
		if matchLength == 0 {
			step := acceleration >> accelerationThreshold
			if step < 1 {
				step = 1
			}
			pos += step
			acceleration++
			continue
		}

		if opts.level >= 2 && pos+1 < blockEnd {
			nextPos, nextLength := finder.findAndUpdate(src, pos+1, blockEnd)
			if nextLength > matchLength {
				pos++
				matchPos = nextPos
				matchLength = nextLength
			}
		}

		for pos > litStart && matchPos > 0 && src[pos-1] == src[matchPos-1] {
			pos--
			matchPos--
			matchLength++
		}

		if level0Hash6Fast {
			finder.addAfterMatchLevel0(src, pos, matchLength, blockEnd)
		} else {
			finder.addAfterMatch(src, pos, matchLength, blockEnd)
		}
		if level0Hash6Fast {
			literals.appendRunLevel0(src, litStart, pos, repOffsets[0], &tokens, &lengths)
		} else {
			literals.appendRun(src, litStart, pos, repOffsets[0], &tokens, &lengths)
		}
		distance := pos - matchPos
		if advancedDistance {
			appendAdvancedBlockDistance(distance, &distances, &bitWriter, &tokens, &lastDistance, &repOffsets)
		} else {
			appendStandardBlockDistance(distance, &distances, &tokens, &lastDistance, &repOffsets)
		}
		appendMatchLengthWithMode(matchLength, noHuffman, &tokens, &lengths)
		pos += matchLength
		litStart = pos
		acceleration = 1 << accelerationThreshold

		if opts.parser == compressorParserUltraFast {
			litStart = pos
			for {
				matchPos := pos + 1
				repeatLength := 0
				if matchPos+3 <= blockEnd {
					if matchPos+4 <= blockEnd && src[matchPos] != src[matchPos-repOffsets[0]] {
						matchPos++
					}
					if src[matchPos] == src[matchPos-repOffsets[0]] {
						repeatLength = commonMatchLengthUnchecked(src, matchPos, matchPos-repOffsets[0], blockEnd)
					}
				}
				if repeatLength >= 3 {
					runLength := matchPos - litStart
					repeatDistance := repOffsets[0]
					if fastRepeatLiterals && (runLength == 1 || runLength == 2) &&
						literals.deltaOK && repeatDistance > 0 && litStart-repeatDistance >= 0 {
						tokens = append(tokens, byte(runLength<<5))
						rawOldLen := len(literals.raw)
						deltaOldLen := len(literals.delta)
						literals.raw = literals.raw[:rawOldLen+runLength]
						literals.delta = literals.delta[:deltaOldLen+runLength]
						value := src[litStart]
						ref := src[litStart-repeatDistance]
						literals.raw[rawOldLen] = value
						literals.delta[deltaOldLen] = value - ref
						if runLength == 2 {
							value = src[litStart+1]
							ref = src[litStart+1-repeatDistance]
							literals.raw[rawOldLen+1] = value
							literals.delta[deltaOldLen+1] = value - ref
						}
					} else {
						if level0Hash6Fast {
							literals.appendRunLevel0(src, litStart, matchPos, repeatDistance, &tokens, &lengths)
						} else {
							literals.appendRun(src, litStart, matchPos, repeatDistance, &tokens, &lengths)
						}
					}
					if level0Hash6Fast {
						finder.addRepeatPositionsLevel0(src, matchPos, blockEnd)
					} else {
						finder.addRepeatPositions(src, matchPos, blockEnd)
					}
					if advancedDistance {
						lastDistance = repeatDistance
					} else {
						appendStandardBlockDistance(repeatDistance, &distances, &tokens, &lastDistance, &repOffsets)
					}
					if !noHuffman {
						if repeatLength <= 8 {
							tokens[len(tokens)-1] |= byte(repeatLength - minMatchLength)
						} else {
							tokens[len(tokens)-1] |= 7
							encodeLength(&lengths, repeatLength, 9)
						}
					} else {
						appendMatchLengthWithMode(repeatLength, true, &tokens, &lengths)
					}
					pos = matchPos + repeatLength
					litStart = pos
					continue
				}
				if advancedDistance {
					if level0Hash6Fast {
						if nextPos, ok := appendAdvancedRepeatMatchLevel0(src, litStart, pos, blockEnd, repOffsets[1], matchDistanceLimit, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets); ok {
							pos = nextPos
							litStart = pos
							continue
						}
						if nextPos, ok := appendAdvancedRepeatMatchLevel0(src, litStart, pos, blockEnd, repOffsets[2], matchDistanceLimit, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets); ok {
							pos = nextPos
							litStart = pos
							continue
						}
					} else {
						if nextPos, ok := appendAdvancedRepeatMatch(src, litStart, pos, blockEnd, repOffsets[1], 4, matchDistanceLimit, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets); ok {
							pos = nextPos
							litStart = pos
							continue
						}
						if nextPos, ok := appendAdvancedRepeatMatch(src, litStart, pos, blockEnd, repOffsets[2], 4, matchDistanceLimit, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets); ok {
							pos = nextPos
							litStart = pos
							continue
						}
					}
				}
				break
			}
		} else if opts.parser == compressorParserGreedy && advancedDistance {
			if nextPos, ok := appendAdvancedRepeatMatch(src, litStart, pos, blockEnd, repOffsets[1], 4, matchDistanceLimit, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets); ok {
				pos = nextPos
				litStart = pos
				continue
			}
			if nextPos, ok := appendAdvancedRepeatMatch(src, litStart, pos, blockEnd, repOffsets[2], 4, matchDistanceLimit, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets); ok {
				pos = nextPos
				litStart = pos
				continue
			}
		}
	}

	if litStart < blockEnd {
		if level0Hash6Fast {
			literals.appendRunLevel0(src, litStart, blockEnd, repOffsets[0], &tokens, &lengths)
		} else {
			literals.appendRun(src, litStart, blockEnd, repOffsets[0], &tokens, &lengths)
		}
	}

	stripedEntropy := opts.parser == compressorParserUltraFast
	if stripedEntropy {
		out = literals.appendEncodedWithStripedHistogram(out, opts.decSpeedBias, true)
		out = appendEntropyWithStripedHistogram(out, tokens, 0, opts.decSpeedBias, true)
	} else {
		out = literals.appendEncoded(out, opts.decSpeedBias)
		out = appendEntropy(out, tokens, 0, opts.decSpeedBias)
	}
	distanceFlags := 0
	if advancedDistance {
		distanceFlags = streamDistanceAdvanced
	}
	if stripedEntropy {
		out = appendEntropyWithStripedHistogram(out, distances, distanceFlags, opts.decSpeedBias, true)
	} else {
		out = appendEntropy(out, distances, distanceFlags, opts.decSpeedBias)
	}
	if advancedDistance {
		out = bitWriter.appendTo(out)
	}
	if stripedEntropy {
		out = appendEntropyWithStripedHistogram(out, lengths, 0, opts.decSpeedBias, true)
	} else {
		out = appendEntropy(out, lengths, 0, opts.decSpeedBias)
	}

	if state != nil {
		state.lastDistance = lastDistance
		state.repOffsets = repOffsets
		keepCompressionBlockBuffer(state, out)
		keepCompressionStreamBuffers(state, &literals, tokens, distances, &bitWriter, lengths)
	}
	return blockEncoding{data: out, lastDistance: lastDistance}
}

func compressBlockLazyFast(src []byte, blockStart, blockEnd int, state *compressState, opts compressorLevelOptions) blockEncoding {
	return compressBlockLazyWithFinder(src, blockStart, blockEnd, state, opts, levelLazyFastFinderFromState(state, opts))
}

func compressBlockLazy(src []byte, blockStart, blockEnd int, state *compressState, opts compressorLevelOptions) blockEncoding {
	return compressBlockLazyWithFinder(src, blockStart, blockEnd, state, opts, levelLazyFinderFromState(state, opts))
}

func compressBlockOptimal(src []byte, blockStart, blockEnd int, state *compressState, opts compressorLevelOptions) blockEncoding {
	startRepOffsets := repOffsetsFromState(state)
	acceleration := optimalAccelerationFromState(state)
	advancedDistance := opts.decSpeedBias < 0.1

	entropyPlan := optimalEntropyPlanFromModel(opts, opts.initialCostModel)
	noHuffmanCosts := useNoHuffmanParserCosts(opts, opts.initialCostModel)

	out := compressionBlockBuffer(state, CompressBound(blockEnd-blockStart))
	out = writeHeader(out, blockEnd-blockStart, blockCompressed, 0)

	if blockStart == 0 && blockStart < blockEnd {
		out = append(out, src[blockStart])
	}

	iterations := optimalParserIterations(opts, opts.initialCostModel)
	matchSource := precomputeOptimalMatchSource(src, blockStart, blockEnd, opts, iterations, noHuffmanCosts)
	parseResult := parseOptimalBlockWithPrecomputedSourceAndAcceleration(src, blockStart, blockEnd, startRepOffsets, advancedDistance,
		optimalParserIterationOptions(opts, 0, iterations), opts.initialCostModel, noHuffmanCosts, matchSource, acceleration)
	steps := parseResult.steps
	acceleration = parseResult.acceleration
	if iterations > 1 {
		for iteration := 1; iteration < iterations; iteration++ {
			model := buildOptimalCostModel(src, blockStart, blockEnd, steps, startRepOffsets, advancedDistance, opts, entropyPlan, state)
			if !model.enabled {
				break
			}
			parseResult = parseOptimalBlockWithPrecomputedSourceAndAcceleration(src, blockStart, blockEnd, startRepOffsets, advancedDistance,
				optimalParserIterationOptions(opts, iteration, iterations), &model, noHuffmanCosts, matchSource, acceleration)
			steps = parseResult.steps
			acceleration = parseResult.acceleration
		}
	}
	outputModel := optimalCostModel{}
	if noHuffmanCosts {
		outputModel = buildOptimalCostModel(src, blockStart, blockEnd, steps, startRepOffsets, advancedDistance, opts, entropyPlan, state)
	}
	blockSize := blockEnd - blockStart
	literals := newCompressionBlockLiterals(state, src, blockStart, blockEnd, opts.decSpeedBias, entropyPlan.literalMode&streamLiteralsPosMask3 != 0)
	tokens := compressionTokenBuffer(state, blockSize)
	distances := compressionDistanceBuffer(state, blockSize)
	lengths := compressionLengthBuffer(state, blockSize)
	bitWriter := compressionAdvancedDistanceBitWriter(state, blockSize, advancedDistance)
	lastDistance := startRepOffsets[0]
	repOffsets := startRepOffsets
	litStart := blockStart
	if blockStart == 0 {
		litStart++
	}
	for _, step := range steps {
		if step.pos < litStart || step.pos+step.length > blockEnd {
			continue
		}
		literals.appendRun(src, litStart, step.pos, repOffsets[0], &tokens, &lengths)
		if advancedDistance {
			appendAdvancedBlockDistance(step.distance, &distances, &bitWriter, &tokens, &lastDistance, &repOffsets)
		} else {
			appendStandardBlockDistance(step.distance, &distances, &tokens, &lastDistance, &repOffsets)
		}
		appendMatchLengthWithMode(step.length, noHuffmanCosts, &tokens, &lengths)
		litStart = step.pos + step.length
	}

	if litStart < blockEnd {
		literals.appendRun(src, litStart, blockEnd, repOffsets[0], &tokens, &lengths)
	}
	if !noHuffmanCosts {
		outputModel = buildOptimalCostModelFromStreams(literals, tokens, distances, lengths, opts, entropyPlan)
	}

	useFastCodegen := opts.parser < compressorParserOptimal3
	out = appendOptimalLiterals(out, literals, opts, outputModel, entropyPlan, useFastCodegen)
	out = appendEntropyWithCodegen(out, tokens, 0, entropyPlan.streamBias(entropyPlan.token, opts.decSpeedBias), useFastCodegen)
	distanceFlags := 0
	if advancedDistance {
		distanceFlags = streamDistanceAdvanced
	}
	out = appendEntropyWithCodegen(out, distances, distanceFlags, entropyPlan.streamBias(entropyPlan.distance, opts.decSpeedBias), useFastCodegen)
	if advancedDistance {
		out = bitWriter.appendTo(out)
	}
	out = appendEntropyWithCodegen(out, lengths, 0, entropyPlan.streamBias(entropyPlan.length, opts.decSpeedBias), useFastCodegen)

	if state != nil {
		state.lastDistance = lastDistance
		state.repOffsets = repOffsets
		state.acceleration = acceleration
		keepCompressionBlockBuffer(state, out)
		keepCompressionStreamBuffers(state, &literals, tokens, distances, &bitWriter, lengths)
	}
	return blockEncoding{data: out, lastDistance: lastDistance}
}

func useBufferedMatches(opts compressorLevelOptions) bool {
	return opts.parser != compressorParserOptimal1 && opts.parserIterations > 1 && opts.decSpeedBias < 0.99
}

func precomputeOptimalMatchSource(src []byte, blockStart, blockEnd int, opts compressorLevelOptions, iterations int, noHuffmanCosts bool) optimalMatchSource {
	if iterations <= 1 || !useBufferedMatches(opts) || noHuffmanCosts {
		return nil
	}
	finder := optimalBinaryMatchFinder(blockStart, blockEnd, opts)
	if opts.matchState != nil {
		return opts.matchState.resetMatchBuffer(src, blockStart, blockEnd, len(src)-lastBytes, opts, finder)
	}
	return newMatchBuffer(src, blockStart, blockEnd, len(src)-lastBytes, opts, finder)
}

func newOptimalMatchState(size int, opts compressorLevelOptions) *optimalMatchState {
	opts = normalizeCompressorLevelOptions(opts)
	switch opts.parser {
	case compressorParserOptimal1:
		finder := newLevelOptimalMatchFinder(opts)
		return &optimalMatchState{optimalFinder: &finder}
	case compressorParserOptimal2, compressorParserOptimal3:
		return &optimalMatchState{binaryFinder: newBinaryMatchFinder(0, max(1, size), opts)}
	default:
		return nil
	}
}

func (state *optimalMatchState) release() {
	if state == nil {
		return
	}
	if state.optimalFinder != nil {
		state.optimalFinder.release()
		state.optimalFinder = nil
	}
	if state.binaryFinder != nil {
		state.binaryFinder.release()
		state.binaryFinder = nil
	}
	if state.steps != nil {
		releaseOptimalMatchSteps(state.steps)
		state.steps = nil
	}
	if state.backtrackScratch != nil {
		releaseOptimalMatchSteps(state.backtrackScratch)
		state.backtrackScratch = nil
	}
	state.matchBuffer = matchBuffer{}
	state.matchScratch = nil
}

func (state *optimalMatchState) resetForEncode() {
	if state == nil {
		return
	}
	if state.optimalFinder != nil {
		state.optimalFinder.reset()
	}
	if state.binaryFinder != nil {
		state.binaryFinder.reset()
	}
	if state.steps != nil {
		state.steps = state.steps[:0]
	}
	if state.backtrackScratch != nil {
		state.backtrackScratch = state.backtrackScratch[:0]
	}
	state.matchBuffer.counts = state.matchBuffer.counts[:0]
	state.matchBuffer.matches = state.matchBuffer.matches[:0]
	state.matchScratch = state.matchScratch[:0]
}

func (state *optimalMatchState) resetSteps(sizeHint int) []optimalMatchStep {
	if state == nil {
		return make([]optimalMatchStep, 0, sizeHint)
	}
	if cap(state.steps) < sizeHint {
		if state.steps != nil {
			releaseOptimalMatchSteps(state.steps)
		}
		state.steps = acquireOptimalMatchSteps(sizeHint)
	}
	return state.steps[:0]
}

func (state *optimalMatchState) keepSteps(steps []optimalMatchStep) {
	if state != nil {
		state.steps = steps
	}
}

func (state *optimalMatchState) resetMatchBuffer(src []byte, blockStart, blockEnd, compressionLimit int, opts compressorLevelOptions, finder *binaryMatchFinder) *matchBuffer {
	return fillMatchBuffer(src, blockStart, blockEnd, compressionLimit, opts, finder, &state.matchBuffer, &state.matchScratch)
}

func optimalMatchStepSizeHint(blockSize int) int {
	if blockSize <= 0 {
		return 0
	}
	hint := blockSize / 16
	if hint < 16 {
		return 16
	}
	if hint > 1<<maxPooledOptimalMatchStepLog {
		return 1 << maxPooledOptimalMatchStepLog
	}
	return hint
}

func useHuffmanCostModel(opts compressorLevelOptions) bool {
	return opts.decSpeedBias < 0.99 && (opts.parser == compressorParserOptimal1 || opts.parser == compressorParserOptimal2 || opts.parser == compressorParserOptimal3)
}

func useNoHuffmanParserCosts(opts compressorLevelOptions, model *optimalCostModel) bool {
	if opts.decSpeedBias >= 0.99 {
		return true
	}
	return model != nil && model.enabled && model.allStreamsRaw()
}

func optimalEntropyPlanFromModel(opts compressorLevelOptions, model *optimalCostModel) optimalEntropyPlan {
	plan := optimalEntropyPlan{
		token:    opts.decSpeedBias,
		distance: opts.decSpeedBias,
		length:   opts.decSpeedBias,
	}
	for stream := range plan.literal {
		plan.literal[stream] = opts.decSpeedBias
	}
	if !useHuffmanCostModel(opts) || model == nil || !model.enabled {
		return plan
	}
	plan.enabled = true
	plan.literalMode = model.literalMode
	streamCount := 1
	if model.literalMode&streamLiteralsPosMask3 != 0 {
		streamCount = 4
	}
	for stream := 0; stream < streamCount; stream++ {
		plan.literal[stream] = forcedEntropyBias(&model.literal[stream])
	}
	plan.token = forcedEntropyBias(&model.token)
	plan.distance = forcedEntropyBias(&model.distance)
	plan.length = forcedEntropyBias(&model.length)
	return plan
}

func forcedEntropyBias(costs *[256]int) float64 {
	if allCostsRaw(costs) {
		return 1
	}
	return 0
}

func (plan optimalEntropyPlan) literalBias(stream int, fallback float64) float64 {
	if !plan.enabled || stream < 0 || stream >= len(plan.literal) {
		return fallback
	}
	return plan.literal[stream]
}

func (plan optimalEntropyPlan) streamBias(bias, fallback float64) float64 {
	if !plan.enabled {
		return fallback
	}
	return bias
}

func optimalParserIterations(opts compressorLevelOptions, model *optimalCostModel) int {
	if !useHuffmanCostModel(opts) || opts.parser < compressorParserOptimal2 || model == nil || !model.enabled || model.allStreamsRaw() {
		return 1
	}
	if opts.parserIterations < 1 {
		return 1
	}
	if opts.level >= 10 && opts.decSpeedBias <= 0.1 && opts.parserIterations > 2 {
		return 2
	}
	return opts.parserIterations
}

func optimalParserIterationOptions(opts compressorLevelOptions, iteration, iterations int) compressorLevelOptions {
	if iteration != 0 || iterations <= 1 || opts.parser < compressorParserOptimal2 {
		return opts
	}
	opts.maxArrivals = max(opts.maxArrivals/4, 1)
	opts.niceLength = max(opts.niceLength/8, 32)
	return opts
}

func (model *optimalCostModel) allStreamsRaw() bool {
	if model == nil || !model.enabled {
		return true
	}
	streamCount := 1
	if model.literalMode&streamLiteralsPosMask3 != 0 {
		streamCount = 4
	}
	for stream := 0; stream < streamCount; stream++ {
		if !allCostsRaw(&model.literal[stream]) {
			return false
		}
	}
	return allCostsRaw(&model.token) && allCostsRaw(&model.distance) && allCostsRaw(&model.length)
}

func allCostsRaw(costs *[256]int) bool {
	for _, cost := range costs {
		if cost != 8 {
			return false
		}
	}
	return true
}

func parseOptimalBlock(src []byte, blockStart, blockEnd int, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool) []optimalMatchStep {
	return parseOptimalBlockInternal(src, blockStart, blockEnd, startRepOffsets, advancedDistance, opts, model, noHuffmanCosts, nil, false)
}

func parseOptimalBlockWithPrecomputedSource(src []byte, blockStart, blockEnd int, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool, precomputedSource optimalMatchSource) []optimalMatchStep {
	return parseOptimalBlockInternal(src, blockStart, blockEnd, startRepOffsets, advancedDistance, opts, model, noHuffmanCosts, precomputedSource, true)
}

func parseOptimalBlockWithPrecomputedSourceAndAcceleration(src []byte, blockStart, blockEnd int, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool, precomputedSource optimalMatchSource, acceleration int) optimalBlockParseResult {
	return parseOptimalBlockInternalDetailed(src, blockStart, blockEnd, startRepOffsets, advancedDistance, opts, model, noHuffmanCosts, precomputedSource, true, acceleration)
}

func parseOptimalBlockInternal(src []byte, blockStart, blockEnd int, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool, precomputedSource optimalMatchSource, usePrecomputedSource bool) []optimalMatchStep {
	return parseOptimalBlockInternalDetailed(src, blockStart, blockEnd, startRepOffsets, advancedDistance, opts, model, noHuffmanCosts, precomputedSource, usePrecomputedSource, optimalAccelerationBase).steps
}

func parseOptimalBlockInternalDetailed(src []byte, blockStart, blockEnd int, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool, precomputedSource optimalMatchSource, usePrecomputedSource bool, acceleration int) optimalBlockParseResult {
	pos := blockStart
	if blockStart == 0 && pos < blockEnd {
		pos++
	}
	if acceleration <= 0 {
		acceleration = optimalAccelerationBase
	}

	steps := opts.matchState.resetSteps(optimalMatchStepSizeHint(blockEnd - blockStart))
	var matchSource optimalMatchSource
	var finder *levelOptimalMatchFinder
	if opts.parser == compressorParserOptimal1 {
		if opts.matchState != nil && opts.matchState.optimalFinder != nil {
			finder = opts.matchState.optimalFinder
		} else {
			localFinder := newLevelOptimalMatchFinder(opts)
			finder = &localFinder
		}
	} else {
		if usePrecomputedSource {
			matchSource = precomputedSource
			if matchSource == nil {
				matchSource = optimalBinaryMatchFinder(blockStart, blockEnd, opts)
			}
		} else if useBufferedMatches(opts) && !noHuffmanCosts {
			matchSource = precomputeOptimalMatchSource(src, blockStart, blockEnd, opts, opts.parserIterations, noHuffmanCosts)
		} else {
			matchSource = optimalBinaryMatchFinder(blockStart, blockEnd, opts)
		}
	}

	repOffsets := startRepOffsets
	litStart := pos
	for pos < blockEnd {
		if acceleration >= optimalAccelerationLimit {
			step := optimalGreedyFallbackStep(src, pos, litStart, blockStart, blockEnd, finder, matchSource, opts)
			if step.length > 0 {
				steps = append(steps, step)
				repOffsets = updateOptimalRepOffsets(repOffsets, step.distance, advancedDistance)
				pos = step.pos + step.length
				litStart = pos
				acceleration = optimalAccelerationBase
			} else {
				skip := acceleration >> optimalAccelerationThreshold
				if skip < 1 {
					skip = 1
				}
				pos += skip
				if pos > blockEnd {
					pos = blockEnd
				}
				acceleration++
			}
			continue
		}

		chunkSize := optimalParseChunkSize(opts)
		parseEnd := pos + min(chunkSize, blockEnd-pos)
		result := optimalParseResult{consumed: parseEnd, acceleration: acceleration}
		if opts.parser == compressorParserOptimal1 {
			result = optimalParseDetailedWithAcceleration(src, pos, parseEnd, blockEnd, finder, repOffsets, advancedDistance, opts, model, noHuffmanCosts, acceleration)
		} else {
			result = multiArrivalOptimalParseDetailedWithAcceleration(src, pos, parseEnd, blockEnd, matchSource, repOffsets, advancedDistance, opts, model, noHuffmanCosts, acceleration)
		}
		acceleration = result.acceleration
		for _, step := range result.steps {
			if step.pos < pos || step.pos+step.length > blockEnd {
				continue
			}
			steps = append(steps, step)
			repOffsets = updateOptimalRepOffsets(repOffsets, step.distance, advancedDistance)
			pos = step.pos + step.length
			litStart = pos
		}
		if result.consumed > pos {
			pos = result.consumed
		}
	}
	opts.matchState.keepSteps(steps)
	return optimalBlockParseResult{steps: steps, acceleration: acceleration}
}

func optimalParseChunkSize(opts compressorLevelOptions) int {
	chunkSize := opts.optimalBlockSize
	if opts.parser >= compressorParserOptimal2 {
		chunkSize--
	}
	if chunkSize < 1 {
		return 1
	}
	return chunkSize
}

func optimalGreedyFallbackStep(src []byte, pos, litStart, blockStart, blockEnd int, finder *levelOptimalMatchFinder, matchSource optimalMatchSource, opts compressorLevelOptions) optimalMatchStep {
	if opts.parser == compressorParserOptimal1 {
		if finder == nil {
			return optimalMatchStep{}
		}
		matches := finder.findMatchesAndUpdate(src, pos, blockEnd, 4)
		if len(matches) == 0 {
			return optimalMatchStep{}
		}
		match := matches[len(matches)-1]
		distance := pos - match.pos
		matchPos, length := extendOptimalMatchLeft(src, 0, pos, distance, match.length, pos-litStart)
		return optimalMatchStep{pos: matchPos, length: length, distance: distance}
	}
	if matchSource == nil {
		return optimalMatchStep{}
	}
	minLength := 4
	if _, ok := matchSource.(*matchBuffer); ok {
		minLength = 3
	}
	var matches []lzMatch
	matches = matchSource.findLZMatchesAndUpdate(src, pos, 0, len(src)-lastBytes, blockEnd, minLength, opts, matches)
	if len(matches) == 0 {
		return optimalMatchStep{}
	}
	match := matches[len(matches)-1]
	matchPos, length := extendOptimalMatchLeft(src, 0, pos, match.distance, match.length, pos-litStart)
	if matchPos < blockStart {
		return optimalMatchStep{}
	}
	return optimalMatchStep{pos: matchPos, length: length, distance: match.distance}
}

func optimalBinaryMatchFinder(blockStart, blockEnd int, opts compressorLevelOptions) *binaryMatchFinder {
	if opts.matchState != nil && opts.matchState.binaryFinder != nil {
		return opts.matchState.binaryFinder
	}
	return newBinaryMatchFinder(blockStart, blockEnd, opts)
}

func compressBlockLazyWithFinder(src []byte, blockStart, blockEnd int, state *compressState, opts compressorLevelOptions, finder levelLazyBlockFinder) blockEncoding {
	lastDistance := 1
	if state != nil && state.lastDistance > 0 {
		lastDistance = state.lastDistance
	}
	repOffsets := repOffsetsFromState(state)
	advancedDistance := opts.decSpeedBias < 0.1
	noHuffman := opts.decSpeedBias >= 0.99
	matchDistanceLimit := maxMatchDistance(opts)

	blockSize := blockEnd - blockStart
	literals := newCompressionBlockLiterals(state, src, blockStart, blockEnd, opts.decSpeedBias, false)
	tokens := compressionTokenBuffer(state, blockSize)
	distances := compressionDistanceBuffer(state, blockSize)
	lengths := compressionLengthBuffer(state, blockSize)
	bitWriter := compressionAdvancedDistanceBitWriter(state, blockSize, advancedDistance)

	out := compressionBlockBuffer(state, CompressBound(blockEnd-blockStart))
	out = writeHeader(out, blockEnd-blockStart, blockCompressed, 0)

	pos := blockStart
	if blockStart == 0 && pos < blockEnd {
		out = append(out, src[pos])
		pos++
	}

	litStart := pos
	const accelerationThreshold = 6
	acceleration := 1 << accelerationThreshold

	for pos < blockEnd {
		rep := findRepeatDistanceMatch(src, pos+1, blockEnd, repOffsets[0], 2, matchDistanceLimit)
		if rep.length >= 4 {
			matchStart := pos + 1
			finder.addLongRepeatPositions(src, pos, blockEnd)
			literals.appendRun(src, litStart, matchStart, repOffsets[0], &tokens, &lengths)
			if advancedDistance {
				appendAdvancedBlockDistance(repOffsets[0], &distances, &bitWriter, &tokens, &lastDistance, &repOffsets)
			} else {
				appendStandardBlockDistance(repOffsets[0], &distances, &tokens, &lastDistance, &repOffsets)
			}
			appendMatchLengthWithMode(rep.length, noHuffman, &tokens, &lengths)
			pos = matchStart + rep.length
			litStart = pos
			acceleration = 1 << accelerationThreshold
			continue
		}

		if advancedDistance && acceleration < (1<<accelerationThreshold)+7 {
			if nextPos, ok := appendLazyAdvancedRepeatMatch(src, litStart, pos, blockEnd, repOffsets[1], 2, matchDistanceLimit, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets, finder); ok {
				pos = nextPos
				litStart = pos
				continue
			}
			if nextPos, ok := appendLazyAdvancedRepeatMatch(src, litStart, pos, blockEnd, repOffsets[2], 2, matchDistanceLimit, &literals, &tokens, &distances, &bitWriter, &lengths, &lastDistance, &repOffsets, finder); ok {
				pos = nextPos
				litStart = pos
				continue
			}
		}

		match := finder.find(src, pos, blockEnd, rep.length)
		if match.length == 0 {
			if rep.length > 0 {
				matchStart := pos + 1
				finder.addShortRepeatPositions(src, matchStart, blockEnd)
				literals.appendRun(src, litStart, matchStart, repOffsets[0], &tokens, &lengths)
				if advancedDistance {
					appendAdvancedBlockDistance(repOffsets[0], &distances, &bitWriter, &tokens, &lastDistance, &repOffsets)
				} else {
					appendStandardBlockDistance(repOffsets[0], &distances, &tokens, &lastDistance, &repOffsets)
				}
				appendMatchLengthWithMode(rep.length, noHuffman, &tokens, &lengths)
				pos = matchStart + rep.length
				litStart = pos
				acceleration = 1 << accelerationThreshold
				continue
			}

			step := acceleration >> accelerationThreshold
			if step < 1 {
				step = 1
			}
			pos += step
			acceleration++
			continue
		}

		searchPos := pos
		lazySteps := 0
		if next := finder.findLazy1(src, searchPos+1, blockEnd, match.length); next.length > match.length {
			match = next
			lazySteps = 1
		}
		if next := finder.findLazy2(src, searchPos+2, blockEnd, match.length); next.length > match.length {
			match = next
			lazySteps = 2
		}

		pos += lazySteps
		finder.addAfterMatch(src, searchPos, pos, match.length, blockEnd)

		for pos > litStart && match.pos > 0 && src[pos-1] == src[match.pos-1] {
			pos--
			match.pos--
			match.length++
		}

		literals.appendRun(src, litStart, pos, repOffsets[0], &tokens, &lengths)
		distance := pos - match.pos
		if advancedDistance {
			appendAdvancedBlockDistance(distance, &distances, &bitWriter, &tokens, &lastDistance, &repOffsets)
		} else {
			appendStandardBlockDistance(distance, &distances, &tokens, &lastDistance, &repOffsets)
		}
		appendMatchLengthWithMode(match.length, noHuffman, &tokens, &lengths)
		pos += match.length
		litStart = pos
		acceleration = 1 << accelerationThreshold
	}

	if litStart < blockEnd {
		literals.appendRun(src, litStart, blockEnd, repOffsets[0], &tokens, &lengths)
	}

	out = literals.appendEncoded(out, opts.decSpeedBias)
	out = appendEntropy(out, tokens, 0, opts.decSpeedBias)
	distanceFlags := 0
	if advancedDistance {
		distanceFlags = streamDistanceAdvanced
	}
	out = appendEntropy(out, distances, distanceFlags, opts.decSpeedBias)
	if advancedDistance {
		out = bitWriter.appendTo(out)
	}
	out = appendEntropy(out, lengths, 0, opts.decSpeedBias)

	if state != nil {
		state.lastDistance = lastDistance
		state.repOffsets = repOffsets
		keepCompressionBlockBuffer(state, out)
		keepCompressionStreamBuffers(state, &literals, tokens, distances, &bitWriter, lengths)
	}
	return blockEncoding{data: out, lastDistance: lastDistance}
}

func appendAdvancedRepeatMatch(src []byte, litStart, pos, blockEnd, distance, minLen, maxDistance int, literals *blockLiterals, tokens, distances *[]byte, bitWriter *advancedDistanceBitWriter, lengths *[]byte, lastDistance *int, repOffsets *[3]int) (int, bool) {
	if distance > 0 && distance <= maxDistance && pos-distance >= 0 && pos+minLen <= blockEnd && src[pos] != src[pos-distance] {
		return pos, false
	}
	rep := findRepeatDistanceMatch(src, pos, blockEnd, distance, minLen, maxDistance)
	if rep.length == 0 {
		return pos, false
	}
	literals.appendRun(src, litStart, pos, repOffsets[0], tokens, lengths)
	appendAdvancedBlockDistance(distance, distances, bitWriter, tokens, lastDistance, repOffsets)
	appendMatchLength(rep.length, tokens, lengths)
	return pos + rep.length, true
}

func appendAdvancedRepeatMatchLevel0(src []byte, litStart, pos, blockEnd, distance, maxDistance int, literals *blockLiterals, tokens, distances *[]byte, bitWriter *advancedDistanceBitWriter, lengths *[]byte, lastDistance *int, repOffsets *[3]int) (int, bool) {
	if distance <= 0 || distance > maxDistance || pos-distance < 0 || pos+4 > blockEnd {
		return pos, false
	}
	prev := pos - distance
	if src[pos] != src[prev] {
		return pos, false
	}
	length := commonMatchLengthUnchecked(src, pos, prev, blockEnd)
	if length < 4 {
		return pos, false
	}
	literals.appendRunLevel0(src, litStart, pos, repOffsets[0], tokens, lengths)
	appendAdvancedBlockDistance(distance, distances, bitWriter, tokens, lastDistance, repOffsets)
	appendMatchLength(length, tokens, lengths)
	return pos + length, true
}

func appendLazyAdvancedRepeatMatch(src []byte, litStart, pos, blockEnd, distance, minLen, maxDistance int, literals *blockLiterals, tokens, distances *[]byte, bitWriter *advancedDistanceBitWriter, lengths *[]byte, lastDistance *int, repOffsets *[3]int, finder levelLazyBlockFinder) (int, bool) {
	if distance > 0 && distance <= maxDistance && pos-distance >= 0 && pos+minLen <= blockEnd && src[pos] != src[pos-distance] {
		return pos, false
	}
	rep := findRepeatDistanceMatch(src, pos, blockEnd, distance, minLen, maxDistance)
	if rep.length == 0 {
		return pos, false
	}
	finder.addAdvancedRepeatPositions(src, pos, blockEnd)
	literals.appendRun(src, litStart, pos, repOffsets[0], tokens, lengths)
	appendAdvancedBlockDistance(distance, distances, bitWriter, tokens, lastDistance, repOffsets)
	appendMatchLength(rep.length, tokens, lengths)
	return pos + rep.length, true
}

func normalizeCompressorLevelOptions(opts compressorLevelOptions) compressorLevelOptions {
	defaults := compressorLevelOptionsForLevel(opts.level, opts.decSpeedBias)
	if opts.hashLog <= 0 {
		opts.parser = defaults.parser
	}
	if opts.parser != compressorParserUltraFast && opts.parser != compressorParserGreedy && opts.parser != compressorParserLazyFast &&
		opts.parser != compressorParserLazy && opts.parser != compressorParserOptimal1 && opts.parser != compressorParserOptimal2 &&
		opts.parser != compressorParserOptimal3 {
		opts.parser = defaults.parser
	}
	if opts.windowLog <= 0 {
		opts.windowLog = defaults.windowLog
	}
	if opts.windowLog <= 0 {
		opts.windowLog = 24
	}
	if opts.windowLog < 6 {
		opts.windowLog = 6
	}
	if opts.windowLog > 31 {
		opts.windowLog = 31
	}
	if opts.hashLog <= 0 {
		opts.hashLog = defaults.hashLog
	}
	if opts.hashBytes <= 0 {
		opts.hashBytes = defaults.hashBytes
	}
	if opts.minMatchLength <= 0 {
		opts.minMatchLength = defaults.minMatchLength
	}
	if (opts.parser == compressorParserLazy || opts.parser == compressorParserOptimal1 || opts.parser == compressorParserOptimal2 ||
		opts.parser == compressorParserOptimal3) && opts.hashEntriesLog <= 0 {
		opts.hashEntriesLog = defaults.hashEntriesLog
	}
	if opts.niceLength <= 0 {
		opts.niceLength = defaults.niceLength
	}
	if opts.optimalBlockSize <= 0 {
		opts.optimalBlockSize = defaults.optimalBlockSize
	}
	if opts.maxArrivals <= 0 {
		opts.maxArrivals = defaults.maxArrivals
	}
	if opts.parserIterations <= 0 {
		opts.parserIterations = defaults.parserIterations
	}
	if opts.hashEntriesLog < 0 {
		opts.hashEntriesLog = defaults.hashEntriesLog
	}
	if opts.hashLog > 27 {
		opts.hashLog = 27
	}
	if opts.hashEntriesLog > 7 {
		opts.hashEntriesLog = 7
	}
	if opts.maxArrivals > 16 {
		opts.maxArrivals = 16
	}
	if opts.parserIterations > 3 {
		opts.parserIterations = 3
	}
	if opts.decSpeedBias < 0 {
		opts.decSpeedBias = 0
	}
	if opts.decSpeedBias > 1 {
		opts.decSpeedBias = 1
	}
	return opts
}

func maxMatchDistance(opts compressorLevelOptions) int {
	if opts.windowLog <= 0 {
		opts.windowLog = 24
	}
	limit := maxDistance
	if opts.windowLog < 31 {
		limit = (1 << opts.windowLog) - 1
	}
	if opts.decSpeedBias >= 0.1 {
		limit = min(limit, maxStdDistance)
	}
	if limit < 1 {
		return 1
	}
	return limit
}

func newLevelHashMatchFinder(opts compressorLevelOptions) *levelHashMatchFinder {
	hashLog := effectiveLevelHashLog(opts)
	size := 1 << hashLog
	table := acquireIntTable(size)
	hashMask, hashLeftShift := hashWindow(opts.hashBytes)
	return &levelHashMatchFinder{
		table:          table,
		hashBytes:      opts.hashBytes,
		hashShift:      uint(64 - hashLog),
		hashMask:       hashMask,
		hashLeftShift:  hashLeftShift,
		minMatchLength: opts.minMatchLength,
		maxDistance:    maxMatchDistance(opts),
	}
}

func acquireIntTable(size int) []int {
	if size <= 0 || size&(size-1) != 0 {
		return make([]int, size)
	}
	log := bits.TrailingZeros(uint(size))
	if log > maxPooledIntTableLog {
		return make([]int, size)
	}
	select {
	case table := <-pooledIntTables[log]:
		return table[:size]
	default:
	}
	return make([]int, size)
}

func releaseIntTable(table []int) {
	size := len(table)
	if size <= 0 || size&(size-1) != 0 {
		return
	}
	log := bits.TrailingZeros(uint(size))
	if log > maxPooledIntTableLog {
		return
	}
	clear(table)
	table = table[:size]
	select {
	case pooledIntTables[log] <- table:
	default:
	}
}

func acquireByteBuffer(size int) []byte {
	if size <= 0 {
		return nil
	}
	log := bits.Len(uint(size - 1))
	if log > maxPooledByteBufferLog {
		return make([]byte, 0, size)
	}
	select {
	case buf := <-pooledByteBuffers[log]:
		return buf[:0]
	default:
	}
	return make([]byte, 0, 1<<log)
}

func releaseByteBuffer(buf []byte) {
	capacity := cap(buf)
	if capacity <= 0 || capacity&(capacity-1) != 0 {
		return
	}
	log := bits.TrailingZeros(uint(capacity))
	if log > maxPooledByteBufferLog {
		return
	}
	buf = buf[:0]
	select {
	case pooledByteBuffers[log] <- buf:
	default:
	}
}

func acquireUint32Buffer(size int) []uint32 {
	if size <= 0 {
		return nil
	}
	log := bits.Len(uint(size - 1))
	if log > maxPooledUint32BufferLog {
		return make([]uint32, size)
	}
	select {
	case buf := <-pooledUint32Buffers[log]:
		return buf[:size]
	default:
	}
	return make([]uint32, size, 1<<log)
}

func releaseUint32Buffer(buf []uint32) {
	capacity := cap(buf)
	if capacity <= 0 || capacity&(capacity-1) != 0 {
		return
	}
	log := bits.TrailingZeros(uint(capacity))
	if log > maxPooledUint32BufferLog {
		return
	}
	buf = buf[:0]
	select {
	case pooledUint32Buffers[log] <- buf:
	default:
	}
}

func acquireOptimalParseStates(size int) []optimalParseState {
	if size <= 0 {
		return nil
	}
	log := bits.Len(uint(size - 1))
	if log > maxPooledOptimalParseStateLog {
		return make([]optimalParseState, size)
	}
	select {
	case states := <-pooledOptimalParseStateBuffers[log]:
		return states[:size]
	default:
	}
	return make([]optimalParseState, size, 1<<log)
}

func releaseOptimalParseStates(states []optimalParseState) {
	capacity := cap(states)
	if capacity <= 0 || capacity&(capacity-1) != 0 {
		return
	}
	log := bits.TrailingZeros(uint(capacity))
	if log > maxPooledOptimalParseStateLog {
		return
	}
	states = states[:0]
	select {
	case pooledOptimalParseStateBuffers[log] <- states:
	default:
	}
}

func acquireOptimalMatchSteps(size int) []optimalMatchStep {
	if size <= 0 {
		return nil
	}
	log := bits.Len(uint(size - 1))
	if log > maxPooledOptimalMatchStepLog {
		return make([]optimalMatchStep, 0, size)
	}
	select {
	case steps := <-pooledOptimalMatchStepBuffers[log]:
		return steps[:0]
	default:
	}
	return make([]optimalMatchStep, 0, 1<<log)
}

func releaseOptimalMatchSteps(steps []optimalMatchStep) {
	capacity := cap(steps)
	if capacity <= 0 || capacity&(capacity-1) != 0 {
		return
	}
	log := bits.TrailingZeros(uint(capacity))
	if log > maxPooledOptimalMatchStepLog {
		return
	}
	steps = steps[:0]
	select {
	case pooledOptimalMatchStepBuffers[log] <- steps:
	default:
	}
}

func (f *levelHashMatchFinder) release() {
	if f == nil {
		return
	}
	releaseIntTable(f.table)
	f.table = nil
}

func (f *levelHashMatchFinder) reset() {
	if f != nil {
		clear(f.table)
	}
}

func newLevelLazyFastMatchFinder(opts compressorLevelOptions) *levelLazyFastMatchFinder {
	hash4Options := opts
	hash4Options.hashBytes = 4
	hash4Options.minMatchLength = 4
	hash8Options := opts
	hash8Options.hashBytes = 8
	hash8Options.minMatchLength = 8
	return &levelLazyFastMatchFinder{
		hash4: newLevelHashMatchFinder(hash4Options),
		hash8: newLevelHashMatchFinder(hash8Options),
	}
}

func (f *levelLazyFastMatchFinder) release() {
	if f == nil {
		return
	}
	if f.hash4 != nil {
		f.hash4.release()
		f.hash4 = nil
	}
	if f.hash8 != nil {
		f.hash8.release()
		f.hash8 = nil
	}
}

func (f *levelLazyFastMatchFinder) reset() {
	if f == nil {
		return
	}
	if f.hash4 != nil {
		f.hash4.reset()
	}
	if f.hash8 != nil {
		f.hash8.reset()
	}
}

func newLevelCacheMatchFinder(opts compressorLevelOptions, hashBytes, minMatchLength int) *levelCacheMatchFinder {
	entries := 1 << cacheFinderEntriesLog(opts)
	hashLog := effectiveLevelHashLog(opts)
	size := 1 << hashLog
	table := acquireIntTable(size * entries)
	hashMask, hashLeftShift := hashWindow(hashBytes)
	return &levelCacheMatchFinder{
		table:          table,
		hashShift:      uint(64 - hashLog),
		hashMask:       hashMask,
		hashLeftShift:  hashLeftShift,
		minMatchLength: minMatchLength,
		entries:        entries,
		maxDistance:    maxMatchDistance(opts),
	}
}

func (f *levelCacheMatchFinder) release() {
	if f == nil {
		return
	}
	releaseIntTable(f.table)
	f.table = nil
}

func (f *levelCacheMatchFinder) reset() {
	if f != nil {
		clear(f.table)
	}
}

func effectiveLevelHashLog(opts compressorLevelOptions) int {
	hashLog := opts.hashLog
	if opts.windowLog > 0 {
		hashLog = min(hashLog, opts.windowLog-3)
	}
	if hashLog > 20 {
		hashLog = 20
	}
	if hashLog < 1 {
		hashLog = 1
	}
	return hashLog
}

func cacheFinderEntriesLog(opts compressorLevelOptions) int {
	entriesLog := opts.hashEntriesLog
	if entriesLog > 4 {
		entriesLog = 4
	}
	if entriesLog < 0 {
		entriesLog = 0
	}
	return entriesLog
}

func newLevelLazyMatchFinder(opts compressorLevelOptions) *levelLazyMatchFinder {
	return &levelLazyMatchFinder{
		hash4: newLevelCacheMatchFinder(opts, 4, 4),
		hash8: newLevelCacheMatchFinder(opts, 8, 8),
	}
}

func (f *levelLazyMatchFinder) release() {
	if f == nil {
		return
	}
	if f.hash4 != nil {
		f.hash4.release()
		f.hash4 = nil
	}
	if f.hash8 != nil {
		f.hash8.release()
		f.hash8 = nil
	}
}

func (f *levelLazyMatchFinder) reset() {
	if f == nil {
		return
	}
	if f.hash4 != nil {
		f.hash4.reset()
	}
	if f.hash8 != nil {
		f.hash8.reset()
	}
}

func newLevelOptimalMatchFinder(opts compressorLevelOptions) levelOptimalMatchFinder {
	effectiveHashLog := effectiveLevelHashLog(opts)
	opts.hashLog = effectiveHashLog
	hash3Options := opts
	hash3Options.hashLog = min(effectiveHashLog, 14)
	hash3Options.hashBytes = 3
	hash3Options.minMatchLength = 3
	return levelOptimalMatchFinder{
		hash3:   newLevelHashMatchFinder(hash3Options),
		hash4:   newLevelCacheMatchFinder(opts, 4, 4),
		hash8:   newLevelCacheMatchFinder(opts, 8, 8),
		matches: make([]levelMatch, 0, 1+(1<<cacheFinderEntriesLog(opts))*2),
	}
}

func (f *levelOptimalMatchFinder) release() {
	if f == nil {
		return
	}
	if f.hash3 != nil {
		f.hash3.release()
		f.hash3 = nil
	}
	if f.hash4 != nil {
		f.hash4.release()
		f.hash4 = nil
	}
	if f.hash8 != nil {
		f.hash8.release()
		f.hash8 = nil
	}
	f.matches = nil
	f.states = nil
	f.stepScratch = nil
}

func (f *levelOptimalMatchFinder) reset() {
	if f == nil {
		return
	}
	if f.hash3 != nil {
		f.hash3.reset()
	}
	if f.hash4 != nil {
		f.hash4.reset()
	}
	if f.hash8 != nil {
		f.hash8.reset()
	}
	f.matches = f.matches[:0]
	f.stepScratch = f.stepScratch[:0]
}

func (f *levelOptimalMatchFinder) parseStates(size int) []optimalParseState {
	if size <= 0 {
		return nil
	}
	if cap(f.states) < size {
		f.states = make([]optimalParseState, size)
	}
	return f.states[:size]
}

func newBinaryMatchFinder(blockStart, blockEnd int, opts compressorLevelOptions) *binaryMatchFinder {
	binaryTreeWindow := min(opts.hashLog, opts.windowLog)
	blockSize := blockEnd - blockStart
	if blockSize < 1 {
		blockSize = 1
	}
	nodeListSize := blockSize
	directNodes := true
	if binaryTreeWindow < bits.UintSize-1 {
		windowSize := 1 << binaryTreeWindow
		if blockSize >= windowSize {
			nodeListSize = windowSize
			directNodes = false
		}
	}
	if nodeListSize < 1 {
		nodeListSize = 1
	}

	chain3HashLog := min(14, max(opts.windowLog-3, 1))
	chain3EntriesLog := 0
	if opts.parser == compressorParserOptimal3 {
		chain3HashLog = min(16, max(opts.windowLog-3, 1))
		chain3EntriesLog = max(opts.hashEntriesLog-4, 0)
	}
	nodeLookupHashLog := min(20, max(opts.windowLog-3, 1))

	return &binaryMatchFinder{
		chain3:          newBinaryCacheTable(chain3HashLog, chain3EntriesLog),
		nodeLookup:      make([]uint32, 1<<nodeLookupHashLog),
		nodeLookupShift: uint(64 - nodeLookupHashLog),
		nodes:           make([]uint32, nodeListSize*2),
		base:            blockStart,
		nodeListSize:    nodeListSize,
		directNodes:     directNodes,
	}
}

func (f *binaryMatchFinder) release() {
	if f == nil {
		return
	}
	f.chain3.table = nil
	f.nodeLookup = nil
	f.nodes = nil
}

func (f *binaryMatchFinder) reset() {
	if f == nil {
		return
	}
	f.chain3.reset()
	clear(f.nodeLookup)
	clear(f.nodes)
}

func newBinaryCacheTable(hashLog, entriesLog int) binaryCacheTable {
	if hashLog < 1 {
		hashLog = 1
	}
	if entriesLog < 0 {
		entriesLog = 0
	}
	entries := 1 << entriesLog
	return binaryCacheTable{
		table:     make([]uint32, (1<<hashLog)*entries),
		entries:   entries,
		hashShift: uint(64 - hashLog),
	}
}

func (t binaryCacheTable) reset() {
	clear(t.table)
}

func (t binaryCacheTable) bucket(hash uint64) int {
	return hashUintShift(hash, t.hashShift) * t.entries
}

func (t binaryCacheTable) push(hash uint64, pos int) {
	start := t.bucket(hash)
	for i := t.entries - 1; i > 0; i-- {
		t.table[start+i] = t.table[start+i-1]
	}
	t.table[start] = uint32(pos)
}

func (t binaryCacheTable) updateAndVisit(hash uint64, pos int, visit func(prev int)) {
	start := t.bucket(hash)
	current := uint32(pos)
	for i := 0; i < t.entries; i++ {
		prev := t.table[start+i]
		t.table[start+i] = current
		current = prev
		if prev != 0 {
			visit(int(prev))
		}
	}
}

func hashUint(value uint64, tableSize int) int {
	return int((value * 0xff51afd7ed558ccd) >> (64 - bits.TrailingZeros(uint(tableSize))))
}

func hashUintShift(value uint64, hashShift uint) int {
	return int((value * 0xff51afd7ed558ccd) >> hashShift)
}

func readHash3Value(src []byte, pos int) (uint64, bool) {
	if pos < 0 || pos+4 > len(src) {
		return 0, false
	}
	value := binary.LittleEndian.Uint32(src[pos:])
	return uint64(value) << 40, true
}

func readHash4Value(src []byte, pos int) (uint64, bool) {
	if pos < 0 || pos+4 > len(src) {
		return 0, false
	}
	return uint64(binary.LittleEndian.Uint32(src[pos:])), true
}

func (f *binaryMatchFinder) nodeIndex(pos int) int {
	if f.directNodes {
		index := pos - f.base
		if index < 0 || index >= f.nodeListSize {
			return -1
		}
		return index
	}
	return pos & (f.nodeListSize - 1)
}

func (f *binaryMatchFinder) btEnd(pos int) int {
	if pos-f.base < f.nodeListSize {
		return f.base
	}
	return pos - f.nodeListSize
}

func (f *binaryMatchFinder) findLZMatchesAndUpdate(src []byte, pos, inputStart, compressionLimit, blockLimit, minLength int, opts compressorLevelOptions, dst []lzMatch) []lzMatch {
	nextExpectedLength := minLength
	if nextExpectedLength < 3 {
		nextExpectedLength = 3
	}
	matchDistanceLimit := maxMatchDistance(opts)

	if hash3, ok := readHash3Value(src, pos); ok {
		f.chain3.updateAndVisit(hash3, pos, func(prev int) {
			if prev < inputStart || prev >= pos || pos-prev > matchDistanceLimit {
				return
			}
			checkPos := pos + nextExpectedLength - 1
			checkPrev := prev + nextExpectedLength - 1
			if checkPos >= blockLimit || checkPrev >= len(src) || src[checkPos] != src[checkPrev] {
				return
			}
			length := matchLengthAtWindow(src, pos, prev, blockLimit, 3, matchDistanceLimit)
			if length >= nextExpectedLength {
				dst = append(dst, lzMatch{length: length, distance: pos - prev})
				nextExpectedLength = length
			}
		})
	}

	hash4, ok := readHash4Value(src, pos)
	if !ok || len(f.nodeLookup) == 0 {
		return dst
	}
	lookupIndex := hashUintShift(hash4, f.nodeLookupShift)
	backPosition := int(f.nodeLookup[lookupIndex])
	f.nodeLookup[lookupIndex] = uint32(pos)

	currentIndex := f.nodeIndex(pos)
	if currentIndex < 0 {
		return dst
	}
	lesserNode := currentIndex * 2
	greaterNode := lesserNode + 1
	lesserFront := pos
	greaterFront := pos
	btEnd := f.btEnd(pos)
	depth := 1 << opts.hashEntriesLog
	if depth < 1 {
		depth = 1
	}

	for {
		if backPosition <= btEnd || depth == 0 || backPosition >= pos || pos-backPosition > matchDistanceLimit {
			f.nodes[lesserNode] = 0
			f.nodes[greaterNode] = 0
			return dst
		}
		depth--

		front := lesserFront
		if greaterFront < front {
			front = greaterFront
		}
		back := front - (pos - backPosition)
		if back < 0 {
			f.nodes[lesserNode] = 0
			f.nodes[greaterNode] = 0
			return dst
		}

		extraLength := commonMatchLengthUnchecked(src, front, back, compressionLimit)
		front += extraLength
		back += extraLength
		length := front - pos
		effectiveLength := min(length, blockLimit-pos)
		nextIndex := f.nodeIndex(backPosition)
		if nextIndex < 0 {
			f.nodes[lesserNode] = 0
			f.nodes[greaterNode] = 0
			return dst
		}
		nextNode := nextIndex * 2

		if effectiveLength >= nextExpectedLength {
			dst = append(dst, lzMatch{length: effectiveLength, distance: pos - backPosition})
			nextExpectedLength = effectiveLength
		}
		if length >= opts.niceLength || front >= compressionLimit || back >= compressionLimit {
			f.nodes[lesserNode] = f.nodes[nextNode]
			f.nodes[greaterNode] = f.nodes[nextNode+1]
			return dst
		}
		if back < 0 || front < 0 || back >= len(src) || front >= len(src) {
			f.nodes[lesserNode] = 0
			f.nodes[greaterNode] = 0
			return dst
		}

		if src[back] < src[front] {
			f.nodes[lesserNode] = uint32(backPosition)
			lesserNode = nextNode + 1
			backPosition = int(f.nodes[lesserNode])
			lesserFront = front
		} else {
			f.nodes[greaterNode] = uint32(backPosition)
			greaterNode = nextNode
			backPosition = int(f.nodes[greaterNode])
			greaterFront = front
		}
	}
}

func (f *binaryMatchFinder) updatePosition(src []byte, pos, inputStart, compressionLimit int, opts compressorLevelOptions) {
	if hash3, ok := readHash3Value(src, pos); ok {
		f.chain3.push(hash3, pos)
	}
	matchDistanceLimit := maxMatchDistance(opts)
	hash4, ok := readHash4Value(src, pos)
	if !ok || len(f.nodeLookup) == 0 {
		return
	}
	lookupIndex := hashUintShift(hash4, f.nodeLookupShift)
	backPosition := int(f.nodeLookup[lookupIndex])
	f.nodeLookup[lookupIndex] = uint32(pos)

	currentIndex := f.nodeIndex(pos)
	if currentIndex < 0 {
		return
	}
	lesserNode := currentIndex * 2
	greaterNode := lesserNode + 1
	lesserFront := pos
	greaterFront := pos
	positionSkip := min(compressionLimit, pos+opts.niceLength)
	btEnd := f.btEnd(pos)
	depth := 1 << opts.hashEntriesLog
	if depth < 1 {
		depth = 1
	}

	for {
		if backPosition <= btEnd || depth == 0 || backPosition >= pos || pos-backPosition > matchDistanceLimit {
			f.nodes[lesserNode] = 0
			f.nodes[greaterNode] = 0
			return
		}
		depth--

		front := lesserFront
		if greaterFront < front {
			front = greaterFront
		}
		back := front - (pos - backPosition)
		if back < 0 {
			f.nodes[lesserNode] = 0
			f.nodes[greaterNode] = 0
			return
		}
		length := commonMatchLengthUnchecked(src, front, back, positionSkip)
		front += length
		back += length
		nextIndex := f.nodeIndex(backPosition)
		if nextIndex < 0 {
			f.nodes[lesserNode] = 0
			f.nodes[greaterNode] = 0
			return
		}
		nextNode := nextIndex * 2
		if front >= positionSkip {
			f.nodes[lesserNode] = f.nodes[nextNode]
			f.nodes[greaterNode] = f.nodes[nextNode+1]
			return
		}
		if back < 0 || front < 0 || back >= len(src) || front >= len(src) {
			f.nodes[lesserNode] = 0
			f.nodes[greaterNode] = 0
			return
		}
		if src[back] < src[front] {
			f.nodes[lesserNode] = uint32(backPosition)
			lesserNode = nextNode + 1
			backPosition = int(f.nodes[lesserNode])
			lesserFront = front
		} else {
			f.nodes[greaterNode] = uint32(backPosition)
			greaterNode = nextNode
			backPosition = int(f.nodes[greaterNode])
			greaterFront = front
		}
	}
}

func commonMatchLengthUnchecked(src []byte, front, back, limit int) int {
	// Callers only use this after proving back < front and limit <= len(src).
	maxLength := limit - front
	length := 0
	for length+8 <= maxLength {
		diff := binary.LittleEndian.Uint64(src[front+length:]) ^ binary.LittleEndian.Uint64(src[back+length:])
		if diff != 0 {
			return length + bits.TrailingZeros64(diff)/8
		}
		length += 8
	}
	for length < maxLength && src[front+length] == src[back+length] {
		length++
	}
	return length
}

func newMatchBuffer(src []byte, blockStart, blockEnd, compressionLimit int, opts compressorLevelOptions, finder *binaryMatchFinder) *matchBuffer {
	return fillMatchBuffer(src, blockStart, blockEnd, compressionLimit, opts, finder, &matchBuffer{}, nil)
}

func fillMatchBuffer(src []byte, blockStart, blockEnd, compressionLimit int, opts compressorLevelOptions, finder *binaryMatchFinder, buffer *matchBuffer, scratch *[]lzMatch) *matchBuffer {
	blockSize := blockEnd - blockStart
	if blockSize <= 0 {
		buffer.matches = buffer.matches[:0]
		buffer.counts = buffer.counts[:0]
		buffer.blockStart = blockStart
		buffer.maxPerPos = 1
		return buffer
	}
	maxPerPos := (1 << opts.hashEntriesLog) + 1
	if maxPerPos > 4 {
		maxPerPos = 4
	}
	if maxPerPos < 1 {
		maxPerPos = 1
	}
	matchSlots := blockSize * maxPerPos
	if cap(buffer.matches) < matchSlots {
		buffer.matches = make([]lzMatch, matchSlots)
	} else {
		buffer.matches = buffer.matches[:matchSlots]
	}
	if cap(buffer.counts) < blockSize {
		buffer.counts = make([]int, blockSize)
	} else {
		buffer.counts = buffer.counts[:blockSize]
		clear(buffer.counts)
	}
	buffer.maxPerPos = maxPerPos
	buffer.blockStart = blockStart

	const accelerationThreshold = 6
	acceleration := 1 << accelerationThreshold
	pos := blockStart
	if blockStart == 0 {
		pos++
	}
	var matches []lzMatch
	if scratch != nil {
		matches = (*scratch)[:0]
	}
	for pos < blockEnd {
		matches = matches[:0]
		matches = finder.findLZMatchesAndUpdate(src, pos, 0, compressionLimit, blockEnd, 3, opts, matches)
		rel := pos - blockStart
		count := min(len(matches), maxPerPos)
		if count > 0 {
			copy(buffer.matches[rel*maxPerPos:rel*maxPerPos+count], matches[len(matches)-count:])
			buffer.counts[rel] = count
			best := matches[len(matches)-1]
			if best.length >= opts.niceLength {
				fillLength := best.length - 1
				updateEnd := min(blockEnd, pos+min(opts.niceLength, best.length))
				for updatePos := pos + 1; updatePos < updateEnd; updatePos++ {
					finder.updatePosition(src, updatePos, 0, compressionLimit, opts)
				}
				pos++
				for fillLength > 0 && pos < blockEnd {
					rel = pos - blockStart
					if fillLength > 2 {
						buffer.matches[rel*maxPerPos] = lzMatch{length: fillLength, distance: best.distance}
						buffer.counts[rel] = 1
					}
					pos++
					fillLength--
				}
				continue
			}
			if best.length >= 4 {
				acceleration = 1 << accelerationThreshold
			}
		} else if opts.parser != compressorParserOptimal3 {
			acceleration++
		}

		pos++
		for skip := 1; skip < acceleration>>accelerationThreshold && pos < blockEnd; skip++ {
			pos++
		}
	}
	if scratch != nil {
		*scratch = matches[:0]
	}
	return buffer
}

func (b *matchBuffer) findLZMatchesAndUpdate(src []byte, pos, inputStart, compressionLimit, blockLimit, minLength int, opts compressorLevelOptions, dst []lzMatch) []lzMatch {
	rel := pos - b.blockStart
	if rel < 0 || rel >= len(b.counts) {
		return dst
	}
	start := rel * b.maxPerPos
	count := b.counts[rel]
	for i := 0; i < count; i++ {
		match := b.matches[start+i]
		if match.length >= minLength {
			dst = append(dst, match)
		}
	}
	return dst
}

func (f *levelHashMatchFinder) accelerationThreshold(parser compressorLevelParser) uint {
	if parser == compressorParserUltraFast {
		return 4
	}
	if parser == compressorParserLazyFast {
		return 6
	}
	return 5
}

func (f *levelHashMatchFinder) findAndUpdate(src []byte, pos, blockEnd int) (int, int) {
	if pos < 0 || pos+8 > len(src) || pos+f.minMatchLength > blockEnd {
		return 0, 0
	}
	value := binary.LittleEndian.Uint64(src[pos:])
	h := f.hashFromValue(value)
	prev := f.table[h]
	f.table[h] = pos
	// pos is non-negative after the guard above, so uint also rejects negative prev.
	if uint(prev) >= uint(pos) || pos-prev > f.maxDistance {
		return 0, 0
	}
	var length int
	if pos+8 <= blockEnd {
		diff := value ^ binary.LittleEndian.Uint64(src[prev:])
		if diff != 0 {
			length = bits.TrailingZeros64(diff) / 8
		} else {
			length = 8 + commonMatchLengthUnchecked(src, pos+8, prev+8, blockEnd)
		}
	} else {
		length = commonMatchLengthUnchecked(src, pos, prev, blockEnd)
	}
	if length < f.minMatchLength {
		return 0, 0
	}
	return prev, length
}

func (f *levelHashMatchFinder) findAndUpdateLevel0(src []byte, pos, blockEnd int) (int, int) {
	// The level 0 compression loop only calls this with non-negative positions.
	if pos+8 > len(src) || pos+6 > blockEnd {
		return 0, 0
	}
	value := binary.LittleEndian.Uint64(src[pos:])
	h := hashLevel6Value(value, f.hashShift)
	prev := f.table[h]
	f.table[h] = pos
	if uint(prev) >= uint(pos) || pos-prev > f.maxDistance {
		return 0, 0
	}
	var length int
	if pos+8 <= blockEnd {
		const match6Mask = uint64(0x0000ffffffffffff)
		diff := value ^ binary.LittleEndian.Uint64(src[prev:])
		if diff&match6Mask != 0 {
			return 0, 0
		}
		if diff == 0 {
			return prev, 8 + commonMatchLengthUnchecked(src, pos+8, prev+8, blockEnd)
		}
		return prev, bits.TrailingZeros64(diff) / 8
	} else {
		length = commonMatchLengthUnchecked(src, pos, prev, blockEnd)
	}
	if length < 6 {
		return 0, 0
	}
	return prev, length
}

func (f *levelHashMatchFinder) addLevel0(src []byte, pos, blockEnd int) {
	if pos >= 0 && pos+8 <= len(src) && pos+6 <= blockEnd {
		f.table[hashLevel6Value(binary.LittleEndian.Uint64(src[pos:]), f.hashShift)] = pos
	}
}

func (f *levelHashMatchFinder) addAfterMatchLevel0(src []byte, pos, matchLen, blockEnd int) {
	if matchLen <= 1 {
		return
	}
	addPos := pos + 1
	if matchLen > 2 && addPos+9 <= len(src) && addPos+7 <= blockEnd {
		value := binary.LittleEndian.Uint64(src[addPos:])
		f.table[hashLevel6Value(value, f.hashShift)] = addPos
		nextValue := (value >> 8) | uint64(src[addPos+8])<<56
		f.table[hashLevel6Value(nextValue, f.hashShift)] = addPos + 1
		return
	}
	f.addLevel0(src, addPos, blockEnd)
	if matchLen > 2 {
		f.addLevel0(src, addPos+1, blockEnd)
	}
}

func (f *levelHashMatchFinder) addRepeatPositionsLevel0(src []byte, pos, blockEnd int) {
	if pos >= 0 && pos+9 <= len(src) && pos+7 <= blockEnd {
		value := binary.LittleEndian.Uint64(src[pos:])
		f.table[hashLevel6Value(value, f.hashShift)] = pos
		nextValue := (value >> 8) | uint64(src[pos+8])<<56
		f.table[hashLevel6Value(nextValue, f.hashShift)] = pos + 1
		return
	}
	f.addLevel0(src, pos, blockEnd)
	f.addLevel0(src, pos+1, blockEnd)
}

func (f *levelHashMatchFinder) candidateAndUpdate(src []byte, pos, blockEnd int) (int, bool) {
	if !f.canHash(src, pos, blockEnd) {
		return 0, false
	}
	return f.candidateAndUpdateHashable(src, pos)
}

func (f *levelHashMatchFinder) candidateAndUpdateHashable(src []byte, pos int) (int, bool) {
	h := f.hash(src, pos)
	prev := f.table[h]
	f.table[h] = pos
	if prev < 0 || prev >= pos || pos-prev > f.maxDistance {
		return 0, false
	}
	return prev, true
}

func (f *levelHashMatchFinder) add(src []byte, pos, blockEnd int) {
	if f.canHash(src, pos, blockEnd) {
		f.addHashable(src, pos)
	}
}

func (f *levelHashMatchFinder) addHashable(src []byte, pos int) {
	f.table[f.hash(src, pos)] = pos
}

func (f *levelHashMatchFinder) addAfterMatch(src []byte, pos, matchLen, blockEnd int) {
	limit := 2
	if f.hashBytes == 5 {
		limit = 4
	}
	for i := 1; i <= limit && i < matchLen; i++ {
		f.add(src, pos+i, blockEnd)
	}
}

func (f *levelHashMatchFinder) addRepeatPositions(src []byte, pos, blockEnd int) {
	if pos >= 0 && pos+9 <= len(src) && pos+1+f.minMatchLength <= blockEnd {
		value := binary.LittleEndian.Uint64(src[pos:])
		f.table[f.hashFromValue(value)] = pos
		nextValue := (value >> 8) | uint64(src[pos+8])<<56
		f.table[f.hashFromValue(nextValue)] = pos + 1
		return
	}
	f.add(src, pos, blockEnd)
	f.add(src, pos+1, blockEnd)
}

func (f *levelHashMatchFinder) canHash(src []byte, pos, blockEnd int) bool {
	return pos >= 0 && pos+8 <= len(src) && pos+f.minMatchLength <= blockEnd
}

func (f *levelHashMatchFinder) hash(src []byte, pos int) int {
	return f.hashFromValue(binary.LittleEndian.Uint64(src[pos:]))
}

func (f *levelHashMatchFinder) hashFromValue(value uint64) int {
	if f.hashLeftShift != 0 {
		value <<= f.hashLeftShift
	} else {
		value &= f.hashMask
	}
	return hashUintShift(value, f.hashShift)
}

func hashLevel6Value(value uint64, hashShift uint) int {
	return hashUintShift(value<<16, hashShift)
}

func hashWindow(hashBytes int) (uint64, uint) {
	switch hashBytes {
	case 3:
		return 0x0000000000ffffff, 40
	case 4:
		return 0x00000000ffffffff, 0
	case 5:
		return 0x000000ffffffffff, 24
	case 6:
		return 0x0000ffffffffffff, 16
	case 7:
		return 0x00ffffffffffffff, 8
	default:
		return ^uint64(0), 0
	}
}

func hashLevelBytes(src []byte, pos, hashBytes int, hashShift uint) int {
	value := binary.LittleEndian.Uint64(src[pos:])
	switch hashBytes {
	case 3:
		value <<= 40
	case 4:
		value &= 0xffffffff
	case 5:
		value <<= 24
	case 6:
		value <<= 16
	case 7:
		value <<= 8
	}
	return int((value * 0xff51afd7ed558ccd) >> hashShift)
}

func (f *levelCacheMatchFinder) canHash(src []byte, pos, blockEnd int) bool {
	return pos >= 0 && pos+8 <= len(src) && pos+f.minMatchLength <= blockEnd
}

func (f *levelCacheMatchFinder) hash(src []byte, pos int) int {
	value := (binary.LittleEndian.Uint64(src[pos:]) & f.hashMask) << f.hashLeftShift
	return hashUintShift(value, f.hashShift)
}

func (f *levelCacheMatchFinder) hashFromValue(value uint64) int {
	return hashUintShift((value&f.hashMask)<<f.hashLeftShift, f.hashShift)
}

func (f *levelCacheMatchFinder) push(src []byte, pos, blockEnd int) {
	if !f.canHash(src, pos, blockEnd) {
		return
	}
	f.pushFast(src, pos)
}

func (f *levelCacheMatchFinder) pushFast(src []byte, pos int) {
	f.pushHash(f.hash(src, pos), pos)
}

func (f *levelCacheMatchFinder) pushHash(hash, pos int) {
	start := hash * f.entries
	table := f.table
	switch f.entries {
	case 1:
	case 2:
		table[start+1] = table[start]
	case 4:
		table[start+3] = table[start+2]
		table[start+2] = table[start+1]
		table[start+1] = table[start]
	case 8:
		table[start+7] = table[start+6]
		table[start+6] = table[start+5]
		table[start+5] = table[start+4]
		table[start+4] = table[start+3]
		table[start+3] = table[start+2]
		table[start+2] = table[start+1]
		table[start+1] = table[start]
	case 16:
		table[start+15] = table[start+14]
		table[start+14] = table[start+13]
		table[start+13] = table[start+12]
		table[start+12] = table[start+11]
		table[start+11] = table[start+10]
		table[start+10] = table[start+9]
		table[start+9] = table[start+8]
		table[start+8] = table[start+7]
		table[start+7] = table[start+6]
		table[start+6] = table[start+5]
		table[start+5] = table[start+4]
		table[start+4] = table[start+3]
		table[start+3] = table[start+2]
		table[start+2] = table[start+1]
		table[start+1] = table[start]
	default:
		for i := f.entries - 1; i > 0; i-- {
			table[start+i] = table[start+i-1]
		}
	}
	table[start] = pos
}

func (f *levelCacheMatchFinder) forEachCandidateAndUpdate(src []byte, pos, blockEnd int, visit func(prev int)) {
	if !f.canHash(src, pos, blockEnd) {
		return
	}
	start := f.hash(src, pos) * f.entries
	current := pos
	for i := 0; i < f.entries; i++ {
		prev := f.table[start+i]
		f.table[start+i] = current
		current = prev
		if prev >= 0 && prev < pos && pos-prev <= f.maxDistance {
			visit(prev)
		}
	}
}

func (f *levelCacheMatchFinder) candidatesAndUpdate(src []byte, pos, blockEnd int, candidates []int) {
	for i := range candidates {
		candidates[i] = -1
	}
	if !f.canHash(src, pos, blockEnd) {
		return
	}
	start := f.hash(src, pos) * f.entries
	current := pos
	for i := 0; i < f.entries && i < len(candidates); i++ {
		prev := f.table[start+i]
		f.table[start+i] = current
		current = prev
		if prev >= 0 && prev < pos && pos-prev <= f.maxDistance {
			candidates[i] = prev
		}
	}
}

func (f *levelOptimalMatchFinder) updatePosition(src []byte, pos, blockEnd int) {
	if pos >= 0 && pos+8 <= len(src) && pos+8 <= blockEnd {
		value := binary.LittleEndian.Uint64(src[pos:])
		f.hash3.table[f.hash3.hashFromValue(value)] = pos
		if f.hash4.entries == 4 && f.hash8.entries == 4 {
			start4 := f.hash4.hashFromValue(value) * 4
			table4 := f.hash4.table
			table4[start4+3] = table4[start4+2]
			table4[start4+2] = table4[start4+1]
			table4[start4+1] = table4[start4]
			table4[start4] = pos
			start8 := f.hash8.hashFromValue(value) * 4
			table8 := f.hash8.table
			table8[start8+3] = table8[start8+2]
			table8[start8+2] = table8[start8+1]
			table8[start8+1] = table8[start8]
			table8[start8] = pos
			return
		}
		f.hash4.pushHash(f.hash4.hashFromValue(value), pos)
		f.hash8.pushHash(f.hash8.hashFromValue(value), pos)
		return
	}
	f.hash3.add(src, pos, blockEnd)
	f.hash4.push(src, pos, blockEnd)
	f.hash8.push(src, pos, blockEnd)
}

func (f *levelOptimalMatchFinder) findMatchesAndUpdate(src []byte, pos, blockEnd, lastLength int) []levelMatch {
	if lastLength < 0 {
		lastLength = 0
	}
	hash4 := f.hash4
	hash8 := f.hash8
	matches := f.matches[:0]
	bestLength := lastLength
	canHash8 := pos >= 0 && pos+8 <= len(src) && pos+8 <= blockEnd
	var hashValue uint64
	if canHash8 {
		hashValue = binary.LittleEndian.Uint64(src[pos:])
	}
	if bestLength < 3 {
		var prev int
		ok := false
		if canHash8 {
			hash3 := f.hash3
			h := hash3.hashFromValue(hashValue)
			prev = hash3.table[h]
			hash3.table[h] = pos
			ok = prev >= 0 && prev < pos && pos-prev <= hash3.maxDistance
		} else {
			prev, ok = f.hash3.candidateAndUpdate(src, pos, blockEnd)
		}
		if ok {
			length := commonMatchLengthUnchecked(src, pos, prev, blockEnd)
			if length >= 3 && length > bestLength {
				bestLength = length
				matches = append(matches, levelMatch{pos: prev, length: length})
			}
		}
	} else {
		if canHash8 {
			f.hash3.table[f.hash3.hashFromValue(hashValue)] = pos
		} else {
			f.hash3.add(src, pos, blockEnd)
		}
	}

	if canHash8 {
		entries := hash4.entries
		table4 := hash4.table
		table8 := hash8.table
		maxDistance4 := hash4.maxDistance
		maxDistance8 := hash8.maxDistance
		checkPos := pos + bestLength
		checkOK := checkPos < len(src)
		var checkByte byte
		if checkOK {
			checkByte = src[checkPos]
		}
		if entries == 4 && hash8.entries == 4 {
			// Preserve the loop order below while avoiding per-candidate index work.
			start4 := hash4.hashFromValue(hashValue) * 4
			start8 := hash8.hashFromValue(hashValue) * 4

			prev40 := table4[start4]
			prev41 := table4[start4+1]
			prev42 := table4[start4+2]
			prev43 := table4[start4+3]
			table4[start4+3] = prev42
			table4[start4+2] = prev41
			table4[start4+1] = prev40
			table4[start4] = pos

			prev80 := table8[start8]
			prev81 := table8[start8+1]
			prev82 := table8[start8+2]
			prev83 := table8[start8+3]
			table8[start8+3] = prev82
			table8[start8+2] = prev81
			table8[start8+1] = prev80
			table8[start8] = pos

			if !checkOK {
				f.matches = matches
				return matches
			}

			prev := prev80
			if prev80 < 0 || prev80 >= pos || pos-prev80 > maxDistance8 || checkByte != src[prev80+bestLength] {
				if prev40 >= 0 && prev40 < pos && pos-prev40 <= maxDistance4 && checkByte == src[prev40+bestLength] {
					prev = prev40
				} else {
					prev = -1
				}
			}
			if prev >= 0 {
				if length := commonMatchLengthUnchecked(src, pos, prev, blockEnd); length >= 4 && length > bestLength {
					bestLength = length
					checkPos = pos + bestLength
					checkOK = checkPos < len(src)
					matches = append(matches, levelMatch{pos: prev, length: length})
					if !checkOK {
						f.matches = matches
						return matches
					}
					checkByte = src[checkPos]
				}
			}

			prev = prev81
			if prev81 < 0 || prev81 >= pos || pos-prev81 > maxDistance8 || checkByte != src[prev81+bestLength] {
				if prev41 >= 0 && prev41 < pos && pos-prev41 <= maxDistance4 && checkByte == src[prev41+bestLength] {
					prev = prev41
				} else {
					prev = -1
				}
			}
			if prev >= 0 {
				if length := commonMatchLengthUnchecked(src, pos, prev, blockEnd); length >= 4 && length > bestLength {
					bestLength = length
					checkPos = pos + bestLength
					checkOK = checkPos < len(src)
					matches = append(matches, levelMatch{pos: prev, length: length})
					if !checkOK {
						f.matches = matches
						return matches
					}
					checkByte = src[checkPos]
				}
			}

			prev = prev82
			if prev82 < 0 || prev82 >= pos || pos-prev82 > maxDistance8 || checkByte != src[prev82+bestLength] {
				if prev42 >= 0 && prev42 < pos && pos-prev42 <= maxDistance4 && checkByte == src[prev42+bestLength] {
					prev = prev42
				} else {
					prev = -1
				}
			}
			if prev >= 0 {
				if length := commonMatchLengthUnchecked(src, pos, prev, blockEnd); length >= 4 && length > bestLength {
					bestLength = length
					checkPos = pos + bestLength
					checkOK = checkPos < len(src)
					matches = append(matches, levelMatch{pos: prev, length: length})
					if !checkOK {
						f.matches = matches
						return matches
					}
					checkByte = src[checkPos]
				}
			}

			prev = prev83
			if prev83 < 0 || prev83 >= pos || pos-prev83 > maxDistance8 || checkByte != src[prev83+bestLength] {
				if prev43 >= 0 && prev43 < pos && pos-prev43 <= maxDistance4 && checkByte == src[prev43+bestLength] {
					prev = prev43
				} else {
					prev = -1
				}
			}
			if prev >= 0 {
				if length := commonMatchLengthUnchecked(src, pos, prev, blockEnd); length >= 4 && length > bestLength {
					matches = append(matches, levelMatch{pos: prev, length: length})
				}
			}
		} else {
			start4 := hash4.hashFromValue(hashValue) * entries
			start8 := hash8.hashFromValue(hashValue) * hash8.entries
			current4 := pos
			current8 := pos
			for i := 0; i < entries; i++ {
				prev4 := table4[start4+i]
				prev8 := table8[start8+i]
				table4[start4+i] = current4
				table8[start8+i] = current8
				current4 = prev4
				current8 = prev8

				prev := prev8
				if prev8 < 0 || prev8 >= pos || pos-prev8 > maxDistance8 || !checkOK || src[checkPos] != src[prev8+bestLength] {
					if prev4 < 0 || prev4 >= pos || pos-prev4 > maxDistance4 || !checkOK || src[checkPos] != src[prev4+bestLength] {
						continue
					}
					prev = prev4
				}
				if length := commonMatchLengthUnchecked(src, pos, prev, blockEnd); length >= 4 && length > bestLength {
					bestLength = length
					checkPos = pos + bestLength
					checkOK = checkPos < len(src)
					matches = append(matches, levelMatch{pos: prev, length: length})
				}
			}
		}
	}
	f.matches = matches
	return matches
}

func optimalParse(src []byte, start, end, blockEnd int, finder *levelOptimalMatchFinder, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool) []optimalMatchStep {
	return optimalParseDetailed(src, start, end, blockEnd, finder, startRepOffsets, advancedDistance, opts, model, noHuffmanCosts).steps
}

func optimalParseDetailed(src []byte, start, end, blockEnd int, finder *levelOptimalMatchFinder, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool) optimalParseResult {
	return optimalParseDetailedWithAcceleration(src, start, end, blockEnd, finder, startRepOffsets, advancedDistance, opts, model, noHuffmanCosts, optimalAccelerationBase)
}

func optimalParseDetailedWithAcceleration(src []byte, start, end, blockEnd int, finder *levelOptimalMatchFinder, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool, acceleration int) optimalParseResult {
	if start >= end {
		return optimalParseResult{consumed: start, acceleration: acceleration}
	}
	if acceleration <= 0 {
		acceleration = optimalAccelerationBase
	}
	stateEnd := min(blockEnd, end+opts.niceLength)
	const maxInt = int(^uint(0) >> 1)
	var states []optimalParseState
	if finder != nil {
		states = finder.parseStates(stateEnd - start + 1)
	} else {
		states = acquireOptimalParseStates(stateEnd - start + 1)
		defer releaseOptimalParseStates(states)
	}
	var stepScratch *[]optimalMatchStep
	if finder != nil {
		stepScratch = &finder.stepScratch
	}
	for i := range states {
		states[i].cost = maxInt
	}
	states[0] = optimalParseState{
		cost:       optimal1InitialCost,
		prev:       -1,
		repOffsets: startRepOffsets,
	}

	positionSkip := 0
	matchDistanceLimit := maxMatchDistance(opts)
	repCount := 1
	if advancedDistance {
		repCount = 3
	}
	terminalRepeatLength := opts.niceLength / 2
	for pos := start; pos < end; pos++ {
		index := pos - start
		current := states[index]
		if current.cost == maxInt {
			finder.updatePosition(src, pos, blockEnd)
			continue
		}
		if positionSkip > 0 || current.cost >= states[index+1].cost {
			finder.updatePosition(src, pos, blockEnd)
			positionSkip--
			continue
		}

		literalCost := current.cost + optimalLiteralCost(src, pos, current.litRun, current.repOffsets, model, noHuffmanCosts)
		if literalCost < states[index+1].cost {
			next := current
			next.cost = literalCost
			next.prev = index
			next.matchLen = 0
			next.distance = 0
			next.litRun = current.litRun + 1
			states[index+1] = next
		}

		normalLastLength := 1
		for rep := 0; rep < repCount; rep++ {
			distance := current.repOffsets[rep]
			if distance <= 0 || distance > matchDistanceLimit || pos+2 > blockEnd {
				continue
			}
			prev := pos - distance
			if prev < 0 {
				continue
			}
			length := commonMatchLengthUnchecked(src, pos, prev, blockEnd)
			if length < 2 {
				continue
			}
			if length >= terminalRepeatLength {
				finder.updatePosition(src, pos, blockEnd)
				updateOptimalFinderTerminalOverlap(finder, src, pos, distance, length, blockEnd, opts)
				steps := backtrackOptimalSteps(states, start, index, stepScratch)
				steps = append(steps, optimalMatchStep{pos: pos, length: length, distance: distance})
				return optimalParseResult{steps: steps, consumed: pos + length, acceleration: acceleration}
			}
			limit := min(length, stateEnd-pos)
			relaxOptimalMatchWithDistancePenalty(states, index, limit, distance, current, advancedDistance, model, noHuffmanCosts, false)
			normalLastLength = noHuffmanStoredMatchLength(length, noHuffmanCosts) + 1
			positionSkip = 1
			if length >= 3 {
				acceleration = optimalAccelerationBase
			}
			break
		}

		matches := finder.findMatchesAndUpdate(src, pos, blockEnd, normalLastLength)
		if len(matches) == 0 {
			acceleration++
			if acceleration >= optimalAccelerationLimit {
				return finishOptimalParseResult(states, start, index+1, opts.niceLength, 0, acceleration, stepScratch)
			}
			continue
		}
		if len(matches) > 0 {
			longest := matches[len(matches)-1]
			if longest.length >= opts.niceLength {
				distance := pos - longest.pos
				matchPos, matchLength := extendOptimalMatchLeft(src, 0, pos, distance, longest.length, current.litRun)
				matchIndex := matchPos - start
				if matchIndex >= 0 && matchIndex < len(states) && states[matchIndex].cost != maxInt && distance > 0 {
					updateOptimalFinderTerminalOverlap(finder, src, matchPos, distance, matchLength, blockEnd, opts)
					steps := backtrackOptimalSteps(states, start, matchIndex, stepScratch)
					steps = append(steps, optimalMatchStep{pos: matchPos, length: matchLength, distance: distance})
					return optimalParseResult{steps: steps, consumed: matchPos + matchLength, acceleration: acceleration}
				}
			}
			if longest.length >= 4 {
				acceleration = optimalAccelerationBase
			}
		}
		for i := len(matches) - 1; i >= 0; i-- {
			match := matches[i]
			distance := pos - match.pos
			matchPos, matchLength := extendOptimalMatchLeft(src, 0, pos, distance, match.length, current.litRun)
			matchIndex := matchPos - start
			if matchIndex < 0 || matchIndex >= len(states) || states[matchIndex].cost == maxInt {
				continue
			}
			matchState := states[matchIndex]
			limit := min(matchLength, stateEnd-matchPos)
			if limit < 3 || distance <= 0 {
				continue
			}
			length, nextIndex, cost, improves := optimalMatchCandidate(states, matchIndex, limit, distance, matchState, advancedDistance, model, noHuffmanCosts, true)
			if nextIndex < 0 || nextIndex >= len(states) {
				continue
			}
			if !improves {
				break
			}
			setOptimalMatchState(states, matchIndex, nextIndex, length, distance, matchState, advancedDistance, cost)
		}
	}

	return finishOptimalParseResult(states, start, end-start, opts.niceLength, end, acceleration, stepScratch)
}

func finishOptimalParseResult(states []optimalParseState, start, best, niceLength, consumed, acceleration int, stepScratch *[]optimalMatchStep) optimalParseResult {
	const maxInt = int(^uint(0) >> 1)
	if best >= len(states) {
		best = len(states) - 1
	}
	if best < 0 {
		best = 0
	}
	bestCost := states[best].cost
	for offset := 1; offset < niceLength && best+offset < len(states); offset++ {
		candidate := best + offset
		if states[candidate].cost <= bestCost {
			best = candidate
			bestCost = states[candidate].cost
		}
	}
	for best > 0 && states[best].cost == maxInt {
		best--
	}
	if consumed == 0 {
		consumed = start + best
	}
	if best == 0 {
		return optimalParseResult{consumed: consumed, acceleration: acceleration}
	}
	return optimalParseResult{steps: backtrackOptimalSteps(states, start, best, stepScratch), consumed: consumed, acceleration: acceleration}
}

func updateOptimalFinderTerminalOverlap(finder *levelOptimalMatchFinder, src []byte, pos, distance, length, blockEnd int, opts compressorLevelOptions) {
	updateEnd := pos + min(min(distance, length), opts.niceLength)
	if updateEnd > blockEnd {
		updateEnd = blockEnd
	}
	for updatePos := pos + 1; updatePos < updateEnd; updatePos++ {
		finder.updatePosition(src, updatePos, blockEnd)
	}
}

func backtrackOptimalSteps(states []optimalParseState, start, best int, stepScratch *[]optimalMatchStep) []optimalMatchStep {
	var steps []optimalMatchStep
	if stepScratch != nil {
		steps = (*stepScratch)[:0]
	} else {
		steps = make([]optimalMatchStep, 0, 16)
	}
	for index := best; index > 0; {
		state := states[index]
		if state.prev < 0 {
			break
		}
		if state.matchLen > 0 {
			steps = append(steps, optimalMatchStep{
				pos:      start + state.prev,
				length:   state.matchLen,
				distance: state.distance,
			})
		}
		index = state.prev
	}
	if len(steps) == 0 {
		if stepScratch != nil {
			if cap(steps) < 1 {
				steps = make([]optimalMatchStep, 0, 1)
			}
			*stepScratch = steps
		}
		return steps
	}
	for left, right := 0, len(steps)-1; left < right; left, right = left+1, right-1 {
		steps[left], steps[right] = steps[right], steps[left]
	}
	if stepScratch != nil {
		*stepScratch = steps
	}
	return steps
}

func extendOptimalMatchLeft(src []byte, inputStart, pos, distance, length, literalRun int) (int, int) {
	back := pos - distance
	literalRunStart := pos - literalRun
	for back > inputStart && pos > literalRunStart && pos-1 >= 0 && back-1 >= 0 && src[pos-1] == src[back-1] {
		pos--
		back--
		length++
	}
	return pos, length
}

func multiArrivalOptimalParse(src []byte, start, end, blockEnd int, matchSource optimalMatchSource, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool) []optimalMatchStep {
	return multiArrivalOptimalParseDetailed(src, start, end, blockEnd, matchSource, startRepOffsets, advancedDistance, opts, model, noHuffmanCosts).steps
}

func multiArrivalOptimalParseDetailed(src []byte, start, end, blockEnd int, matchSource optimalMatchSource, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool) optimalParseResult {
	return multiArrivalOptimalParseDetailedWithAcceleration(src, start, end, blockEnd, matchSource, startRepOffsets, advancedDistance, opts, model, noHuffmanCosts, optimalAccelerationBase)
}

func multiArrivalOptimalParseDetailedWithAcceleration(src []byte, start, end, blockEnd int, matchSource optimalMatchSource, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, model *optimalCostModel, noHuffmanCosts bool, acceleration int) optimalParseResult {
	if start >= end {
		return optimalParseResult{consumed: start, acceleration: acceleration}
	}
	if acceleration <= 0 {
		acceleration = optimalAccelerationBase
	}
	stateEnd := min(blockEnd, end+opts.niceLength)
	maxArrivals := opts.maxArrivals
	if maxArrivals <= 0 {
		maxArrivals = 1
	}
	if maxArrivals > 16 {
		maxArrivals = 16
	}
	const maxInt = int(^uint(0) >> 1)
	stateCount := (stateEnd - start + 1) * maxArrivals
	states := acquireOptimalParseStates(stateCount)
	defer releaseOptimalParseStates(states)
	for i := range states {
		states[i].cost = maxInt
	}
	states[0] = optimalParseState{
		cost:       multiArrivalInitialCost,
		prev:       -1,
		prevPath:   -1,
		repOffsets: startRepOffsets,
	}

	var matches []lzMatch
	var stepScratch *[]optimalMatchStep
	if opts.matchState != nil {
		stepScratch = &opts.matchState.backtrackScratch
	}
	matchDistanceLimit := maxMatchDistance(opts)
	for pos := start; pos < end; pos++ {
		index := pos - start
		nextBase := (index + 1) * maxArrivals
		if states[index*maxArrivals].cost == maxInt {
			updateOptimalMatchSourcePosition(matchSource, src, pos, opts)
			continue
		}

		nextExpectedLength := 2
		for path := 0; path < maxArrivals; path++ {
			current := states[index*maxArrivals+path]
			if current.cost == maxInt {
				break
			}
			if multiArrivalCostPruned(current.cost, states[nextBase].cost, optimalCostScale*maxArrivals) {
				break
			}

			literalCost := current.cost + optimalLiteralCost(src, pos, current.litRun, current.repOffsets, model, noHuffmanCosts)
			insertOptimalArrival(states, maxArrivals, index+1, index, path, 0, 0, literalCost, current.repOffsets, current.litRun+1)

			repCount := 1
			if advancedDistance {
				repCount = 3
			}
			for rep := 0; rep < repCount; rep++ {
				distance := current.repOffsets[rep]
				if distance <= 0 || distance > matchDistanceLimit || pos-distance < 0 || pos+2 > blockEnd {
					continue
				}
				length := commonMatchLengthUnchecked(src, pos, pos-distance, blockEnd)
				if length < nextExpectedLength {
					continue
				}
				if length >= opts.niceLength/2 {
					updateOptimalMatchSourceTerminalOverlap(matchSource, src, pos, distance, length, opts)
					steps := backtrackMultiArrivalSteps(states, maxArrivals, start, index, path, stepScratch)
					steps = append(steps, optimalMatchStep{pos: pos, length: length, distance: distance})
					if stepScratch != nil {
						*stepScratch = steps
					}
					return optimalParseResult{steps: steps, consumed: pos + length, acceleration: acceleration}
				}
				limit := min(length, stateEnd-pos)
				relaxMultiArrivalRepMatches(states, maxArrivals, index, path, limit, nextExpectedLength, distance, current, advancedDistance, model, noHuffmanCosts)
				nextExpectedLength = limit
				if length >= 3 {
					acceleration = optimalAccelerationBase
				}
			}
		}

		if multiArrivalCostPruned(states[index*maxArrivals].cost, states[nextBase].cost, optimalCostScale*(maxArrivals/8)) {
			updateOptimalMatchSourcePosition(matchSource, src, pos, opts)
			continue
		}

		matches = matches[:0]
		if matchSource != nil {
			matches = matchSource.findLZMatchesAndUpdate(src, pos, 0, len(src)-lastBytes, blockEnd, nextExpectedLength, opts, matches)
		}
		if len(matches) == 0 {
			if opts.parser < compressorParserOptimal3 {
				acceleration++
				if acceleration >= optimalAccelerationLimit {
					return finishMultiArrivalParseResult(states, maxArrivals, start, index+1, opts.niceLength, 0, acceleration, stepScratch)
				}
			}
			continue
		}
		if len(matches) > 0 {
			longest := matches[len(matches)-1]
			if longest.length >= opts.niceLength {
				updateOptimalMatchSourceTerminalOverlap(matchSource, src, pos, longest.distance, longest.length, opts)
				steps := backtrackMultiArrivalSteps(states, maxArrivals, start, index, 0, stepScratch)
				steps = append(steps, optimalMatchStep{pos: pos, length: longest.length, distance: longest.distance})
				if stepScratch != nil {
					*stepScratch = steps
				}
				return optimalParseResult{steps: steps, consumed: pos + longest.length, acceleration: acceleration}
			}
			if longest.length >= 4 {
				acceleration = optimalAccelerationBase
			}
		}
		pathMax := min(maxArrivals, 2)
		for path := 0; path < pathMax; path++ {
			current := states[index*maxArrivals+path]
			if current.cost == maxInt {
				break
			}
			if multiArrivalCostPruned(current.cost, states[nextBase].cost, optimalCostScale*maxArrivals) {
				break
			}
			nextMatchReductionLimit := nextExpectedLength
			for _, match := range matches {
				distance := match.distance
				limit := min(match.length, stateEnd-pos)
				if distance <= 0 || distance == current.repOffsets[0] || limit < nextExpectedLength {
					continue
				}
				relaxMultiArrivalNormalMatches(states, maxArrivals, index, path, limit, nextMatchReductionLimit, distance, current, advancedDistance, model, noHuffmanCosts)
				nextMatchReductionLimit = limit
			}
		}
	}

	return finishMultiArrivalParseResult(states, maxArrivals, start, end-start, opts.niceLength, end, acceleration, stepScratch)
}

func finishMultiArrivalParseResult(states []optimalParseState, maxArrivals, start, bestIndex, niceLength, consumed, acceleration int, stepScratch *[]optimalMatchStep) optimalParseResult {
	const maxInt = int(^uint(0) >> 1)
	limit := len(states) / maxArrivals
	if bestIndex >= limit {
		bestIndex = limit - 1
	}
	if bestIndex < 0 {
		bestIndex = 0
	}
	bestPath := 0
	bestCost := states[bestIndex*maxArrivals].cost
	for offset := 1; offset < niceLength && bestIndex+offset < limit; offset++ {
		candidate := bestIndex + offset
		if cost := states[candidate*maxArrivals].cost; cost <= bestCost {
			bestIndex = candidate
			bestCost = cost
		}
	}
	if consumed == 0 {
		consumed = start + bestIndex
	}
	if bestCost == maxInt {
		return optimalParseResult{consumed: consumed, acceleration: acceleration}
	}
	return optimalParseResult{steps: backtrackMultiArrivalSteps(states, maxArrivals, start, bestIndex, bestPath, stepScratch), consumed: consumed, acceleration: acceleration}
}

func updateOptimalMatchSourceTerminalOverlap(matchSource optimalMatchSource, src []byte, pos, distance, length int, opts compressorLevelOptions) {
	finder, ok := matchSource.(*binaryMatchFinder)
	if !ok {
		return
	}
	updateEnd := pos + min(min(distance, length), opts.niceLength)
	blockEnd := len(src) - lastBytes
	if updateEnd > blockEnd {
		updateEnd = blockEnd
	}
	for updatePos := pos + 1; updatePos < updateEnd; updatePos++ {
		finder.updatePosition(src, updatePos, 0, blockEnd, opts)
	}
}

func backtrackMultiArrivalSteps(states []optimalParseState, maxArrivals, start, index, path int, stepScratch *[]optimalMatchStep) []optimalMatchStep {
	originalIndex := index
	originalPath := path
	count := 0
	for index > 0 {
		state := states[index*maxArrivals+path]
		if state.prev < 0 {
			break
		}
		if state.matchLen > 0 {
			count++
		}
		index, path = state.prev, state.prevPath
		if path < 0 {
			break
		}
	}
	if count == 0 {
		return nil
	}
	var steps []optimalMatchStep
	if stepScratch != nil {
		if cap(*stepScratch) < count {
			releaseOptimalMatchSteps(*stepScratch)
			*stepScratch = acquireOptimalMatchSteps(count)
		}
		steps = (*stepScratch)[:count]
	} else {
		steps = make([]optimalMatchStep, count)
	}
	write := count - 1
	index = originalIndex
	path = originalPath
	for index > 0 {
		state := states[index*maxArrivals+path]
		if state.prev < 0 {
			break
		}
		if state.matchLen > 0 {
			steps[write] = optimalMatchStep{
				pos:      start + state.prev,
				length:   state.matchLen,
				distance: state.distance,
			}
			write--
		}
		index, path = state.prev, state.prevPath
		if path < 0 {
			break
		}
	}
	if stepScratch != nil {
		*stepScratch = steps
	}
	return steps
}

func multiArrivalCostPruned(currentCost, nextCost, slack int) bool {
	const maxInt = int(^uint(0) >> 1)
	return currentCost != maxInt && nextCost != maxInt && currentCost >= slack && currentCost-slack >= nextCost
}

func updateOptimalMatchSourcePosition(matchSource optimalMatchSource, src []byte, pos int, opts compressorLevelOptions) {
	if finder, ok := matchSource.(*binaryMatchFinder); ok {
		finder.updatePosition(src, pos, 0, len(src)-lastBytes, opts)
	}
}

func relaxMultiArrivalRepMatches(states []optimalParseState, maxArrivals, index, path, maxLength, nextExpectedLength, distance int, current optimalParseState, advancedDistance bool, model *optimalCostModel, noHuffmanCosts bool) {
	if maxLength < 2 {
		return
	}
	lower := nextExpectedLength
	if noHuffmanCosts {
		lower = maxLength
	} else if maxLength-lower > 15 {
		lower = maxLength - 15
	}
	for length := maxLength; length >= lower; length-- {
		relaxMultiArrivalRepMatch(states, maxArrivals, index, path, length, distance, current, advancedDistance, model, noHuffmanCosts)
	}
}

func relaxMultiArrivalNormalMatches(states []optimalParseState, maxArrivals, index, path, maxLength, nextMatchReductionLimit, distance int, current optimalParseState, advancedDistance bool, model *optimalCostModel, noHuffmanCosts bool) {
	if maxLength < 3 {
		return
	}
	lower := nextMatchReductionLimit
	if maxLength != nextMatchReductionLimit {
		lower = nextMatchReductionLimit + 1
	}
	if noHuffmanCosts {
		lower = maxLength
	} else if maxLength-lower > 15 {
		lower = maxLength - 15
	}
	for length := maxLength; length >= lower; length-- {
		relaxMultiArrivalMatch(states, maxArrivals, index, path, length, distance, current, advancedDistance, model, noHuffmanCosts)
	}
}

func relaxMultiArrivalMatch(states []optimalParseState, maxArrivals, index, path, length, distance int, current optimalParseState, advancedDistance bool, model *optimalCostModel, noHuffmanCosts bool) {
	relaxMultiArrivalMatchWithDistancePenalty(states, maxArrivals, index, path, length, distance, current, advancedDistance, model, noHuffmanCosts, true)
}

func relaxMultiArrivalRepMatch(states []optimalParseState, maxArrivals, index, path, length, distance int, current optimalParseState, advancedDistance bool, model *optimalCostModel, noHuffmanCosts bool) {
	relaxMultiArrivalMatchWithDistancePenalty(states, maxArrivals, index, path, length, distance, current, advancedDistance, model, noHuffmanCosts, false)
}

func relaxMultiArrivalMatchWithDistancePenalty(states []optimalParseState, maxArrivals, index, path, length, distance int, current optimalParseState, advancedDistance bool, model *optimalCostModel, noHuffmanCosts bool, longDistanceSpeedPenalty bool) {
	length = noHuffmanStoredMatchLength(length, noHuffmanCosts)
	nextIndex := index + length
	if nextIndex >= len(states)/maxArrivals {
		return
	}
	cost := current.cost + optimalMatchCostWithDistancePenalty(length, distance, current.litRun, current.repOffsets, advancedDistance, model, noHuffmanCosts, longDistanceSpeedPenalty)
	repOffsets := updateOptimalRepOffsets(current.repOffsets, distance, advancedDistance)
	insertOptimalArrival(states, maxArrivals, nextIndex, index, path, length, distance, cost, repOffsets, 0)
}

func insertOptimalArrival(states []optimalParseState, maxArrivals, index, prev, prevPath, matchLen, distance, cost int, repOffsets [3]int, litRun int) {
	base := index * maxArrivals
	limit := base + maxArrivals
	for slot := base; slot < limit; slot++ {
		if states[slot].cost <= cost && states[slot].repOffsets[0] == repOffsets[0] {
			return
		}
		if cost >= states[slot].cost {
			continue
		}
		copy(states[slot+1:limit], states[slot:limit-1])
		states[slot] = optimalParseState{
			cost:       cost,
			prev:       prev,
			prevPath:   prevPath,
			matchLen:   matchLen,
			distance:   distance,
			litRun:     litRun,
			repOffsets: repOffsets,
		}
		return
	}
}

func relaxOptimalMatchWithDistancePenalty(states []optimalParseState, index, length, distance int, current optimalParseState, advancedDistance bool, model *optimalCostModel, noHuffmanCosts bool, longDistanceSpeedPenalty bool) bool {
	length, nextIndex, cost, improves := optimalMatchCandidate(states, index, length, distance, current, advancedDistance, model, noHuffmanCosts, longDistanceSpeedPenalty)
	if !improves {
		return false
	}
	setOptimalMatchState(states, index, nextIndex, length, distance, current, advancedDistance, cost)
	return true
}

func optimalMatchCandidate(states []optimalParseState, index, length, distance int, current optimalParseState, advancedDistance bool, model *optimalCostModel, noHuffmanCosts bool, longDistanceSpeedPenalty bool) (int, int, int, bool) {
	length = noHuffmanStoredMatchLength(length, noHuffmanCosts)
	nextIndex := index + length
	if nextIndex < 0 || nextIndex >= len(states) {
		return length, nextIndex, 0, false
	}
	if current.cost >= states[nextIndex].cost {
		return length, nextIndex, 0, false
	}
	cost := current.cost + optimalMatchCostWithDistancePenalty(length, distance, current.litRun, current.repOffsets, advancedDistance, model, noHuffmanCosts, longDistanceSpeedPenalty)
	return length, nextIndex, cost, cost < states[nextIndex].cost
}

func setOptimalMatchState(states []optimalParseState, index, nextIndex, length, distance int, current optimalParseState, advancedDistance bool, cost int) {
	states[nextIndex] = optimalParseState{
		cost:       cost,
		prev:       index,
		matchLen:   length,
		distance:   distance,
		repOffsets: updateOptimalRepOffsets(current.repOffsets, distance, advancedDistance),
	}
}

func optimalLiteralCost(src []byte, pos int, literalRun int, repOffsets [3]int, model *optimalCostModel, noHuffmanCosts bool) int {
	if noHuffmanCosts {
		return noHuffmanLiteralCost(literalRun)
	}
	cost := literalSymbolCost(src, pos, repOffsets, model)
	if literalRun >= 6 {
		cost += encodedLengthCost(literalRun+1, 7, model)
		if literalRun >= 7 {
			cost -= encodedLengthCost(literalRun, 7, model)
		}
	}
	speedCost := 0
	if literalRun == 6 {
		speedCost = 2
	}
	return cost*optimalCostScale + speedCost
}

const (
	modelTokenStream = iota
	modelDistanceStream
	modelLengthStream
)

const optimalCostScale = 1 << 12

const (
	optimal1InitialCost     = 64 << 8
	multiArrivalInitialCost = 64 << 12
)

const (
	optimalAccelerationThreshold = 6
	optimalAccelerationBase      = 1 << optimalAccelerationThreshold
	optimalAccelerationLimit     = 2 << optimalAccelerationThreshold
)

func literalSymbolCost(src []byte, pos int, repOffsets [3]int, model *optimalCostModel) int {
	if model == nil || !model.enabled {
		return 8
	}
	symbol := int(src[pos])
	switch model.literalMode {
	case streamLiteralsDelta:
		distance := repOffsets[0]
		if distance > 0 && pos-distance >= 0 {
			return model.literal[0][int(src[pos]-src[pos-distance])]
		}
	case streamLiteralsPosMask3:
		return model.literal[pos&3][symbol]
	case streamLiteralsDelta | streamLiteralsPosMask3:
		distance := repOffsets[0]
		if distance > 0 && pos-distance >= 0 {
			return model.literal[pos&3][int(src[pos]-src[pos-distance])]
		}
	}
	return model.literal[0][symbol]
}

func optimalMatchCostWithDistancePenalty(length, distance, literalRun int, repOffsets [3]int, advancedDistance bool, model *optimalCostModel, noHuffmanCosts bool, longDistanceSpeedPenalty bool) int {
	if noHuffmanCosts {
		return noHuffmanMatchCostWithDistancePenalty(length, distance, repOffsets, advancedDistance, longDistanceSpeedPenalty)
	}
	distanceBits := 0
	if advancedDistance {
		switch distance {
		case repOffsets[0]:
			distanceBits = 0
		case repOffsets[1]:
			distanceBits = 1
		case repOffsets[2]:
			distanceBits = 2
		default:
			distanceBits = 3
		}
	} else if distance != repOffsets[0] {
		distanceBits = ((bits.Len(uint(distance)) - 1) / 8) + 1
	}
	token := (min(literalRun, 7) << 5) | (distanceBits << 3) | (min(length, 9) - 2)
	cost := symbolCost(model, modelTokenStream, token)
	if length > 8 {
		cost += encodedLengthCost(length, 9, model)
	}
	if advancedDistance {
		if distanceBits == 3 {
			virtualDistance := distance + 7
			extraBits := bits.Len(uint(virtualDistance>>3)) - 1
			distanceToken := ((virtualDistance & 7) ^ 7) | (extraBits << 3)
			cost += symbolCost(model, modelDistanceStream, distanceToken) + extraBits
		}
	} else if distance != repOffsets[0] {
		bytes := ((bits.Len(uint(distance)) - 1) / 8) + 1
		for i := 0; i < bytes; i++ {
			cost += symbolCost(model, modelDistanceStream, (distance>>(i*8))&0xff)
		}
	}
	speedCost := 1
	if length > 8 {
		speedCost += 2
	}
	if longDistanceSpeedPenalty && distance > 1<<16 {
		speedCost++
	}
	return cost*optimalCostScale + speedCost
}

func noHuffmanLiteralCost(literalRun int) int {
	if literalRun == 6 {
		return 16*optimalCostScale + 2
	}
	return 8 * optimalCostScale
}

func noHuffmanStoredMatchLength(length int, noHuffmanCosts bool) int {
	if noHuffmanCosts && length > 8 && length <= 16 {
		return 8
	}
	return length
}

func noHuffmanMatchCost(length, distance int, repOffsets [3]int, advancedDistance bool) int {
	return noHuffmanMatchCostWithDistancePenalty(length, distance, repOffsets, advancedDistance, true)
}

func noHuffmanMatchCostWithDistancePenalty(length, distance int, repOffsets [3]int, advancedDistance bool, longDistanceSpeedPenalty bool) int {
	length = noHuffmanStoredMatchLength(length, true)
	cost := 0
	if distance == repOffsets[0] || (advancedDistance && (distance == repOffsets[1] || distance == repOffsets[2])) {
		cost = 8
		if length > 8 {
			cost = 16
		}
	} else if advancedDistance {
		virtualDistance := distance + 7
		extraBits := bits.Len(uint(virtualDistance>>3)) - 1
		cost = 16 + extraBits
		if length > 8 {
			cost += 8
		}
	} else {
		distanceBytes := ((bits.Len(uint(distance)) - 1) / 8) + 1
		cost = 8 * (distanceBytes + 1)
		if length > 8 {
			cost += 8
		}
	}
	speedCost := 1
	if length > 8 {
		speedCost += 2
	}
	if longDistanceSpeedPenalty && distance > 1<<16 {
		speedCost++
	}
	return cost*optimalCostScale + speedCost
}

func symbolCost(model *optimalCostModel, stream int, symbol int) int {
	if model == nil || !model.enabled {
		return 8
	}
	switch stream {
	case modelTokenStream:
		return model.token[symbol&0xff]
	case modelDistanceStream:
		return model.distance[symbol&0xff]
	case modelLengthStream:
		return model.length[symbol&0xff]
	default:
		return 8
	}
}

func encodedLengthCost(value, overflow int, model *optimalCostModel) int {
	value -= overflow
	if value <= 223 {
		return symbolCost(model, modelLengthStream, value)
	}
	value -= 224
	cost := symbolCost(model, modelLengthStream, 224|(value&0x1f))
	value >>= 5
	if value <= 223 {
		return cost + symbolCost(model, modelLengthStream, value)
	}
	value -= 224
	return cost + symbolCost(model, modelLengthStream, 224|(value&0x1f)) + symbolCost(model, modelLengthStream, value>>5)
}

func buildOptimalCostModel(src []byte, blockStart, blockEnd int, steps []optimalMatchStep, startRepOffsets [3]int, advancedDistance bool, opts compressorLevelOptions, plan optimalEntropyPlan, state *compressState) optimalCostModel {
	model := newRawOptimalCostModel()
	if opts.decSpeedBias >= 0.99 {
		return model
	}

	blockSize := blockEnd - blockStart
	literals := newCompressionBlockLiterals(state, src, blockStart, blockEnd, opts.decSpeedBias, plan.literalMode&streamLiteralsPosMask3 != 0)
	tokens := compressionTokenBuffer(state, blockSize)
	distances := compressionDistanceBuffer(state, blockSize)
	lengths := compressionLengthBuffer(state, blockSize)
	lastDistance := startRepOffsets[0]
	repOffsets := startRepOffsets
	litStart := blockStart
	if blockStart == 0 {
		litStart++
	}
	for _, step := range steps {
		if step.pos < litStart || step.pos+step.length > blockEnd {
			continue
		}
		literals.appendRun(src, litStart, step.pos, repOffsets[0], &tokens, &lengths)
		if advancedDistance {
			appendAdvancedBlockDistanceNoBits(step.distance, &distances, &tokens, &lastDistance, &repOffsets)
		} else {
			appendStandardBlockDistance(step.distance, &distances, &tokens, &lastDistance, &repOffsets)
		}
		appendMatchLength(step.length, &tokens, &lengths)
		litStart = step.pos + step.length
	}
	if litStart < blockEnd {
		literals.appendRun(src, litStart, blockEnd, repOffsets[0], &tokens, &lengths)
	}

	return buildOptimalCostModelFromStreams(literals, tokens, distances, lengths, opts, plan)
}

func buildOptimalCostModelFromStreams(literals blockLiterals, tokens, distances, lengths []byte, opts compressorLevelOptions, plan optimalEntropyPlan) optimalCostModel {
	model := newRawOptimalCostModel()
	model.applyLiteralCosts(literals, opts, plan)
	model.token = estimateHuffmanCosts(tokens, plan.streamBias(plan.token, opts.decSpeedBias))
	model.distance = estimateHuffmanCosts(distances, plan.streamBias(plan.distance, opts.decSpeedBias))
	model.length = estimateHuffmanCosts(lengths, plan.streamBias(plan.length, opts.decSpeedBias))
	model.enabled = len(literals.raw) >= 32 || len(tokens) >= 32 || len(distances) >= 32 || len(lengths) >= 32
	return model
}

func newRawOptimalCostModel() optimalCostModel {
	model := optimalCostModel{}
	for stream := 0; stream < len(model.literal); stream++ {
		for i := 0; i < 256; i++ {
			model.literal[stream][i] = 8
		}
	}
	for i := 0; i < 256; i++ {
		model.token[i] = 8
		model.distance[i] = 8
		model.length[i] = 8
	}
	return model
}

func (model *optimalCostModel) applyLiteralCosts(literals blockLiterals, opts compressorLevelOptions, plan optimalEntropyPlan) {
	if plan.enabled {
		model.applyForcedLiteralCosts(literals, opts, plan)
		return
	}

	rawBias := plan.literalBias(0, opts.decSpeedBias)
	bestSize := estimatedEntropyEncodedSize(literals.raw, rawBias)
	model.literalMode = 0
	model.literal[0] = estimateHuffmanCosts(literals.raw, rawBias)

	if !literals.collectAdvanced {
		return
	}
	if literals.deltaOK && len(literals.delta) == len(literals.raw) {
		if size := estimatedEntropyEncodedSize(literals.delta, rawBias); size < bestSize {
			bestSize = size
			model.literalMode = streamLiteralsDelta
			model.literal[0] = estimateHuffmanCosts(literals.delta, rawBias)
		}
	}
	if literals.collectPos {
		posSize := literalSubstreamEncodedSize(literals.pos[:], streamLiteralsPosMask3, opts.decSpeedBias, plan)
		if posSize < bestSize {
			bestSize = posSize
			model.literalMode = streamLiteralsPosMask3
			for stream := 0; stream < 4; stream++ {
				model.literal[stream] = estimateHuffmanCosts(literals.pos[stream], plan.literalBias(stream, opts.decSpeedBias))
			}
		}
		if literals.deltaOK && len(literals.delta) == len(literals.raw) {
			posDeltaSize := literalSubstreamEncodedSize(literals.posDelta[:], streamLiteralsDelta|streamLiteralsPosMask3, opts.decSpeedBias, plan)
			if posDeltaSize < bestSize {
				model.literalMode = streamLiteralsDelta | streamLiteralsPosMask3
				for stream := 0; stream < 4; stream++ {
					model.literal[stream] = estimateHuffmanCosts(literals.posDelta[stream], plan.literalBias(stream, opts.decSpeedBias))
				}
			}
		}
	}
}

func (model *optimalCostModel) applyForcedLiteralCosts(literals blockLiterals, opts compressorLevelOptions, plan optimalEntropyPlan) {
	if plan.literalMode&streamLiteralsPosMask3 == 0 || !literals.collectPos {
		rawBias := plan.literalBias(0, opts.decSpeedBias)
		rawSize := estimatedEntropyEncodedSize(literals.raw, rawBias)
		model.literalMode = 0
		model.literal[0] = estimateHuffmanCosts(literals.raw, rawBias)

		if literals.collectAdvanced && literals.deltaOK && len(literals.delta) == len(literals.raw) {
			deltaSize := estimatedEntropyEncodedSize(literals.delta, rawBias)
			deltaSize += literalDeltaRetestPenalty(len(literals.delta), opts)
			if deltaSize < rawSize {
				model.literalMode = streamLiteralsDelta
				model.literal[0] = estimateHuffmanCosts(literals.delta, rawBias)
			}
		}
		return
	}

	rawSize := literalSubstreamEncodedSize(literals.pos[:], streamLiteralsPosMask3, opts.decSpeedBias, plan)
	model.literalMode = streamLiteralsPosMask3
	for stream := 0; stream < 4; stream++ {
		model.literal[stream] = estimateHuffmanCosts(literals.pos[stream], plan.literalBias(stream, opts.decSpeedBias))
	}

	if literals.collectAdvanced && literals.deltaOK && len(literals.delta) == len(literals.raw) {
		deltaSize := literalSubstreamEncodedSize(literals.posDelta[:], streamLiteralsDelta|streamLiteralsPosMask3, opts.decSpeedBias, plan)
		for _, stream := range literals.posDelta {
			deltaSize += literalDeltaRetestPenalty(len(stream), opts)
		}
		if deltaSize < rawSize {
			model.literalMode = streamLiteralsDelta | streamLiteralsPosMask3
			for stream := 0; stream < 4; stream++ {
				model.literal[stream] = estimateHuffmanCosts(literals.posDelta[stream], plan.literalBias(stream, opts.decSpeedBias))
			}
		}
	}
}

func literalDeltaRetestPenalty(symbolCount int, opts compressorLevelOptions) int {
	if symbolCount <= 0 {
		return 0
	}
	return int(float64(symbolCount) * (opts.decSpeedBias/4 + 0.05/8))
}

func literalSubstreamEncodedSize(streams [][]byte, _ int, decSpeedBias float64, plan optimalEntropyPlan) int {
	size := 0
	for index, stream := range streams {
		size += estimatedEntropyEncodedSize(stream, plan.literalBias(index, decSpeedBias))
	}
	return size
}

func estimatedEntropyEncodedSize(data []byte, decSpeedBias float64) int {
	var hist [256]uint32
	for _, value := range data {
		hist[value]++
	}
	return estimateEntropyFromHistogram(hist[:], decSpeedBias).size
}

func encodeOptimalLiterals(literals blockLiterals, opts compressorLevelOptions, model optimalCostModel, plan optimalEntropyPlan, useFastCodegen bool) []byte {
	return appendOptimalLiterals(nil, literals, opts, model, plan, useFastCodegen)
}

func appendOptimalLiterals(dst []byte, literals blockLiterals, opts compressorLevelOptions, model optimalCostModel, plan optimalEntropyPlan, useFastCodegen bool) []byte {
	if !model.enabled {
		return literals.appendEncodedWithCodegen(dst, opts.decSpeedBias, useFastCodegen)
	}
	switch model.literalMode {
	case streamLiteralsDelta:
		if literals.deltaOK && len(literals.delta) == len(literals.raw) {
			return appendEntropyWithCodegen(dst, literals.delta, streamLiteralsDelta, plan.literalBias(0, opts.decSpeedBias), useFastCodegen)
		}
	case streamLiteralsPosMask3:
		if literals.collectPos {
			return appendLiteralSubstreamsWithPlan(dst, literals.pos[:], streamLiteralsPosMask3, opts.decSpeedBias, plan, useFastCodegen)
		}
	case streamLiteralsDelta | streamLiteralsPosMask3:
		if literals.collectPos && literals.deltaOK && len(literals.delta) == len(literals.raw) {
			return appendLiteralSubstreamsWithPlan(dst, literals.posDelta[:], streamLiteralsDelta|streamLiteralsPosMask3, opts.decSpeedBias, plan, useFastCodegen)
		}
	}
	return appendEntropyWithCodegen(dst, literals.raw, 0, plan.literalBias(0, opts.decSpeedBias), useFastCodegen)
}

func appendLiteralSubstreamsWithPlan(dst []byte, streams [][]byte, flags int, decSpeedBias float64, plan optimalEntropyPlan, useFastCodegen bool) []byte {
	for index, stream := range streams {
		dst = appendEntropyWithCodegen(dst, stream, flags, plan.literalBias(index, decSpeedBias), useFastCodegen)
	}
	return dst
}

func estimateHuffmanCosts(data []byte, decSpeedBias float64) [256]int {
	var costs [256]int
	for i := range costs {
		costs[i] = 8
	}
	var hist [256]uint32
	for _, b := range data {
		hist[b]++
	}
	estimate := estimateEntropyFromHistogram(hist[:], decSpeedBias)
	if estimate.uncompressed {
		return costs
	}
	return estimate.costs
}

func updateOptimalRepOffsets(repOffsets [3]int, distance int, advancedDistance bool) [3]int {
	if !advancedDistance {
		repOffsets[0] = distance
		return repOffsets
	}
	if distance == repOffsets[0] {
		return repOffsets
	}
	if distance == repOffsets[1] {
		repOffsets[1] = repOffsets[0]
		repOffsets[0] = distance
		return repOffsets
	}
	repOffsets[2] = repOffsets[1]
	repOffsets[1] = repOffsets[0]
	repOffsets[0] = distance
	return repOffsets
}

func (f *levelLazyFastMatchFinder) find(src []byte, pos, blockEnd, repLength int) levelMatch {
	if pos < 0 || pos+8 > len(src) || pos+4 > blockEnd {
		return levelMatch{}
	}
	prev4, ok := f.hash4.candidateAndUpdateHashable(src, pos)
	if !ok {
		return levelMatch{}
	}
	length := matchLengthFromCandidate(src, pos, prev4, blockEnd, 4)
	distance := pos - prev4
	if length == 0 || length < repLength+2 || (length == 4 && distance >= 1<<16) {
		return levelMatch{}
	}

	match := levelMatch{pos: prev4, length: length}
	if pos+8 <= blockEnd {
		prev8, ok := f.hash8.candidateAndUpdateHashable(src, pos)
		if ok && matchEndEqual(src, pos, prev8, match.length) {
			if length := matchLengthFromCandidate(src, pos, prev8, blockEnd, 8); length > match.length {
				match = levelMatch{pos: prev8, length: length}
			}
		}
	}
	return match
}

func (f *levelLazyFastMatchFinder) findLazy1(src []byte, pos, blockEnd, currentLength int) levelMatch {
	if pos < 0 || pos+8 > len(src) || pos+4 > blockEnd {
		return levelMatch{}
	}
	prev8, ok8 := 0, false
	if pos+8 <= blockEnd {
		prev8, ok8 = f.hash8.candidateAndUpdateHashable(src, pos)
	}
	prev4, ok4 := f.hash4.candidateAndUpdateHashable(src, pos)
	matched8 := ok8 && matchEndEqual(src, pos, prev8, currentLength)
	matched4 := ok4 && matchEndEqual(src, pos, prev4, currentLength)
	if !matched8 && !matched4 {
		return levelMatch{}
	}
	prev := prev4
	if matched8 {
		prev = prev8
	}
	length := matchLengthFromCandidate(src, pos, prev, blockEnd, 4)
	if length <= currentLength {
		return levelMatch{}
	}
	return levelMatch{pos: prev, length: length}
}

func (f *levelLazyFastMatchFinder) findLazy2(src []byte, pos, blockEnd, currentLength int) levelMatch {
	if pos >= 0 && pos+8 <= len(src) && pos+4 <= blockEnd {
		f.hash4.addHashable(src, pos)
	}
	if pos < 0 || pos+8 > blockEnd {
		return levelMatch{}
	}
	prev8, ok := f.hash8.candidateAndUpdateHashable(src, pos)
	if !ok || !matchEndEqual(src, pos, prev8, currentLength) {
		return levelMatch{}
	}
	length := matchLengthFromCandidate(src, pos, prev8, blockEnd, 8)
	if length <= currentLength {
		return levelMatch{}
	}
	return levelMatch{pos: prev8, length: length}
}

func (f *levelLazyFastMatchFinder) addLongRepeatPositions(src []byte, pos, blockEnd int) {
	for i := 0; i <= 4; i++ {
		f.hash4.add(src, pos+i, blockEnd)
	}
	f.hash8.add(src, pos+1, blockEnd)
}

func (f *levelLazyFastMatchFinder) addShortRepeatPositions(src []byte, pos, blockEnd int) {
	f.hash4.add(src, pos, blockEnd)
	f.hash4.add(src, pos+1, blockEnd)
}

func (f *levelLazyFastMatchFinder) addAdvancedRepeatPositions(src []byte, pos, blockEnd int) {
	f.hash4.add(src, pos, blockEnd)
	f.hash4.add(src, pos+1, blockEnd)
	f.hash8.add(src, pos, blockEnd)
}

func (f *levelLazyFastMatchFinder) addAfterMatch(src []byte, searchPos, matchPos, matchLength, blockEnd int) {
	f.hash4.add(src, searchPos+3, blockEnd)
	f.hash8.add(src, searchPos+3, blockEnd)
	f.hash4.add(src, matchPos+matchLength-1, blockEnd)
	f.hash8.add(src, matchPos+matchLength-2, blockEnd)
	f.hash4.add(src, matchPos+matchLength-3, blockEnd)
	f.hash8.add(src, matchPos+matchLength-4, blockEnd)
}

func (f *levelLazyMatchFinder) find(src []byte, pos, blockEnd, repLength int) levelMatch {
	match := levelMatch{}
	f.hash4.forEachCandidateAndUpdate(src, pos, blockEnd, func(prev int) {
		if !matchEndEqual(src, pos, prev, match.length) {
			return
		}
		length := matchLengthAtWindow(src, pos, prev, blockEnd, 4, f.hash4.maxDistance)
		distance := pos - prev
		if length >= repLength+2 && length > match.length && (length >= 5 || distance < 1<<16) {
			match = levelMatch{pos: prev, length: length}
		}
	})

	if match.length >= 4 {
		f.hash8.forEachCandidateAndUpdate(src, pos, blockEnd, func(prev int) {
			if !matchEndEqual(src, pos, prev, match.length) {
				return
			}
			if length := matchLengthAtWindow(src, pos, prev, blockEnd, 8, f.hash8.maxDistance); length > match.length {
				match = levelMatch{pos: prev, length: length}
			}
		})
	}
	return match
}

func (f *levelLazyMatchFinder) findLazy1(src []byte, pos, blockEnd, currentLength int) levelMatch {
	var hash4Candidates [16]int
	var hash8Candidates [16]int
	entries := min(f.hash4.entries, len(hash4Candidates))
	f.hash8.candidatesAndUpdate(src, pos, blockEnd, hash8Candidates[:entries])
	f.hash4.candidatesAndUpdate(src, pos, blockEnd, hash4Candidates[:entries])

	match := levelMatch{}
	for i := 0; i < entries; i++ {
		prev8 := hash8Candidates[i]
		prev4 := hash4Candidates[i]
		matched8 := prev8 >= 0 && matchEndEqual(src, pos, prev8, currentLength)
		matched4 := prev4 >= 0 && matchEndEqual(src, pos, prev4, currentLength)
		if !matched8 && !matched4 {
			continue
		}
		prev := prev4
		if matched8 {
			prev = prev8
		}
		if length := matchLengthAtWindow(src, pos, prev, blockEnd, 4, f.hash4.maxDistance); length > currentLength && length > match.length {
			match = levelMatch{pos: prev, length: length}
		}
	}
	return match
}

func (f *levelLazyMatchFinder) findLazy2(src []byte, pos, blockEnd, currentLength int) levelMatch {
	f.hash4.push(src, pos, blockEnd)

	var hash8Candidates [16]int
	entries := min(f.hash8.entries, len(hash8Candidates))
	f.hash8.candidatesAndUpdate(src, pos, blockEnd, hash8Candidates[:entries])

	match := levelMatch{}
	for i := 0; i < entries; i++ {
		prev := hash8Candidates[i]
		if prev < 0 || !matchEndEqual(src, pos, prev, currentLength) {
			continue
		}
		if length := matchLengthAtWindow(src, pos, prev, blockEnd, 8, f.hash8.maxDistance); length > currentLength && length > match.length {
			match = levelMatch{pos: prev, length: length}
		}
	}
	return match
}

func (f *levelLazyMatchFinder) addLongRepeatPositions(src []byte, pos, blockEnd int) {
	for i := 0; i <= 4; i++ {
		f.hash4.push(src, pos+i, blockEnd)
	}
	f.hash8.push(src, pos+1, blockEnd)
}

func (f *levelLazyMatchFinder) addShortRepeatPositions(src []byte, pos, blockEnd int) {
	f.hash4.push(src, pos, blockEnd)
	f.hash4.push(src, pos+1, blockEnd)
}

func (f *levelLazyMatchFinder) addAdvancedRepeatPositions(src []byte, pos, blockEnd int) {
	f.hash4.push(src, pos, blockEnd)
	f.hash4.push(src, pos+1, blockEnd)
	f.hash8.push(src, pos, blockEnd)
}

func (f *levelLazyMatchFinder) addAfterMatch(src []byte, searchPos, matchPos, matchLength, blockEnd int) {
	updateEnd := matchPos + min(matchLength, 16)
	for pos := searchPos + 3; pos < updateEnd; pos++ {
		f.hash4.push(src, pos, blockEnd)
		f.hash8.push(src, pos, blockEnd)
	}
}

func matchEndEqual(src []byte, pos, prev, length int) bool {
	return pos+length < len(src) && src[pos+length] == src[prev+length]
}

func matchLengthAtWindow(src []byte, pos, prev, blockEnd, minLen, maxDistance int) int {
	if pos+minLen > blockEnd || prev < 0 || prev >= pos || pos-prev > maxDistance {
		return 0
	}
	return matchLengthFromCandidate(src, pos, prev, blockEnd, minLen)
}

func matchLengthFromCandidate(src []byte, pos, prev, blockEnd, minLen int) int {
	length := commonMatchLengthUnchecked(src, pos, prev, blockEnd)
	if length < minLen {
		return 0
	}
	return length
}

func findRepeatDistanceMatch(src []byte, pos, blockEnd, distance, minLen, maxDistance int) levelMatch {
	if distance <= 0 || distance > maxDistance || pos-distance < 0 || pos+minLen > blockEnd {
		return levelMatch{}
	}
	prev := pos - distance
	length := commonMatchLengthUnchecked(src, pos, prev, blockEnd)
	if length < minLen {
		return levelMatch{}
	}
	return levelMatch{pos: pos, length: length}
}
