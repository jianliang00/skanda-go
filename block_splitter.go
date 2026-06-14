package skanda

import (
	"encoding/binary"
	"math/bits"
)

const blockSplitterLiteralHistSize = 256 * 10

type blockSplitHistogram struct {
	literal  [blockSplitterLiteralHistSize]uint32
	token    [256]uint32
	distance [256]uint32
	length   [256]uint32
}

type blockSplitterDict struct {
	table         []int
	hashShift     uint
	hashMask      uint64
	hashLeftShift uint
}

type blockSplitter struct {
	maxSubdivisions   int
	subdivisionSize   int
	reuseSubdivisions int
	histograms        []blockSplitHistogram
	dict3             blockSplitterDict
	dict6             blockSplitterDict
	costModel         optimalCostModel
}

func newOptimalBlockSplitter(opts compressorLevelOptions) *blockSplitter {
	opts = normalizeCompressorLevelOptions(opts)
	if !useOptimalBlockSplitter(opts) {
		return nil
	}

	subdivisions := 2
	if opts.parser >= compressorParserOptimal2 {
		subdivisions = 64
	}
	dictLog := min(opts.windowLog-4, opts.hashLog)
	if dictLog > 20 {
		dictLog = 20
	}
	if dictLog < 1 {
		dictLog = 1
	}

	return &blockSplitter{
		maxSubdivisions: subdivisions,
		subdivisionSize: (maxBlockSize + 1) / subdivisions,
		histograms:      make([]blockSplitHistogram, subdivisions),
		dict3:           newBlockSplitterDict(dictLog, 3),
		dict6:           newBlockSplitterDict(dictLog, 6),
	}
}

func blockSplitterMemory(opts compressorLevelOptions) int {
	if !useOptimalBlockSplitter(opts) {
		return 0
	}
	subdivisions := 2
	if opts.parser >= compressorParserOptimal2 {
		subdivisions = 64
	}
	dictLog := min(opts.windowLog-4, opts.hashLog)
	if dictLog > 20 {
		dictLog = 20
	}
	if dictLog < 1 {
		dictLog = 1
	}
	intSize := bits.UintSize / 8
	const uint32Size = 4
	histogramSize := subdivisions * (blockSplitterLiteralHistSize + 256*3) * uint32Size
	dictionarySize := 2 * (1 << dictLog) * intSize
	return histogramSize + dictionarySize
}

func useOptimalBlockSplitter(opts compressorLevelOptions) bool {
	return opts.decSpeedBias < 0.99 &&
		(opts.parser == compressorParserOptimal1 || opts.parser == compressorParserOptimal2 || opts.parser == compressorParserOptimal3)
}

func newBlockSplitterDict(hashLog, hashBytes int) blockSplitterDict {
	table := acquireIntTable(1 << hashLog)
	hashMask, hashLeftShift := hashWindow(hashBytes)
	return blockSplitterDict{table: table, hashShift: uint(64 - hashLog), hashMask: hashMask, hashLeftShift: hashLeftShift}
}

func (s *blockSplitter) release() {
	if s == nil {
		return
	}
	s.dict3.release()
	s.dict6.release()
	s.histograms = nil
}

func (s *blockSplitter) reset() {
	if s == nil {
		return
	}
	s.reuseSubdivisions = 0
	s.costModel = optimalCostModel{}
	s.dict3.reset()
	s.dict6.reset()
}

func (d *blockSplitterDict) release() {
	if d == nil {
		return
	}
	releaseIntTable(d.table)
	d.table = nil
}

func (d *blockSplitterDict) reset() {
	if d != nil {
		clear(d.table)
	}
}

func (d *blockSplitterDict) canHash(src []byte, pos int) bool {
	return len(d.table) > 0 && pos >= 0 && pos+8 <= len(src)
}

func (d *blockSplitterDict) hash(src []byte, pos int) int {
	value := (binary.LittleEndian.Uint64(src[pos:]) & d.hashMask) << d.hashLeftShift
	return hashUintShift(value, d.hashShift)
}

func (d *blockSplitterDict) add(src []byte, pos int) {
	if d.canHash(src, pos) {
		d.table[d.hash(src, pos)] = pos
	}
}

func (d *blockSplitterDict) candidateAndUpdate(src []byte, pos int) (int, bool) {
	if !d.canHash(src, pos) {
		return 0, false
	}
	index := d.hash(src, pos)
	prev := d.table[index]
	d.table[index] = pos
	return prev, prev >= 0 && prev < pos
}

