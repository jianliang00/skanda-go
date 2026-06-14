package skanda

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"sync"
)

const (
	minMatchLength = 2
	lastBytes      = 31
	maxBlockSize   = 262143
	maxDistance    = 1<<31 - 1
	maxStdDistance = 1<<24 - 1

	entropyRaw     = 0
	entropyHuffman = 1
	entropyRLE     = 2

	blockCompressed = 0
	blockRaw        = 1

	blockLast = 1

	streamDistanceAdvanced = 1
	streamLiteralsDelta    = 1
	streamLiteralsPosMask3 = 4

	maxSharedEncoderSourceSize = 16 << 20
	maxSharedDecoderOutputSize = 16 << 20
)

var entropyCountLog2 [maxBlockSize + 1]float64
var sharedEncoderPool sync.Pool
var sharedDecoderPool sync.Pool

func init() {
	for i := 1; i < len(entropyCountLog2); i++ {
		entropyCountLog2[i] = float64(i) * math.Log2(float64(i))
	}
}

var windowLogs = [...]int{31, 24, 24, 23, 23, 22, 22, 21, 21, 20, 20}

var (
	ErrCorrupt            = errors.New("skanda: corrupt input")
	ErrUnsupportedEntropy = errors.New("skanda: unsupported entropy stream")
)

type ProgressFunc func(processedBytes, compressedBytes int) bool

type Options struct {
	// Level follows Skanda v1.0's public range and is clamped to 0..10.
	Level        int
	DecSpeedBias float64
	Progress     ProgressFunc
}

type Option func(*Options)

type decodeState struct {
	scratch         []byte
	distanceScratch []uint32
	huffmanTable    [huffmanCodeSpace]huffmanEntry
}

type Encoder struct {
	state             compressState
	levelOptions      compressorLevelOptions
	levelOptionsValid bool
	sourceSize        int
	matchState        *optimalMatchState
	splitter          *blockSplitter
}

type Decoder struct {
	state decodeState
}

func (state *decodeState) reset() {
	if state != nil {
		state.scratch = state.scratch[:0]
		state.distanceScratch = state.distanceScratch[:0]
	}
}

func (state *decodeState) release() {
	if state == nil {
		return
	}
	releaseByteBuffer(state.scratch)
	state.scratch = nil
	releaseUint32Buffer(state.distanceScratch)
	state.distanceScratch = nil
}

func (state *decodeState) alloc(size int) []byte {
	if size <= 0 {
		return nil
	}
	if state == nil {
		return make([]byte, size)
	}
	start := len(state.scratch)
	need := start + size
	if cap(state.scratch) < need {
		nextCap := cap(state.scratch) * 2
		if nextCap < need {
			nextCap = need
		}
		next := make([]byte, need, nextCap)
		copy(next, state.scratch)
		state.scratch = next
	} else {
		state.scratch = state.scratch[:need]
	}
	return state.scratch[start:need]
}

func (state *decodeState) uint32s(size int) []uint32 {
	if size <= 0 {
		return nil
	}
	if state == nil {
		return make([]uint32, size)
	}
	if cap(state.distanceScratch) < size {
		releaseUint32Buffer(state.distanceScratch)
		state.distanceScratch = acquireUint32Buffer(size)
	} else {
		state.distanceScratch = state.distanceScratch[:size]
	}
	return state.distanceScratch
}

func WithLevel(level int) Option {
	return func(o *Options) {
		o.Level = level
	}
}

func WithDecSpeedBias(decSpeedBias float64) Option {
	return func(o *Options) {
		o.DecSpeedBias = decSpeedBias
	}
}

func WithProgress(progress ProgressFunc) Option {
	return func(o *Options) {
		o.Progress = progress
	}
}

func defaultOptions() Options {
	return Options{
		Level:        2,
		DecSpeedBias: 0.5,
	}
}

func normalizeOptions(options []Option) Options {
	opts := defaultOptions()
	for _, option := range options {
		option(&opts)
	}
	if opts.Level < 0 {
		opts.Level = 0
	}
	if opts.Level > 10 {
		opts.Level = 10
	}
	if opts.DecSpeedBias < 0 {
		opts.DecSpeedBias = 0
	}
	if opts.DecSpeedBias > 1 {
		opts.DecSpeedBias = 1
	}
	return opts
}

func CompressBound(size int) int {
	if size < 0 {
		return 0
	}
	return size + size/1024 + 128
}

func Compress(src []byte, options ...Option) ([]byte, error) {
	return Encode(nil, src, options...)
}

func Encode(dst, src []byte, options ...Option) ([]byte, error) {
	if len(src) <= lastBytes+32 || len(src) > maxSharedEncoderSourceSize {
		return encodeFresh(dst, src, options...)
	}
	encoder, _ := sharedEncoderPool.Get().(*Encoder)
	if encoder == nil {
		encoder = new(Encoder)
	}
	encoded, err := encoder.Encode(dst, src, options...)
	sharedEncoderPool.Put(encoder)
	return encoded, err
}

func encodeFresh(dst, src []byte, options ...Option) ([]byte, error) {
	opts := normalizeOptions(options)
	baseLen := len(dst)
	dst = growEncodeBuffer(dst, src, opts.Level)
	if len(src) <= lastBytes+32 {
		dst = writeHeader(dst, len(src), blockRaw, blockLast)
		dst = append(dst, src...)
		if opts.Progress != nil {
			opts.Progress(len(src), len(dst)-baseLen)
		}
		return dst, nil
	}

	limit := len(src) - lastBytes
	levelOptions := compressorLevelOptionsForSize(opts.Level, opts.DecSpeedBias, len(src))
	state := newCompressState()
	defer state.release()
	levelOptions.matchState = newOptimalMatchState(len(src), levelOptions)
	if levelOptions.matchState != nil {
		defer levelOptions.matchState.release()
	}
	splitter := newOptimalBlockSplitter(levelOptions)
	if splitter != nil {
		defer splitter.release()
	}
	for pos := 0; pos < limit; {
		blockSize := min(levelMaxBlockSize(levelOptions), limit-pos)
		blockOptions := levelOptions
		if splitter != nil {
			blockSize = splitter.getBlockSize(src, 0, pos, blockSize, levelOptions)
			blockOptions.initialCostModel = splitter.initialCostModel()
		}
		blockEnd := pos + blockSize
		nextState := *state
		encoded := compressBlockLevel(src, pos, blockEnd, &nextState, blockOptions)
		rawSize := 3 + blockEnd - pos
		if len(encoded.data) < rawSize {
			dst = append(dst, encoded.data...)
			*state = nextState
		} else {
			releaseUnusedCompressState(&nextState, state)
			dst = writeHeader(dst, blockEnd-pos, blockRaw, 0)
			dst = append(dst, src[pos:blockEnd]...)
		}
		pos = blockEnd
		if opts.Progress != nil {
			if opts.Progress(pos, len(dst)-baseLen) {
				return dst, nil
			}
		}
	}

	dst = writeHeader(dst, lastBytes, blockRaw, blockLast)
	dst = append(dst, src[limit:]...)
	if opts.Progress != nil {
		opts.Progress(len(src), len(dst)-baseLen)
	}
	return dst, nil
}

