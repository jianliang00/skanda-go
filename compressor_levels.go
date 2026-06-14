package skanda

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

type blockEncoding struct {
	data         []byte
	lastDistance int
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
