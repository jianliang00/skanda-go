package skanda

import "math/bits"

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