func (encoder *Encoder) Encode(dst, src []byte, options ...Option) ([]byte, error) {
	if encoder == nil {
		return encodeFresh(dst, src, options...)
	}
	opts := normalizeOptions(options)
	baseLen := len(dst)
	dst = growEncodeBuffer(dst, src, opts.Level)
	if len(src) <= lastBytes+32 {
		dst = writeHeader(dst, len(src), blockRaw, blockLast)
		dst = append(dst, src...)
		if opts.Progress != nil {
			opts.Progress(len(src), len(dst)-baseLen)
		}
		return dst, nil
	}

	limit := len(src) - lastBytes
	levelOptions := compressorLevelOptionsForSize(opts.Level, opts.DecSpeedBias, len(src))
	state, splitter := encoder.encodeWorkspace(len(src), &levelOptions)
	for pos := 0; pos < limit; {
		blockSize := min(levelMaxBlockSize(levelOptions), limit-pos)
		blockOptions := levelOptions
		if splitter != nil {
			blockSize = splitter.getBlockSize(src, 0, pos, blockSize, levelOptions)
			blockOptions.initialCostModel = splitter.initialCostModel()
		}
		blockEnd := pos + blockSize
		nextState := *state
		encoded := compressBlockLevel(src, pos, blockEnd, &nextState, blockOptions)
		rawSize := 3 + blockEnd - pos
		if len(encoded.data) < rawSize {
			dst = append(dst, encoded.data...)
			*state = nextState
		} else {
			releaseUnusedCompressState(&nextState, state)
			dst = writeHeader(dst, blockEnd-pos, blockRaw, 0)
			dst = append(dst, src[pos:blockEnd]...)
		}
		pos = blockEnd
		if opts.Progress != nil {
			if opts.Progress(pos, len(dst)-baseLen) {
				return dst, nil
			}
		}
	}

	dst = writeHeader(dst, lastBytes, blockRaw, blockLast)
	dst = append(dst, src[limit:]...)
	if opts.Progress != nil {
		opts.Progress(len(src), len(dst)-baseLen)
	}
	return dst, nil
}

func (encoder *Encoder) Close() {
	if encoder == nil {
		return
	}
	encoder.state.release()
	if encoder.matchState != nil {
		encoder.matchState.release()
		encoder.matchState = nil
	}
	if encoder.splitter != nil {
		encoder.splitter.release()
		encoder.splitter = nil
	}
	encoder.levelOptionsValid = false
	encoder.sourceSize = 0
}

func (encoder *Encoder) encodeWorkspace(sourceSize int, levelOptions *compressorLevelOptions) (*compressState, *blockSplitter) {
	if !encoder.levelOptionsValid || encoder.sourceSize != sourceSize || !sameEncoderLevelOptions(encoder.levelOptions, *levelOptions) {
		encoder.state.release()
		if encoder.matchState != nil {
			encoder.matchState.release()
			encoder.matchState = nil
		}
		if encoder.splitter != nil {
			encoder.splitter.release()
			encoder.splitter = nil
		}
		encoder.levelOptions = *levelOptions
		encoder.levelOptionsValid = true
		encoder.sourceSize = sourceSize
	}
	encoder.state.resetForEncode()
	if needsOptimalMatchState(*levelOptions) {
		if encoder.matchState == nil {
			encoder.matchState = newOptimalMatchState(sourceSize, *levelOptions)
		} else {
			encoder.matchState.resetForEncode()
		}
		levelOptions.matchState = encoder.matchState
	}
	if useOptimalBlockSplitter(*levelOptions) {
		if encoder.splitter == nil {
			encoder.splitter = newOptimalBlockSplitter(*levelOptions)
		} else {
			encoder.splitter.reset()
		}
	}
	return &encoder.state, encoder.splitter
}

func sameEncoderLevelOptions(a, b compressorLevelOptions) bool {
	return a.level == b.level &&
		a.parser == b.parser &&
		a.windowLog == b.windowLog &&
		a.hashLog == b.hashLog &&
		a.hashBytes == b.hashBytes &&
		a.minMatchLength == b.minMatchLength &&
		a.hashEntriesLog == b.hashEntriesLog &&
		a.niceLength == b.niceLength &&
		a.optimalBlockSize == b.optimalBlockSize &&
		a.maxArrivals == b.maxArrivals &&
		a.parserIterations == b.parserIterations &&
		a.decSpeedBias == b.decSpeedBias
}

func needsOptimalMatchState(opts compressorLevelOptions) bool {
	opts = normalizeCompressorLevelOptions(opts)
	return opts.parser == compressorParserOptimal1 || opts.parser == compressorParserOptimal2 || opts.parser == compressorParserOptimal3
}

func growAppendBuffer(dst []byte, extra int) []byte {
	if extra <= cap(dst)-len(dst) {
		return dst
	}
	next := make([]byte, len(dst), len(dst)+extra)
	copy(next, dst)
	return next
}

func growEncodeBuffer(dst, src []byte, level int) []byte {
	bound := CompressBound(len(src))
	if bound <= cap(dst)-len(dst) {
		return dst
	}
	if len(dst) == 0 && cap(dst) == 0 {
		return growAppendBuffer(dst, initialEncodeCapacity(src, bound, level))
	}
	return growAppendBuffer(dst, bound)
}

func initialEncodeCapacity(src []byte, bound, level int) int {
	sourceSize := len(src)
	if sourceSize <= 128<<10 {
		return bound
	}
	if !hasCompressibleSample(src) {
		return bound
	}
	divisor := 4
	if level <= 6 {
		divisor = 8
	}
	if level >= 7 {
		divisor = 2
	}
	hint := sourceSize/divisor + 128
	if hint < 64<<10 {
		hint = 64 << 10
	}
	if hint > bound {
		return bound
	}
	return hint
}

func hasCompressibleSample(src []byte) bool {
	const maxSamples = 4096
	sampleCount := min(len(src), maxSamples)
	if sampleCount <= 0 {
		return false
	}
	var counts [256]uint16
	stride := len(src) / sampleCount
	pos := 0
	threshold := uint16(max(1, sampleCount/25))
	for i := 0; i < sampleCount; i++ {
		symbol := src[pos]
		counts[symbol]++
		if counts[symbol] >= threshold {
			return true
		}
		pos += stride
	}
	return false
}

func levelMaxBlockSize(opts compressorLevelOptions) int {
	switch opts.parser {
	case compressorParserUltraFast, compressorParserOptimal1, compressorParserOptimal2, compressorParserOptimal3:
		return maxBlockSize
	}
	if opts.decSpeedBias >= 0.99 {
		return maxBlockSize
	}
	return maxBlockSize / 2
}

func Decompress(src []byte, decompressedSize int) ([]byte, error) {
	if decompressedSize < 0 {
		return nil, ErrCorrupt
	}
	dst := make([]byte, decompressedSize)
	if err := Decode(dst, src); err != nil {
		return nil, err
	}
	return dst, nil
}

func Decode(dst, src []byte) error {
	if len(dst) > maxSharedDecoderOutputSize {
		decodeState := decodeState{scratch: acquireByteBuffer(64 << 10)}
		defer decodeState.release()
		return decodeWithState(dst, src, &decodeState)
	}
	decoder, _ := sharedDecoderPool.Get().(*Decoder)
	if decoder == nil {
		decoder = new(Decoder)
	}
	if decoder.state.scratch == nil {
		decoder.state.scratch = acquireByteBuffer(64 << 10)
	}
	err := decodeWithState(dst, src, &decoder.state)
	decoder.state.reset()
	sharedDecoderPool.Put(decoder)
	return err
}

func (decoder *Decoder) Decode(dst, src []byte) error {
	if decoder == nil {
		return Decode(dst, src)
	}
	if decoder.state.scratch == nil {
		decoder.state.scratch = acquireByteBuffer(64 << 10)
	}
	return decodeWithState(dst, src, &decoder.state)
}

func (decoder *Decoder) Close() {
	if decoder == nil {
		return
	}
	decoder.state.release()
}