func (s *blockSplitter) getBlockSize(src []byte, inputStart, blockStart, maxSize int, opts compressorLevelOptions) int {
	if s == nil || maxSize <= 0 {
		return maxSize
	}
	if maxSize > maxBlockSize {
		maxSize = maxBlockSize
	}
	subdivisionCount := maxSize / s.subdivisionSize
	if maxSize%s.subdivisionSize != 0 {
		subdivisionCount++
	}
	if subdivisionCount < 1 {
		subdivisionCount = 1
	}
	if subdivisionCount > s.maxSubdivisions {
		subdivisionCount = s.maxSubdivisions
	}
	if s.reuseSubdivisions > subdivisionCount {
		s.reuseSubdivisions = 0
	}

	for i := s.reuseSubdivisions; i < subdivisionCount; i++ {
		testStart := blockStart + i*s.subdivisionSize
		testSize := min(s.subdivisionSize, maxSize-i*s.subdivisionSize)
		s.approximateSymbolHistogram(src, inputStart, testStart, testSize, opts, &s.histograms[i])
	}

	left := s.histograms[0]
	var right blockSplitHistogram
	for i := 1; i < subdivisionCount; i++ {
		left.add(&s.histograms[i])
	}

	bestEncodedSize, bestModel := estimateBlockSplitModel(&left, opts)
	bestBlockSize := maxSize

	for i := subdivisionCount - 1; i > 0; i-- {
		left.subtract(&s.histograms[i])
		right.add(&s.histograms[i])
		leftSize, leftModel := estimateBlockSplitModel(&left, opts)
		rightSize, _ := estimateBlockSplitModel(&right, opts)
		if leftSize+rightSize < bestEncodedSize {
			bestEncodedSize = leftSize
			bestModel = leftModel
			bestBlockSize = i * s.subdivisionSize
			right.reset()
		}
	}

	if bestBlockSize != maxSize {
		usedSubdivisions := bestBlockSize / s.subdivisionSize
		reuse := subdivisionCount - usedSubdivisions
		copy(s.histograms[:reuse], s.histograms[usedSubdivisions:subdivisionCount])
		s.reuseSubdivisions = reuse
	} else {
		s.reuseSubdivisions = 0
	}
	if bestBlockSize <= 0 || bestBlockSize > maxSize {
		s.costModel = bestModel
		return maxSize
	}
	s.costModel = bestModel
	return bestBlockSize
}

func (s *blockSplitter) initialCostModel() *optimalCostModel {
	if s == nil || !s.costModel.enabled {
		return nil
	}
	return &s.costModel
}

func (s *blockSplitter) approximateSymbolHistogram(src []byte, inputStart, blockStart, size int, opts compressorLevelOptions, hist *blockSplitHistogram) {
	hist.reset()
	if size <= 0 {
		return
	}
	blockEnd := min(blockStart+size, len(src))
	if blockStart >= blockEnd {
		return
	}

	const accelerationThreshold = 6
	repOffsets := [3]int{1, 1, 1}
	acceleration := 1 << accelerationThreshold
	literalRunStart := blockStart
	pos := blockStart + 1
	advancedDistance := opts.decSpeedBias < 0.1
	advancedLiteral := opts.decSpeedBias < 0.6
	maxDistance := maxStdDistance

	for pos < blockEnd {
		if rep := findRepeatDistanceMatch(src, pos+1, blockEnd, repOffsets[0], 2, maxDistance); rep.length > 0 {
			s.dict3.add(src, pos)
			s.dict3.add(src, pos+1)
			s.dict3.add(src, pos+2)
			s.dict6.add(src, pos+1)
			pos++
			control := hist.encodeLiteralRun(src, literalRunStart, pos, repOffsets[0], advancedLiteral)
			matchLength := rep.length
			pos += matchLength
			literalRunStart = pos
			hist.encodeMatch(control, matchLength, repOffsets[0], &repOffsets, advancedDistance)
			acceleration = 1 << accelerationThreshold
			continue
		}

		if advancedDistance {
			matchedRepeat := false
			for rep := 1; rep < 3; rep++ {
				repeat := findRepeatDistanceMatch(src, pos, blockEnd, repOffsets[rep], 2, maxDistance)
				if repeat.length == 0 {
					continue
				}
				s.dict3.add(src, pos)
				s.dict3.add(src, pos+1)
				s.dict6.add(src, pos)
				control := hist.encodeLiteralRun(src, literalRunStart, pos, repOffsets[0], advancedLiteral)
				matchLength := repeat.length
				pos += matchLength
				literalRunStart = pos
				hist.encodeMatch(control, matchLength, repOffsets[rep], &repOffsets, true)
				acceleration = 1 << accelerationThreshold
				matchedRepeat = true
				break
			}
			if matchedRepeat {
				continue
			}
		}

		prev3, ok3 := s.dict3.candidateAndUpdate(src, pos)
		matchLength := 0
		distance := 0
		if ok3 {
			matchLength = matchLengthAtWindow(src, pos, prev3, blockEnd, 3, maxDistance)
			distance = pos - prev3
		}
		if matchLength > 0 && (matchLength >= 4 || distance < 1<<16) {
			if prev6, ok6 := s.dict6.candidateAndUpdate(src, pos); ok6 && matchEndEqual(src, pos, prev6, matchLength) {
				if length := matchLengthAtWindow(src, pos, prev6, blockEnd, 6, maxDistance); length > matchLength {
					matchLength = length
					distance = pos - prev6
				}
			}
		} else {
			step := acceleration >> accelerationThreshold
			if step < 1 {
				step = 1
			}
			pos += step
			acceleration++
			continue
		}

		lazySteps := 0
		matchPos := pos + 1
		prev6, ok6 := s.dict6.candidateAndUpdate(src, matchPos)
		prev3, ok3 = s.dict3.candidateAndUpdate(src, matchPos)
		matched6 := ok6 && matchEndEqual(src, matchPos, prev6, matchLength)
		matched3 := ok3 && matchEndEqual(src, matchPos, prev3, matchLength)
		if matched6 || matched3 {
			prev := prev3
			if matched6 {
				prev = prev6
			}
			if length := matchLengthAtWindow(src, matchPos, prev, blockEnd, 3, maxDistance); length > matchLength {
				distance = matchPos - prev
				matchLength = length
				lazySteps = 1
			}
		}

		pos += lazySteps
		s.dict3.add(src, pos+matchLength-1)
		s.dict6.add(src, pos+matchLength-2)
		s.dict3.add(src, pos+matchLength-3)

		match := pos - distance
		for pos > literalRunStart && match > inputStart && src[pos-1] == src[match-1] {
			matchLength++
			pos--
			match--
		}

		control := hist.encodeLiteralRun(src, literalRunStart, pos, repOffsets[0], advancedLiteral)
		pos += matchLength
		literalRunStart = pos
		hist.encodeMatch(control, matchLength, distance, &repOffsets, advancedDistance)
		if matchLength > 3 {
			acceleration = 1 << accelerationThreshold
		}
	}

	if literalRunStart < blockEnd {
		control := hist.encodeLiteralRun(src, literalRunStart, blockEnd, repOffsets[0], advancedLiteral)
		hist.token[control]++
	}
}

