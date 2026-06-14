package skanda

import "math/bits"

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