func decodeWithState(dst, src []byte, decodeState *decodeState) error {
	if len(src) == 0 {
		return ErrCorrupt
	}
	decodeState.reset()

	cpos := 0
	dpos := 0
	repOffsets := [7]int{1, 1, 1, 1, 1, 1, 1}

	for {
		blockSize, blockType, flags, err := readHeader(src, &cpos)
		if err != nil {
			return err
		}

		switch blockType {
		case blockRaw:
			if dpos+blockSize > len(dst) || cpos+blockSize > len(src) {
				return ErrCorrupt
			}
			copy(dst[dpos:dpos+blockSize], src[cpos:cpos+blockSize])
			cpos += blockSize
			dpos += blockSize
			if flags&blockLast != 0 {
				if dpos != len(dst) {
					return ErrCorrupt
				}
				return nil
			}
		case blockCompressed:
			if flags&blockLast != 0 {
				return ErrCorrupt
			}
			decodeState.reset()
			blockEnd := dpos + blockSize
			if blockEnd > len(dst) {
				return ErrCorrupt
			}
			if len(dst) >= lastBytes && blockEnd > len(dst)-lastBytes {
				return ErrCorrupt
			}
			if dpos == 0 {
				if cpos >= len(src) || blockSize == 0 {
					return ErrCorrupt
				}
				dst[dpos] = src[cpos]
				dpos++
				cpos++
			}

			literals, literalFlags, err := decodeEntropyWithState(src, &cpos, decodeState)
			if err != nil {
				return err
			}
			literalStreamCount := 1
			var literalStreams [4][]byte
			if literalFlags&streamLiteralsPosMask3 != 0 {
				literalStreams[0] = literals
				literalStreamCount = 4
				for i := 1; i < 4; i++ {
					stream, _, err := decodeEntropyWithState(src, &cpos, decodeState)
					if err != nil {
						return err
					}
					literalStreams[i] = stream
				}
			}

			tokens, _, err := decodeEntropyWithState(src, &cpos, decodeState)
			if err != nil {
				return err
			}
			distances, distanceFlags, err := decodeEntropyWithState(src, &cpos, decodeState)
			if err != nil {
				return err
			}
			var advancedDistances []uint32
			if distanceFlags&streamDistanceAdvanced != 0 {
				advancedDistances, err = decodeAdvancedDistancesWithState(src, &cpos, distances, decodeState)
				if err != nil {
					return err
				}
			}
			lengths, _, err := decodeEntropyWithState(src, &cpos, decodeState)
			if err != nil {
				return err
			}
			var lzErr error
			if distanceFlags&streamDistanceAdvanced != 0 {
				if literalFlags&(streamLiteralsPosMask3|streamLiteralsDelta) == 0 {
					lzErr = decodeLZBlockAdvancedRawLiterals(dst, &dpos, blockEnd, literals, tokens, advancedDistances, lengths, &repOffsets)
				} else if literalFlags == streamLiteralsDelta {
					lzErr = decodeLZBlockAdvancedDeltaLiterals(dst, &dpos, blockEnd, literals, tokens, advancedDistances, lengths, &repOffsets)
				} else {
					lzErr = decodeLZBlockAdvanced(dst, &dpos, blockEnd, &literalStreams, literalStreamCount, tokens, advancedDistances, lengths, literalFlags, &repOffsets)
				}
			} else if literalFlags&(streamLiteralsPosMask3|streamLiteralsDelta) == 0 {
				lzErr = decodeLZBlockStandardRawLiterals(dst, &dpos, blockEnd, literals, tokens, distances, lengths, &repOffsets)
			} else {
				literalStreams[0] = literals
				lzErr = decodeLZBlockStandard(dst, &dpos, blockEnd, &literalStreams, literalStreamCount, tokens, distances, lengths, literalFlags, &repOffsets)
			}
			if lzErr != nil {
				return lzErr
			}
			dpos = blockEnd
		default:
			return ErrCorrupt
		}
	}
}

func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedEntropy)
}

func EstimateMemory(size int, level int, decSpeedBias float64) int {
	if size <= lastBytes+1 {
		return 0
	}
	opts := normalizeCompressorLevelOptions(compressorLevelOptionsForSize(level, decSpeedBias, size))
	intSize := bits.UintSize / 8
	blockSize := min(size, maxBlockSize)
	memory := blockSize*3 + 4096

	switch opts.parser {
	case compressorParserUltraFast, compressorParserGreedy:
		memory += (1 << effectiveLevelHashLog(opts)) * intSize
	case compressorParserLazyFast:
		memory += 2 * (1 << effectiveLevelHashLog(opts)) * intSize
	case compressorParserLazy, compressorParserOptimal1:
		entries := 1 << cacheFinderEntriesLog(opts)
		tableSize := (1 << effectiveLevelHashLog(opts)) * entries * intSize
		memory += 2 * tableSize
		if opts.parser == compressorParserOptimal1 {
			memory += (1 << 14) * intSize
		}
	case compressorParserOptimal2, compressorParserOptimal3:
		memory += binaryMatchFinderMemory(size, opts)
		if useBufferedMatches(opts) {
			maxPerPos := min((1<<opts.hashEntriesLog)+1, 24)
			memory += blockSize * (maxPerPos*2*intSize + intSize)
		}
	}
	if opts.parser == compressorParserOptimal1 || opts.parser == compressorParserOptimal2 || opts.parser == compressorParserOptimal3 {
		memory += blockSplitterMemory(opts)
		arrivals := max(opts.maxArrivals, 1)
		memory += (opts.optimalBlockSize + opts.niceLength + 1) * arrivals * 64
	}
	return memory
}