func (h *blockSplitHistogram) reset() {
	for i := range h.literal {
		h.literal[i] = 0
	}
	for i := 0; i < 256; i++ {
		h.token[i] = 0
		h.distance[i] = 0
		h.length[i] = 0
	}
}

func (h *blockSplitHistogram) add(other *blockSplitHistogram) {
	for i := range h.literal {
		h.literal[i] += other.literal[i]
	}
	for i := 0; i < 256; i++ {
		h.token[i] += other.token[i]
		h.distance[i] += other.distance[i]
		h.length[i] += other.length[i]
	}
}

func (h *blockSplitHistogram) subtract(other *blockSplitHistogram) {
	for i := range h.literal {
		h.literal[i] -= other.literal[i]
	}
	for i := 0; i < 256; i++ {
		h.token[i] -= other.token[i]
		h.distance[i] -= other.distance[i]
		h.length[i] -= other.length[i]
	}
}

func (h *blockSplitHistogram) encodeLiteralRun(src []byte, start, end, distance int, advancedLiteral bool) byte {
	if start < 0 {
		start = 0
	}
	if end > len(src) {
		end = len(src)
	}
	if end < start {
		end = start
	}
	runLength := end - start
	var control byte
	if runLength >= 7 {
		control = 7 << 5
		h.encodeLength(runLength, 7)
	} else {
		control = byte(runLength << 5)
	}
	for pos := start; pos < end; pos++ {
		value := src[pos]
		h.literal[value]++
		if !advancedLiteral {
			continue
		}
		stream := pos & 3
		h.literal[512+stream*256+int(value)]++
		var prediction byte
		if distance > 0 {
			if ref := pos - distance; ref >= 0 && ref < len(src) {
				prediction = src[ref]
			}
		}
		delta := value - prediction
		h.literal[256+int(delta)]++
		h.literal[1536+stream*256+int(delta)]++
	}
	return control
}

