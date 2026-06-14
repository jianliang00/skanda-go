package skanda

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