func binaryMatchFinderMemory(blockSize int, opts compressorLevelOptions) int {
	const int32Size = 4
	binaryTreeWindow := min(opts.hashLog, opts.windowLog)
	nodeListSize := blockSize
	if binaryTreeWindow < bits.UintSize-1 {
		windowSize := 1 << binaryTreeWindow
		if blockSize >= windowSize {
			nodeListSize = windowSize
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
	return nodeListSize*2*int32Size + (1<<nodeLookupHashLog)*int32Size + (1<<chain3HashLog)*(1<<chain3EntriesLog)*int32Size
}

func skandaWindowLog(size int, decSpeedBias float64) int {
	if size <= 1 {
		return 6
	}
	if decSpeedBias < 0 {
		decSpeedBias = 0
	}
	if decSpeedBias > 1 {
		decSpeedBias = 1
	}
	index := int(decSpeedBias * 10)
	if index < 0 {
		index = 0
	}
	if index >= len(windowLogs) {
		index = len(windowLogs) - 1
	}
	windowLog := bits.Len(uint(size - 1))
	if windowLog > windowLogs[index] {
		windowLog = windowLogs[index]
	}
	if windowLog < 6 {
		windowLog = 6
	}
	return windowLog
}

type blockLiterals struct {
	raw             []byte
	delta           []byte
	pos             [4][]byte
	posDelta        [4][]byte
	deltaOK         bool
	collectAdvanced bool
	collectPos      bool
	preallocated    bool
}

func newBlockLiterals(src []byte, blockStart, blockEnd int, decSpeedBias float64) blockLiterals {
	return newBlockLiteralsWithPos(src, blockStart, blockEnd, decSpeedBias, false)
}

func newBlockLiteralsWithPos(src []byte, blockStart, blockEnd int, decSpeedBias float64, forcePos bool) blockLiterals {
	return newCompressionBlockLiterals(nil, src, blockStart, blockEnd, decSpeedBias, forcePos)
}

func newCompressionBlockLiterals(state *compressState, src []byte, blockStart, blockEnd int, decSpeedBias float64, forcePos bool) blockLiterals {
	collectAdvanced := decSpeedBias < 0.6
	literals := blockLiterals{
		deltaOK:         true,
		collectAdvanced: collectAdvanced,
		collectPos:      collectAdvanced && (forcePos || getBlockPosBits(src, blockStart, blockEnd, decSpeedBias) != 0),
	}
	if state == nil {
		return literals
	}
	literals.preallocated = true
	blockSize := blockEnd - blockStart
	if blockSize < 0 {
		blockSize = 0
	}
	literals.raw = compressionByteBuffer(&state.literalRawScratch, blockSize)
	if literals.collectAdvanced {
		literals.delta = compressionByteBuffer(&state.literalDeltaScratch, blockSize)
	}
	if literals.collectPos {
		perStream := (blockSize + 3) / 4
		for i := range literals.pos {
			literals.pos[i] = compressionByteBuffer(&state.literalPosScratch[i], perStream)
			if literals.collectAdvanced {
				literals.posDelta[i] = compressionByteBuffer(&state.literalPosDeltaScratch[i], perStream)
			}
		}
	}
	return literals
}

func (l *blockLiterals) appendRun(src []byte, start, end, distance int, tokens, lengths *[]byte) {
	runLength := end - start
	if runLength >= 7 {
		*tokens = append(*tokens, 7<<5)
		encodeLength(lengths, runLength, 7)
	} else {
		*tokens = append(*tokens, byte(runLength<<5))
	}
	if runLength <= 0 {
		return
	}
	literalRun := src[start:end]
	if runLength <= 8 && l.preallocated && !l.collectPos && l.collectAdvanced && l.deltaOK && distance > 0 && start-distance >= 0 {
		rawOldLen := len(l.raw)
		deltaOldLen := len(l.delta)
		needRaw := rawOldLen + runLength
		needDelta := deltaOldLen + runLength
		l.raw = l.raw[:needRaw]
		l.delta = l.delta[:needDelta]
		rawRun := l.raw[rawOldLen:needRaw]
		deltaRun := l.delta[deltaOldLen:needDelta]
		ref := src[start-distance : end-distance]
		switch runLength {
		case 1:
			rawRun[0] = literalRun[0]
			deltaRun[0] = literalRun[0] - ref[0]
		case 2:
			rawRun[0] = literalRun[0]
			rawRun[1] = literalRun[1]
			deltaRun[0] = literalRun[0] - ref[0]
			deltaRun[1] = literalRun[1] - ref[1]
		case 3:
			rawRun[0] = literalRun[0]
			rawRun[1] = literalRun[1]
			rawRun[2] = literalRun[2]
			deltaRun[0] = literalRun[0] - ref[0]
			deltaRun[1] = literalRun[1] - ref[1]
			deltaRun[2] = literalRun[2] - ref[2]
		case 4:
			rawRun[0] = literalRun[0]
			rawRun[1] = literalRun[1]
			rawRun[2] = literalRun[2]
			rawRun[3] = literalRun[3]
			deltaRun[0] = literalRun[0] - ref[0]
			deltaRun[1] = literalRun[1] - ref[1]
			deltaRun[2] = literalRun[2] - ref[2]
			deltaRun[3] = literalRun[3] - ref[3]
		default:
			for i, value := range literalRun {
				rawRun[i] = value
				deltaRun[i] = value - ref[i]
			}
		}
		return
	}
	if l.preallocated {
		oldLen := len(l.raw)
		need := oldLen + runLength
		l.raw = l.raw[:need]
		copy(l.raw[oldLen:need], literalRun)
	} else {
		l.raw = append(l.raw, literalRun...)
	}
	if !l.collectPos {
		if !l.collectAdvanced || !l.deltaOK {
			return
		}
		if distance <= 0 || start-distance < 0 {
			l.deltaOK = false
			return
		}
		oldLen := len(l.delta)
		need := oldLen + runLength
		if l.preallocated {
			l.delta = l.delta[:need]
		} else if cap(l.delta) < need {
			next := make([]byte, need, need*2)
			copy(next, l.delta)
			l.delta = next
		} else {
			l.delta = l.delta[:need]
		}
		deltaRun := l.delta[oldLen:need]
		ref := src[start-distance : end-distance]
		for i, value := range literalRun {
			deltaRun[i] = value - ref[i]
		}
		return
	}
	if !l.collectAdvanced || !l.deltaOK || distance <= 0 || start-distance < 0 {
		if l.collectAdvanced {
			l.deltaOK = false
		}
		for pos := start; pos < end; pos++ {
			stream := pos & 3
			l.pos[stream] = append(l.pos[stream], src[pos])
		}
		return
	}
	for pos := start; pos < end; pos++ {
		value := src[pos]
		stream := pos & 3
		delta := value - src[pos-distance]
		l.pos[stream] = append(l.pos[stream], value)
		l.delta = append(l.delta, delta)
		l.posDelta[stream] = append(l.posDelta[stream], delta)
	}
}

func (l *blockLiterals) appendRunLevel0(src []byte, start, end, distance int, tokens, lengths *[]byte) {
	runLength := end - start
	if runLength < 5 || runLength > 8 ||
		!l.preallocated || l.collectPos || !l.collectAdvanced || !l.deltaOK ||
		distance <= 0 || start-distance < 0 {
		l.appendRun(src, start, end, distance, tokens, lengths)
		return
	}

	if runLength >= 7 {
		*tokens = append(*tokens, 7<<5)
		encodeLength(lengths, runLength, 7)
	} else {
		*tokens = append(*tokens, byte(runLength<<5))
	}
	literalRun := src[start:end]
	ref := src[start-distance : end-distance]
	rawOldLen := len(l.raw)
	deltaOldLen := len(l.delta)
	needRaw := rawOldLen + runLength
	needDelta := deltaOldLen + runLength
	l.raw = l.raw[:needRaw]
	l.delta = l.delta[:needDelta]
	rawRun := l.raw[rawOldLen:needRaw]
	deltaRun := l.delta[deltaOldLen:needDelta]

	rawRun[0] = literalRun[0]
	rawRun[1] = literalRun[1]
	rawRun[2] = literalRun[2]
	rawRun[3] = literalRun[3]
	rawRun[4] = literalRun[4]
	deltaRun[0] = literalRun[0] - ref[0]
	deltaRun[1] = literalRun[1] - ref[1]
	deltaRun[2] = literalRun[2] - ref[2]
	deltaRun[3] = literalRun[3] - ref[3]
	deltaRun[4] = literalRun[4] - ref[4]
	if runLength == 5 {
		return
	}
	rawRun[5] = literalRun[5]
	deltaRun[5] = literalRun[5] - ref[5]
	if runLength == 6 {
		return
	}
	rawRun[6] = literalRun[6]
	deltaRun[6] = literalRun[6] - ref[6]
	if runLength == 7 {
		return
	}
	rawRun[7] = literalRun[7]
	deltaRun[7] = literalRun[7] - ref[7]
}

func (l *blockLiterals) encode(decSpeedBias float64) []byte {
	return l.encodeWithCodegen(decSpeedBias, true)
}

func (l *blockLiterals) encodeWithCodegen(decSpeedBias float64, useFastCodegen bool) []byte {
	return l.appendEncodedWithCodegen(nil, decSpeedBias, useFastCodegen)
}

func (l *blockLiterals) appendEncoded(dst []byte, decSpeedBias float64) []byte {
	return l.appendEncodedWithCodegen(dst, decSpeedBias, true)
}

func (l *blockLiterals) appendEncodedWithCodegen(dst []byte, decSpeedBias float64, useFastCodegen bool) []byte {
	if !l.collectAdvanced {
		return appendEntropyWithCodegen(dst, l.raw, 0, decSpeedBias, useFastCodegen)
	}

	useDelta := false
	if l.deltaOK && len(l.delta) == len(l.raw) {
		if l.collectPos {
			rawSize := 0
			deltaSize := 0
			for stream := range l.pos {
				rawSize += entropyModeQuickSize(l.pos[stream], decSpeedBias, true)
				deltaSize += entropyModeQuickSize(l.posDelta[stream], decSpeedBias, true)
				deltaSize += int(float64(len(l.posDelta[stream])) * (decSpeedBias/4 + 0.1/8))
			}
			useDelta = rawSize > deltaSize
		} else {
			rawSize := entropyModeQuickSize(l.raw, decSpeedBias, true)
			deltaSize := entropyModeQuickSize(l.delta, decSpeedBias, true)
			deltaSize += int(float64(len(l.delta)) * (decSpeedBias/4 + 0.1/8))
			useDelta = rawSize > deltaSize
		}
	}

	if l.collectPos {
		if useDelta {
			return appendLiteralSubstreamsWithCodegen(dst, l.posDelta[:], streamLiteralsDelta|streamLiteralsPosMask3, decSpeedBias, useFastCodegen)
		}
		return appendLiteralSubstreamsWithCodegen(dst, l.pos[:], streamLiteralsPosMask3, decSpeedBias, useFastCodegen)
	}

	if useDelta {
		return appendEntropyWithCodegen(dst, l.delta, streamLiteralsDelta, decSpeedBias, useFastCodegen)
	}
	return appendEntropyWithCodegen(dst, l.raw, 0, decSpeedBias, useFastCodegen)
}

func (l *blockLiterals) appendEncodedWithStripedHistogram(dst []byte, decSpeedBias float64, useFastCodegen bool) []byte {
	if !l.collectAdvanced {
		return appendEntropyWithStripedHistogram(dst, l.raw, 0, decSpeedBias, useFastCodegen)
	}

	useDelta := false
	if l.deltaOK && len(l.delta) == len(l.raw) {
		if l.collectPos {
			rawSize := 0
			deltaSize := 0
			for stream := range l.pos {
				rawSize += entropyModeQuickSizeStriped(l.pos[stream], decSpeedBias, true)
				deltaSize += entropyModeQuickSizeStriped(l.posDelta[stream], decSpeedBias, true)
				deltaSize += int(float64(len(l.posDelta[stream])) * (decSpeedBias/4 + 0.1/8))
			}
			useDelta = rawSize > deltaSize
		} else {
			rawSize := entropyModeQuickSizeStriped(l.raw, decSpeedBias, true)
			deltaSize := entropyModeQuickSizeStriped(l.delta, decSpeedBias, true)
			deltaSize += int(float64(len(l.delta)) * (decSpeedBias/4 + 0.1/8))
			useDelta = rawSize > deltaSize
		}
	}

	if l.collectPos {
		if useDelta {
			return appendLiteralSubstreamsWithStripedHistogram(dst, l.posDelta[:], streamLiteralsDelta|streamLiteralsPosMask3, decSpeedBias, useFastCodegen)
		}
		return appendLiteralSubstreamsWithStripedHistogram(dst, l.pos[:], streamLiteralsPosMask3, decSpeedBias, useFastCodegen)
	}
	if useDelta {
		return appendEntropyWithStripedHistogram(dst, l.delta, streamLiteralsDelta, decSpeedBias, useFastCodegen)
	}
	return appendEntropyWithStripedHistogram(dst, l.raw, 0, decSpeedBias, useFastCodegen)
}

func appendLiteralSubstreamsWithCodegen(dst []byte, streams [][]byte, flags int, decSpeedBias float64, useFastCodegen bool) []byte {
	for _, stream := range streams {
		dst = appendEntropyWithCodegen(dst, stream, flags, decSpeedBias, useFastCodegen)
	}
	return dst
}

func appendLiteralSubstreamsWithStripedHistogram(dst []byte, streams [][]byte, flags int, decSpeedBias float64, useFastCodegen bool) []byte {
	for _, stream := range streams {
		dst = appendEntropyWithStripedHistogram(dst, stream, flags, decSpeedBias, useFastCodegen)
	}
	return dst
}

func entropyModeQuickSize(data []byte, decSpeedBias float64, doCompressibilityCheck bool) int {
	symbolCount := len(data)
	if symbolCount >= 2 {
		allSame := true
		for _, value := range data[1:] {
			if value != data[0] {
				allSame = false
				break
			}
		}
		if allSame {
			return 0
		}
	}

	minSymbols := 32
	if doCompressibilityCheck {
		minSymbols = 256
	}
	if symbolCount < minSymbols || decSpeedBias >= 0.99 || !enableHuffmanEncoding {
		return symbolCount
	}

	maxBitsPerByte := 8.0 - 4.0*decSpeedBias - 0.05
	var hist [256]int
	readSymbols := 0
	if doCompressibilityCheck {
		if symbolCount <= 8192+128 {
			readSymbols = symbolCount
			addEntropyQuickHist(hist[:], data[:readSymbols])
		} else {
			readSymbols = 8192
			quarter := readSymbols / 4
			half := readSymbols / 2
			addEntropyQuickHist(hist[:], data[:quarter])
			addEntropyQuickHist(hist[:], data[symbolCount/2-quarter:symbolCount/2-quarter+half])
			addEntropyQuickHist(hist[:], data[symbolCount-quarter:])
		}
		entropy := entropyBits(hist[:], readSymbols)
		headerBias := 1024 * readSymbols / symbolCount
		if float64(readSymbols)*entropy+float64(headerBias) > float64(readSymbols)*maxBitsPerByte {
			return symbolCount
		}
		return int(float64(symbolCount) * entropy / 8)
	}

	readSymbols = symbolCount
	addEntropyQuickHist(hist[:], data)
	entropy := entropyBits(hist[:], readSymbols)
	if entropy < 0 {
		return 0
	}
	return int(float64(symbolCount) * entropy / 8)
}

func addEntropyQuickHist(hist []int, data []byte) {
	for _, value := range data {
		hist[value]++
	}
}

func entropyModeQuickSizeStriped(data []byte, decSpeedBias float64, doCompressibilityCheck bool) int {
	symbolCount := len(data)
	if symbolCount >= 2 {
		allSame := true
		for _, value := range data[1:] {
			if value != data[0] {
				allSame = false
				break
			}
		}
		if allSame {
			return 0
		}
	}

	minSymbols := 32
	if doCompressibilityCheck {
		minSymbols = 256
	}
	if symbolCount < minSymbols || decSpeedBias >= 0.99 || !enableHuffmanEncoding {
		return symbolCount
	}

	maxBitsPerByte := 8.0 - 4.0*decSpeedBias - 0.05
	var hist [256]int
	readSymbols := 0
	if doCompressibilityCheck {
		if symbolCount <= 8192+128 {
			readSymbols = symbolCount
			addEntropyQuickHistStriped(hist[:], data[:readSymbols])
		} else {
			readSymbols = 8192
			quarter := readSymbols / 4
			half := readSymbols / 2
			addEntropyQuickHistStriped(hist[:], data[:quarter])
			addEntropyQuickHistStriped(hist[:], data[symbolCount/2-quarter:symbolCount/2-quarter+half])
			addEntropyQuickHistStriped(hist[:], data[symbolCount-quarter:])
		}
		entropy := entropyBits(hist[:], readSymbols)
		headerBias := 1024 * readSymbols / symbolCount
		if float64(readSymbols)*entropy+float64(headerBias) > float64(readSymbols)*maxBitsPerByte {
			return symbolCount
		}
		return int(float64(symbolCount) * entropy / 8)
	}

	readSymbols = symbolCount
	addEntropyQuickHistStriped(hist[:], data)
	entropy := entropyBits(hist[:], readSymbols)
	if entropy < 0 {
		return 0
	}
	return int(float64(symbolCount) * entropy / 8)
}

func addEntropyQuickHistStriped(hist []int, data []byte) {
	if len(data) < 1024 {
		addEntropyQuickHist(hist, data)
		return
	}
	var hist0, hist1, hist2, hist3 [256]int
	pos := 0
	limit := len(data) &^ 3
	for pos < limit {
		hist0[data[pos]]++
		hist1[data[pos+1]]++
		hist2[data[pos+2]]++
		hist3[data[pos+3]]++
		pos += 4
	}
	for i := range hist0 {
		hist[i] += hist0[i] + hist1[i] + hist2[i] + hist3[i]
	}
	for _, value := range data[pos:] {
		hist[value]++
	}
}

func getBlockPosBits(src []byte, blockStart, blockEnd int, decSpeedBias float64) int {
	count := blockEnd - blockStart
	if count < 16384 {
		return 0
	}
	histBytes := count / 16
	if histBytes > 4096 {
		histBytes = 4096
	}
	if histBytes == 0 {
		return 0
	}

	var rawHist [256]int
	var posHist [4][256]int
	var posCounts [4]int

	firstLen := histBytes / 4
	middleLen := histBytes / 2
	addBlockPosHistSample(src, blockStart, blockEnd, blockStart, firstLen, &rawHist, &posHist, &posCounts)
	addBlockPosHistSample(src, blockStart, blockEnd, blockStart+((count-firstLen)&^3), firstLen, &rawHist, &posHist, &posCounts)
	addBlockPosHistSample(src, blockStart, blockEnd, blockStart+((count/2-firstLen)&^3), middleLen, &rawHist, &posHist, &posCounts)

	total := firstLen*2 + middleLen
	if total == 0 {
		return 0
	}
	rawEntropy := entropyBits(rawHist[:], total)
	posEntropy := 0.0
	for stream := 0; stream < 4; stream++ {
		if posCounts[stream] == 0 {
			continue
		}
		posEntropy += entropyBits(posHist[stream][:], posCounts[stream])
	}
	posEntropy /= 4

	lhs := float64(count) / 4 * (posEntropy + decSpeedBias*2 + 0.10)
	lhs += 512 * 8
	rhs := float64(count) / 4 * rawEntropy
	if lhs < rhs {
		return 2
	}
	return 0
}

func addBlockPosHistSample(src []byte, blockStart, blockEnd, start, length int, rawHist *[256]int, posHist *[4][256]int, posCounts *[4]int) {
	if length <= 0 {
		return
	}
	if start < blockStart {
		start = blockStart
	}
	end := start + length
	if end > blockEnd {
		end = blockEnd
	}
	if start >= end {
		return
	}
	for start < end && start&3 != 0 {
		value := src[start]
		rawHist[value]++
		stream := start & 3
		posHist[stream][value]++
		posCounts[stream]++
		start++
	}
	for start+4 <= end {
		value0 := src[start]
		value1 := src[start+1]
		value2 := src[start+2]
		value3 := src[start+3]
		rawHist[value0]++
		rawHist[value1]++
		rawHist[value2]++
		rawHist[value3]++
		posHist[0][value0]++
		posHist[1][value1]++
		posHist[2][value2]++
		posHist[3][value3]++
		posCounts[0]++
		posCounts[1]++
		posCounts[2]++
		posCounts[3]++
		start += 4
	}
	for start < end {
		value := src[start]
		rawHist[value]++
		stream := start & 3
		posHist[stream][value]++
		posCounts[stream]++
		start++
	}
}

func entropyBits(hist []int, total int) float64 {
	if total <= 0 {
		return 0
	}
	totalF := float64(total)
	weighted := 0.0
	for _, count := range hist {
		if count == 0 {
			continue
		}
		if count < len(entropyCountLog2) {
			weighted += entropyCountLog2[count]
		} else {
			weighted += float64(count) * math.Log2(float64(count))
		}
	}
	return math.Log2(totalF) - weighted/totalF
}

func appendDistance(distance int, distances, tokens *[]byte, lastDistance *int) {
	if distance == *lastDistance {
		return
	}
	bytes := (bits.Len(uint(distance))-1)/8 + 1
	if bytes > 3 {
		bytes = 3
	}
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], uint32(distance))
	*distances = append(*distances, tmp[:bytes]...)
	(*tokens)[len(*tokens)-1] |= byte(bytes << 3)
	*lastDistance = distance
}

