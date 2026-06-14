package skanda

import (
	"encoding/binary"
	"math/bits"
)

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

type levelLazyBlockFinder interface {
	find(src []byte, pos, blockEnd, repLength int) levelMatch
	findLazy1(src []byte, pos, blockEnd, currentLength int) levelMatch
	findLazy2(src []byte, pos, blockEnd, currentLength int) levelMatch
	addLongRepeatPositions(src []byte, pos, blockEnd int)
	addShortRepeatPositions(src []byte, pos, blockEnd int)
	addAdvancedRepeatPositions(src []byte, pos, blockEnd int)
	addAfterMatch(src []byte, searchPos, matchPos, matchLength, blockEnd int)
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