func (h *blockSplitHistogram) encodeMatch(control byte, matchLength int, distance int, repOffsets *[3]int, advancedDistance bool) {
	if advancedDistance {
		switch distance {
		case repOffsets[0]:
		case repOffsets[1]:
			control |= 1 << 3
			repOffsets[0], repOffsets[1] = repOffsets[1], repOffsets[0]
		case repOffsets[2]:
			control |= 2 << 3
			repOffsets[2] = repOffsets[1]
			repOffsets[1] = repOffsets[0]
			repOffsets[0] = distance
		default:
			control |= 3 << 3
			repOffsets[2] = repOffsets[1]
			repOffsets[1] = repOffsets[0]
			repOffsets[0] = distance
			virtualDistance := distance + 7
			extraBits := bits.Len(uint(virtualDistance>>3)) - 1
			distanceToken := ((virtualDistance & 7) ^ 7) | (extraBits << 3)
			h.distance[distanceToken&0xff]++
		}
	} else if distance != repOffsets[0] {
		bytes := ((bits.Len(uint(distance)) - 1) / 8) + 1
		if bytes > 3 {
			bytes = 3
		}
		control |= byte(bytes << 3)
		repOffsets[0] = distance
		for i := 0; i < bytes; i++ {
			h.distance[(distance>>(i*8))&0xff]++
		}
	}

	if matchLength <= 8 {
		h.token[int(control)|(matchLength-minMatchLength)]++
		return
	}
	h.token[int(control)|7]++
	h.encodeLength(matchLength, 9)
}

func (h *blockSplitHistogram) encodeLength(value, overflow int) {
	value -= overflow
	if value <= 223 {
		h.length[value]++
		return
	}
	value -= 224
	h.length[224|(value&0x1f)]++
	value >>= 5
	if value <= 223 {
		h.length[value]++
		return
	}
	value -= 224
	h.length[224|(value&0x1f)]++
	h.length[(value>>5)&0xff]++
}

func estimateBlockSplitModel(hist *blockSplitHistogram, opts compressorLevelOptions) (int, optimalCostModel) {
	model := newRawOptimalCostModel()
	rawEstimate := estimateEntropyFromHistogram(hist.literal[0:256], opts.decSpeedBias)
	bestLiteralsSize := rawEstimate.size
	model.literal[0] = rawEstimate.costs
	model.literalMode = 0

	if opts.decSpeedBias < 0.6 {
		symbolCount := histogramSymbolCount(hist.literal[0:256])
		deltaBias := 0.0
		if opts.parser >= compressorParserOptimal3 {
			deltaBias = (opts.decSpeedBias - 0.6) / 8
		}
		deltaEstimate := estimateEntropyFromHistogram(hist.literal[256:512], opts.decSpeedBias)
		deltaSize := deltaEstimate.size + int(float64(symbolCount)*(opts.decSpeedBias/4+0.05/8+deltaBias))
		if histogramSymbolCount(hist.literal[256:512]) == symbolCount && deltaSize < bestLiteralsSize {
			bestLiteralsSize = deltaSize
			model.literalMode = streamLiteralsDelta
			model.literal[0] = deltaEstimate.costs
		}

		posSize := 0
		var posCosts [4][256]int
		posCount := 0
		posBase := 512
		posPenalty := opts.decSpeedBias/4 + 0.05/8
		if model.literalMode&streamLiteralsDelta != 0 {
			posBase = 1536
			posPenalty = opts.decSpeedBias/2 + 0.05/8 + deltaBias
		}
		for stream := 0; stream < 4; stream++ {
			start := posBase + stream*256
			streamCount := histogramSymbolCount(hist.literal[start : start+256])
			posCount += streamCount
			estimate := estimateEntropyFromHistogram(hist.literal[start:start+256], opts.decSpeedBias)
			posCosts[stream] = estimate.costs
			posSize += estimate.size
			posSize += int(float64(streamCount) * posPenalty)
		}
		if posCount == symbolCount && posSize < bestLiteralsSize {
			bestLiteralsSize = posSize
			model.literalMode |= streamLiteralsPosMask3
			model.literal = posCosts
		}
	}

	size := bestLiteralsSize
	tokenEstimate := estimateEntropyFromHistogram(hist.token[:], opts.decSpeedBias)
	distanceEstimate := estimateEntropyFromHistogram(hist.distance[:], opts.decSpeedBias)
	lengthEstimate := estimateEntropyFromHistogram(hist.length[:], opts.decSpeedBias)
	model.token = tokenEstimate.costs
	model.distance = distanceEstimate.costs
	model.length = lengthEstimate.costs
	model.enabled = histogramSymbolCount(hist.literal[0:256]) >= 32 ||
		histogramSymbolCount(hist.token[:]) >= 32 ||
		histogramSymbolCount(hist.distance[:]) >= 32 ||
		histogramSymbolCount(hist.length[:]) >= 32
	size += tokenEstimate.size
	size += distanceEstimate.size
	size += lengthEstimate.size
	if size < 0 {
		return 0, model
	}
	return size, model
}

func histogramSymbolCount(hist []uint32) int {
	count := 0
	for _, n := range hist {
		count += int(n)
	}
	return count
}