type advancedDistanceBitWriter struct {
	bytes   []byte
	state   uint64
	count   uint
	enabled bool
}

func (writer *advancedDistanceBitWriter) appendUnchecked(value uint32, count int) {
	writer.state |= uint64(value) << writer.count
	writer.count += uint(count)
	for writer.count >= 8 {
		writer.bytes = append(writer.bytes, byte(writer.state))
		writer.state >>= 8
		writer.count -= 8
	}
}

func (writer *advancedDistanceBitWriter) appendTo(out []byte) []byte {
	if writer == nil || !writer.enabled {
		return out
	}
	if writer.count > 0 {
		writer.bytes = append(writer.bytes, byte(writer.state))
		writer.state = 0
		writer.count = 0
	}
	return append(out, writer.bytes...)
}

func appendAdvancedDistance(distance int, distanceTokens *[]byte, bitWriter *advancedDistanceBitWriter, tokens *[]byte, repOffsets *[3]int) {
	if distance == repOffsets[0] {
		return
	}
	tokenPos := len(*tokens) - 1
	if distance == repOffsets[1] {
		(*tokens)[tokenPos] |= 1 << 3
		repOffsets[1] = repOffsets[0]
		repOffsets[0] = distance
		return
	}
	if distance == repOffsets[2] {
		(*tokens)[tokenPos] |= 2 << 3
		repOffsets[2] = repOffsets[1]
		repOffsets[1] = repOffsets[0]
		repOffsets[0] = distance
		return
	}

	(*tokens)[tokenPos] |= 3 << 3
	repOffsets[2] = repOffsets[1]
	repOffsets[1] = repOffsets[0]
	repOffsets[0] = distance

	virtualDistance := distance + 7
	extraBits := bits.Len(uint(virtualDistance>>3)) - 1
	distanceToken := ((virtualDistance & 7) ^ 7) | (extraBits << 3)
	*distanceTokens = append(*distanceTokens, byte(distanceToken))
	if extraBits > 0 {
		bitWriter.appendUnchecked(uint32((virtualDistance>>3)&((1<<extraBits)-1)), extraBits)
	}
}

func appendAdvancedDistanceNoBits(distance int, distanceTokens *[]byte, tokens *[]byte, repOffsets *[3]int) {
	if distance == repOffsets[0] {
		return
	}
	tokenPos := len(*tokens) - 1
	if distance == repOffsets[1] {
		(*tokens)[tokenPos] |= 1 << 3
		repOffsets[1] = repOffsets[0]
		repOffsets[0] = distance
		return
	}
	if distance == repOffsets[2] {
		(*tokens)[tokenPos] |= 2 << 3
		repOffsets[2] = repOffsets[1]
		repOffsets[1] = repOffsets[0]
		repOffsets[0] = distance
		return
	}

	(*tokens)[tokenPos] |= 3 << 3
	repOffsets[2] = repOffsets[1]
	repOffsets[1] = repOffsets[0]
	repOffsets[0] = distance

	virtualDistance := distance + 7
	extraBits := bits.Len(uint(virtualDistance>>3)) - 1
	distanceToken := ((virtualDistance & 7) ^ 7) | (extraBits << 3)
	*distanceTokens = append(*distanceTokens, byte(distanceToken))
}

func appendAdvancedBlockDistance(distance int, distances *[]byte, bitWriter *advancedDistanceBitWriter, tokens *[]byte, lastDistance *int, repOffsets *[3]int) {
	appendAdvancedDistance(distance, distances, bitWriter, tokens, repOffsets)
	*lastDistance = repOffsets[0]
}

func appendAdvancedBlockDistanceNoBits(distance int, distances *[]byte, tokens *[]byte, lastDistance *int, repOffsets *[3]int) {
	appendAdvancedDistanceNoBits(distance, distances, tokens, repOffsets)
	*lastDistance = repOffsets[0]
}

func appendStandardBlockDistance(distance int, distances *[]byte, tokens *[]byte, lastDistance *int, repOffsets *[3]int) {
	appendDistance(distance, distances, tokens, lastDistance)
	repOffsets[0] = *lastDistance
}

func appendMatchLength(matchLen int, tokens, lengths *[]byte) {
	appendMatchLengthWithMode(matchLen, false, tokens, lengths)
}

func appendMatchLengthWithMode(matchLen int, noHuffman bool, tokens, lengths *[]byte) {
	if matchLen <= 8 {
		(*tokens)[len(*tokens)-1] |= byte(matchLen - minMatchLength)
		return
	}
	if noHuffman && matchLen <= 16 {
		bias := 0
		if matchLen < 10 {
			bias = 1
		}
		(*tokens)[len(*tokens)-1] |= byte(6 - bias)
		*tokens = append(*tokens, byte(matchLen-10+bias))
		return
	}
	(*tokens)[len(*tokens)-1] |= 7
	encodeLength(lengths, matchLen, 9)
}

func encodeLength(dst *[]byte, value, overflow int) {
	value -= overflow
	if value <= 223 {
		*dst = append(*dst, byte(value))
		return
	}
	value -= 224
	*dst = append(*dst, byte(224|(value&0x1f)))
	value >>= 5
	if value <= 223 {
		*dst = append(*dst, byte(value))
		return
	}
	value -= 224
	*dst = append(*dst, byte(224|(value&0x1f)), byte(value>>5))
}

func decodeLZBlockStandard(dst []byte, dpos *int, blockEnd int, literalStreams *[4][]byte, literalStreamCount int, tokens, distances, lengths []byte, literalFlags int, repOffsets *[7]int) error {
	if literalFlags&(streamLiteralsPosMask3|streamLiteralsDelta) == 0 {
		if literalStreamCount == 0 {
			return ErrCorrupt
		}
		return decodeLZBlockStandardRawLiterals(dst, dpos, blockEnd, literalStreams[0], tokens, distances, lengths, repOffsets)
	}

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	var literalPos [4]int
	distance := repOffsets[3]
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if outPos+litLen > blockEnd {
			return ErrCorrupt
		}
		if litLen > 0 {
			if err := copyLiterals(dst, outPos, litLen, literalStreams, literalStreamCount, &literalPos, literalFlags, distance); err != nil {
				return err
			}
		}
		outPos += litLen
		if outPos >= blockEnd {
			break
		}

		switch token & 0x18 {
		case 0x08:
			if distancePos >= len(distances) {
				return ErrCorrupt
			}
			distance = int(distances[distancePos])
			distancePos++
		case 0x10:
			if distancePos+2 > len(distances) {
				return ErrCorrupt
			}
			distance = int(binary.LittleEndian.Uint16(distances[distancePos:]))
			distancePos += 2
		case 0x18:
			if distancePos+3 > len(distances) {
				return ErrCorrupt
			}
			distance = int(uint32(distances[distancePos]) | uint32(distances[distancePos+1])<<8 | uint32(distances[distancePos+2])<<16)
			distancePos += 3
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > blockEnd {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	repOffsets[3] = distance
	*dpos = outPos
	return nil
}

func decodeLZBlockStandardRawLiterals(dst []byte, dpos *int, blockEnd int, rawLiterals, tokens, distances, lengths []byte, repOffsets *[7]int) error {
	if blockEnd > len(dst) {
		return ErrCorrupt
	}
	dst = dst[:blockEnd]

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	rawLiteralPos := 0
	distance := repOffsets[3]
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if outPos+litLen > len(dst) || rawLiteralPos+litLen > len(rawLiterals) {
			return ErrCorrupt
		}
		if litLen > 0 {
			switch litLen {
			case 1:
				dst[outPos] = rawLiterals[rawLiteralPos]
			case 2:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
			case 3:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
			case 4:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
			case 5:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
			case 6:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
				dst[outPos+5] = rawLiterals[rawLiteralPos+5]
			default:
				copy(dst[outPos:outPos+litLen], rawLiterals[rawLiteralPos:rawLiteralPos+litLen])
			}
			rawLiteralPos += litLen
		}
		outPos += litLen
		if outPos >= len(dst) {
			break
		}

		switch token & 0x18 {
		case 0x08:
			if distancePos >= len(distances) {
				return ErrCorrupt
			}
			distance = int(distances[distancePos])
			distancePos++
		case 0x10:
			if distancePos+2 > len(distances) {
				return ErrCorrupt
			}
			distance = int(binary.LittleEndian.Uint16(distances[distancePos:]))
			distancePos += 2
		case 0x18:
			if distancePos+3 > len(distances) {
				return ErrCorrupt
			}
			distance = int(uint32(distances[distancePos]) | uint32(distances[distancePos+1])<<8 | uint32(distances[distancePos+2])<<16)
			distancePos += 3
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > len(dst) {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	repOffsets[3] = distance
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvanced(dst []byte, dpos *int, blockEnd int, literalStreams *[4][]byte, literalStreamCount int, tokens []byte, advancedDistances []uint32, lengths []byte, literalFlags int, repOffsets *[7]int) error {
	if literalFlags&(streamLiteralsPosMask3|streamLiteralsDelta) == 0 {
		if literalStreamCount == 0 {
			return ErrCorrupt
		}
		return decodeLZBlockAdvancedRawLiterals(dst, dpos, blockEnd, literalStreams[0], tokens, advancedDistances, lengths, repOffsets)
	}
	if literalFlags == streamLiteralsDelta {
		if literalStreamCount == 0 {
			return ErrCorrupt
		}
		return decodeLZBlockAdvancedDeltaLiterals(dst, dpos, blockEnd, literalStreams[0], tokens, advancedDistances, lengths, repOffsets)
	}

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	var literalPos [4]int
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	distance := rep0
	outPos := *dpos

	for outPos < blockEnd {
		if tokenPos >= len(tokens) {
			return ErrCorrupt
		}
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if outPos+litLen > blockEnd {
			return ErrCorrupt
		}
		if litLen > 0 {
			if err := copyLiterals(dst, outPos, litLen, literalStreams, literalStreamCount, &literalPos, literalFlags, distance); err != nil {
				return err
			}
		}
		outPos += litLen
		if outPos >= blockEnd {
			break
		}

		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > blockEnd {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < blockEnd {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvancedDeltaLiterals(dst []byte, dpos *int, blockEnd int, deltaLiterals, tokens []byte, advancedDistances []uint32, lengths []byte, repOffsets *[7]int) error {
	if lengthStreamUsesSingleByteCodes(lengths) {
		return decodeLZBlockAdvancedDeltaLiteralsSingleByteLengths(dst, dpos, blockEnd, deltaLiterals, tokens, advancedDistances, lengths, repOffsets)
	}

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	deltaLiteralPos := 0
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	distance := rep0
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if litLen > 0 {
			outLitEnd := outPos + litLen
			deltaLiteralEnd := deltaLiteralPos + litLen
			if outLitEnd > blockEnd || deltaLiteralEnd > len(deltaLiterals) {
				return ErrCorrupt
			}
			if distance <= 0 || distance > outPos {
				return ErrCorrupt
			}
			refPos := outPos - distance
			switch litLen {
			case 1:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
			case 2:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
			case 3:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
				dst[outPos+2] = deltaLiterals[deltaLiteralPos+2] + dst[refPos+2]
			case 4:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
				dst[outPos+2] = deltaLiterals[deltaLiteralPos+2] + dst[refPos+2]
				dst[outPos+3] = deltaLiterals[deltaLiteralPos+3] + dst[refPos+3]
			default:
				dstRun := dst[outPos:outLitEnd]
				deltaRun := deltaLiterals[deltaLiteralPos:deltaLiteralEnd]
				refRun := dst[refPos : refPos+litLen]
				reconstructDeltaLiterals(dstRun, deltaRun, refRun)
			}
			deltaLiteralPos = deltaLiteralEnd
			outPos = outLitEnd
		}
		if outPos >= blockEnd {
			break
		}

		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > blockEnd {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < blockEnd {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvancedDeltaLiteralsSingleByteLengths(dst []byte, dpos *int, blockEnd int, deltaLiterals, tokens []byte, advancedDistances []uint32, lengths []byte, repOffsets *[7]int) error {
	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	deltaLiteralPos := 0
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	distance := rep0
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			litLen += int(lengths[lengthPos])
			lengthPos++
		}
		if litLen > 0 {
			outLitEnd := outPos + litLen
			deltaLiteralEnd := deltaLiteralPos + litLen
			if outLitEnd > blockEnd || deltaLiteralEnd > len(deltaLiterals) {
				return ErrCorrupt
			}
			if distance <= 0 || distance > outPos {
				return ErrCorrupt
			}
			refPos := outPos - distance
			switch litLen {
			case 1:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
			case 2:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
			case 3:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
				dst[outPos+2] = deltaLiterals[deltaLiteralPos+2] + dst[refPos+2]
			case 4:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
				dst[outPos+2] = deltaLiterals[deltaLiteralPos+2] + dst[refPos+2]
				dst[outPos+3] = deltaLiterals[deltaLiteralPos+3] + dst[refPos+3]
			default:
				dstRun := dst[outPos:outLitEnd]
				deltaRun := deltaLiterals[deltaLiteralPos:deltaLiteralEnd]
				refRun := dst[refPos : refPos+litLen]
				reconstructDeltaLiterals(dstRun, deltaRun, refRun)
			}
			deltaLiteralPos = deltaLiteralEnd
			outPos = outLitEnd
		}
		if outPos >= blockEnd {
			break
		}

		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			matchLen = int(lengths[lengthPos]) + minMatchLength + 7
			lengthPos++
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > blockEnd {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < blockEnd {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvancedRawLiterals(dst []byte, dpos *int, blockEnd int, rawLiterals, tokens []byte, advancedDistances []uint32, lengths []byte, repOffsets *[7]int) error {
	if lengthStreamUsesSingleByteCodes(lengths) {
		return decodeLZBlockAdvancedRawLiteralsSingleByteLengths(dst, dpos, blockEnd, rawLiterals, tokens, advancedDistances, lengths, repOffsets)
	}
	if blockEnd > len(dst) {
		return ErrCorrupt
	}
	dst = dst[:blockEnd]

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	rawLiteralPos := 0
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if outPos+litLen > len(dst) || rawLiteralPos+litLen > len(rawLiterals) {
			return ErrCorrupt
		}
		if litLen > 0 {
			switch litLen {
			case 1:
				dst[outPos] = rawLiterals[rawLiteralPos]
			case 2:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
			case 3:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
			case 4:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
			case 5:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
			case 6:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
				dst[outPos+5] = rawLiterals[rawLiteralPos+5]
			default:
				copy(dst[outPos:outPos+litLen], rawLiterals[rawLiteralPos:rawLiteralPos+litLen])
			}
			rawLiteralPos += litLen
		}
		outPos += litLen
		if outPos >= len(dst) {
			break
		}

		var distance int
		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > len(dst) {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < len(dst) {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvancedRawLiteralsSingleByteLengths(dst []byte, dpos *int, blockEnd int, rawLiterals, tokens []byte, advancedDistances []uint32, lengths []byte, repOffsets *[7]int) error {
	if blockEnd > len(dst) {
		return ErrCorrupt
	}
	dst = dst[:blockEnd]

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	rawLiteralPos := 0
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			litLen += int(lengths[lengthPos])
			lengthPos++
		}
		if outPos+litLen > len(dst) || rawLiteralPos+litLen > len(rawLiterals) {
			return ErrCorrupt
		}
		if litLen > 0 {
			switch litLen {
			case 1:
				dst[outPos] = rawLiterals[rawLiteralPos]
			case 2:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
			case 3:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
			case 4:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
			case 5:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
			case 6:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
				dst[outPos+5] = rawLiterals[rawLiteralPos+5]
			default:
				copy(dst[outPos:outPos+litLen], rawLiterals[rawLiteralPos:rawLiteralPos+litLen])
			}
			rawLiteralPos += litLen
		}
		outPos += litLen
		if outPos >= len(dst) {
			break
		}

		var distance int
		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			matchLen = int(lengths[lengthPos]) + minMatchLength + 7
			lengthPos++
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > len(dst) {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < len(dst) {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func lengthStreamUsesSingleByteCodes(lengths []byte) bool {
	const highBitMask = 0x8080808080808080
	i := 0
	for ; i+16 <= len(lengths); i += 16 {
		// Bytes without the high bit set cannot be extended-length markers.
		left := binary.LittleEndian.Uint64(lengths[i:])
		right := binary.LittleEndian.Uint64(lengths[i+8:])
		if (left|right)&highBitMask != 0 {
			for j := 0; j < 16; j++ {
				if lengths[i+j] > 223 {
					return false
				}
			}
		}
	}
	for ; i+8 <= len(lengths); i += 8 {
		// Bytes without the high bit set cannot be extended-length markers.
		if binary.LittleEndian.Uint64(lengths[i:])&highBitMask != 0 {
			if lengths[i] > 223 || lengths[i+1] > 223 || lengths[i+2] > 223 || lengths[i+3] > 223 ||
				lengths[i+4] > 223 || lengths[i+5] > 223 || lengths[i+6] > 223 || lengths[i+7] > 223 {
				return false
			}
		}
	}
	for ; i < len(lengths); i++ {
		value := lengths[i]
		if value > 223 {
			return false
		}
	}
	return true
}

func reconstructDeltaLiterals(dstRun, deltaRun, refRun []byte) {
	n := len(deltaRun)
	if n == 0 {
		return
	}
	_ = dstRun[n-1]
	_ = refRun[n-1]
	i := 0
	for ; i+8 <= n; i += 8 {
		dstRun[i] = deltaRun[i] + refRun[i]
		dstRun[i+1] = deltaRun[i+1] + refRun[i+1]
		dstRun[i+2] = deltaRun[i+2] + refRun[i+2]
		dstRun[i+3] = deltaRun[i+3] + refRun[i+3]
		dstRun[i+4] = deltaRun[i+4] + refRun[i+4]
		dstRun[i+5] = deltaRun[i+5] + refRun[i+5]
		dstRun[i+6] = deltaRun[i+6] + refRun[i+6]
		dstRun[i+7] = deltaRun[i+7] + refRun[i+7]
	}
	for ; i < n; i++ {
		dstRun[i] = deltaRun[i] + refRun[i]
	}
}

func copyLiterals(dst []byte, dpos int, length int, streams *[4][]byte, streamCount int, positions *[4]int, flags int, distance int) error {
	if flags&streamLiteralsPosMask3 == 0 {
		if streamCount == 0 || positions[0]+length > len(streams[0]) {
			return ErrCorrupt
		}
		if flags&streamLiteralsDelta == 0 {
			copy(dst[dpos:dpos+length], streams[0][positions[0]:positions[0]+length])
		} else {
			if distance <= 0 || distance > dpos {
				return ErrCorrupt
			}
			dstRun := dst[dpos : dpos+length]
			deltaRun := streams[0][positions[0] : positions[0]+length]
			refRun := dst[dpos-distance : dpos-distance+length]
			reconstructDeltaLiterals(dstRun, deltaRun, refRun)
		}
		positions[0] += length
		return nil
	}

	if streamCount < 4 {
		return ErrCorrupt
	}
	for i := 0; i < length; i++ {
		stream := (dpos + i) & 3
		if positions[stream] >= len(streams[stream]) {
			return ErrCorrupt
		}
		value := streams[stream][positions[stream]]
		positions[stream]++
		if flags&streamLiteralsDelta != 0 {
			if distance <= 0 || distance > dpos+i {
				return ErrCorrupt
			}
			value += dst[dpos+i-distance]
		}
		dst[dpos+i] = value
	}
	return nil
}

func copyMatch(dst []byte, pos, distance, length int) {
	if distance >= length {
		copy(dst[pos:pos+length], dst[pos-distance:pos-distance+length])
		return
	}
	copied := copy(dst[pos:pos+length], dst[pos-distance:pos])
	for copied < length {
		copied += copy(dst[pos+copied:pos+length], dst[pos:pos+copied])
	}
}

func decodeEntropy(src []byte, cpos *int) ([]byte, int, error) {
	return decodeEntropyWithState(src, cpos, nil)
}

func decodeEntropyWithState(src []byte, cpos *int, state *decodeState) ([]byte, int, error) {
	symbolCount, mode, flags, err := readHeader(src, cpos)
	if err != nil {
		return nil, 0, err
	}
	switch mode {
	case entropyRaw:
		if *cpos+symbolCount > len(src) {
			return nil, 0, ErrCorrupt
		}
		stream := src[*cpos : *cpos+symbolCount]
		*cpos += symbolCount
		return stream, flags, nil
	case entropyRLE:
		if *cpos >= len(src) {
			return nil, 0, ErrCorrupt
		}
		stream := state.alloc(symbolCount)
		if len(stream) > 0 {
			stream[0] = src[*cpos]
			for copied := 1; copied < len(stream); copied *= 2 {
				copy(stream[copied:], stream[:copied])
			}
		}
		*cpos += 1
		return stream, flags, nil
	case entropyHuffman:
		stream, err := decodeHuffmanEntropyWithState(src, cpos, symbolCount, state)
		if err != nil {
			return nil, 0, err
		}
		return stream, flags, nil
	default:
		return nil, 0, ErrCorrupt
	}
}

func writeEntropyRaw(dst []byte, flags int, data []byte) []byte {
	dst = writeHeader(dst, len(data), entropyRaw, flags)
	return append(dst, data...)
}

func writeHeader(dst []byte, blockSize, typ, flags int) []byte {
	data := uint32(typ) | uint32(flags<<2) | uint32(blockSize<<6)
	return append(dst, byte(data), byte(data>>8), byte(data>>16))
}

func readHeader(src []byte, pos *int) (blockSize int, typ int, flags int, err error) {
	if *pos+3 > len(src) {
		return 0, 0, 0, ErrCorrupt
	}
	data := uint32(src[*pos]) | uint32(src[*pos+1])<<8 | uint32(src[*pos+2])<<16
	*pos += 3
	typ = int(data & 3)
	flags = int((data >> 2) & 0xf)
	blockSize = int(data >> 6)
	return blockSize, typ, flags, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
