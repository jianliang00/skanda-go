package skanda

import (
	"encoding/binary"
	"sort"
)

type huffmanSymbol struct {
	code uint16
	bits uint8
}

type precodeStreamSymbol struct {
	codeLength uint8
	runBit     uint8
}

type huffmanHeaderData struct {
	precodeSymbols [maxHuffmanCodeLength + 2]huffmanSymbol
	precodeHist    [maxHuffmanCodeLength + 2]uint32
	precodeStream  [256]precodeStreamSymbol
	precodeCount   int
	extraRawBits   int
}

type encodedHuffmanStream struct {
	data   []byte
	buffer []byte
}

const enableHuffmanEncoding = true

func (data *huffmanHeaderData) appendPrecode(symbol precodeStreamSymbol) {
	data.precodeStream[data.precodeCount] = symbol
	data.precodeCount++
}

type huffmanEstimate struct {
	size         int
	costs        [256]int
	uncompressed bool
}

func encodeEntropy(data []byte, flags int, decSpeedBias float64) []byte {
	return encodeEntropyWithCodegen(data, flags, decSpeedBias, true)
}

func encodeEntropyWithCodegen(data []byte, flags int, decSpeedBias float64, useFastCodegen bool) []byte {
	return appendEntropyWithCodegen(nil, data, flags, decSpeedBias, useFastCodegen)
}

func appendEntropy(dst []byte, data []byte, flags int, decSpeedBias float64) []byte {
	return appendEntropyWithCodegen(dst, data, flags, decSpeedBias, true)
}

func appendEntropyWithCodegen(dst []byte, data []byte, flags int, decSpeedBias float64, useFastCodegen bool) []byte {
	if len(data) < 32 || decSpeedBias >= 0.99 || !enableHuffmanEncoding {
		if entropyAllSame(data) {
			dst = writeHeader(dst, len(data), entropyRLE, flags)
			return append(dst, data[0])
		}
		return writeEntropyRaw(dst, flags, data)
	}

	var hist [256]uint32
	if huffmanHistogramAndAllSame(data, &hist) {
		dst = writeHeader(dst, len(data), entropyRLE, flags)
		return append(dst, data[0])
	}
	out, ok := appendEntropyHuffmanWithHistogram(dst, data, flags, useFastCodegen, &hist)
	if !ok || len(out)-len(dst) >= len(data)+3 {
		return writeEntropyRaw(dst, flags, data)
	}
	return out
}

func appendEntropyWithStripedHistogram(dst []byte, data []byte, flags int, decSpeedBias float64, useFastCodegen bool) []byte {
	if len(data) < 1024 {
		return appendEntropyWithCodegen(dst, data, flags, decSpeedBias, useFastCodegen)
	}
	var hist [256]uint32
	huffmanStripedHistogram(data, &hist)
	if hist[data[0]] == uint32(len(data)) {
		dst = writeHeader(dst, len(data), entropyRLE, flags)
		return append(dst, data[0])
	}
	if len(data) < 32 || decSpeedBias >= 0.99 || !enableHuffmanEncoding {
		return writeEntropyRaw(dst, flags, data)
	}

	out, ok := appendEntropyHuffmanWithHistogram(dst, data, flags, useFastCodegen, &hist)
	if !ok || len(out)-len(dst) >= len(data)+3 {
		return writeEntropyRaw(dst, flags, data)
	}
	return out
}

func entropyAllSame(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	first := data[0]
	for _, value := range data[1:] {
		if value != first {
			return false
		}
	}
	return true
}

func huffmanHistogramAndAllSame(data []byte, hist *[256]uint32) bool {
	if len(data) == 0 {
		return false
	}
	first := data[0]
	allSame := true
	for _, value := range data {
		hist[value]++
		if value != first {
			allSame = false
		}
	}
	return allSame
}

func huffmanStripedHistogram(data []byte, hist *[256]uint32) {
	if len(data) < 1024 {
		for _, value := range data {
			hist[value]++
		}
		return
	}
	var hist0, hist1, hist2, hist3 [256]uint32
	pos := 0
	limit := len(data) &^ 3
	for pos < limit {
		hist0[data[pos]]++
		hist1[data[pos+1]]++
		hist2[data[pos+2]]++
		hist3[data[pos+3]]++
		pos += 4
	}
	for i := range hist {
		hist[i] = hist0[i] + hist1[i] + hist2[i] + hist3[i]
	}
	for _, value := range data[pos:] {
		hist[value]++
	}
}

func estimateEntropyFromHistogram(hist []uint32, decSpeedBias float64) huffmanEstimate {
	estimate := huffmanEstimate{uncompressed: true}
	for i := range estimate.costs {
		estimate.costs[i] = 8
	}

	symbolCount := 0
	for i := 0; i < 256 && i < len(hist); i++ {
		symbolCount += int(hist[i])
	}

	for i := 0; i < 256 && i < len(hist); i++ {
		if int(hist[i]) == symbolCount {
			for j := range estimate.costs {
				estimate.costs[j] = maxHuffmanCodeLength + 1
			}
			estimate.size = symbolCount + 3
			estimate.uncompressed = false
			estimate.costs[i] = 0
			return estimate
		}
		if hist[i] != 0 {
			break
		}
	}

	if symbolCount < 32 || decSpeedBias >= 0.99 || !enableHuffmanEncoding {
		estimate.size = symbolCount + 3
		return estimate
	}

	var symbols [256]huffmanSymbol
	fastHuffmanCodegen(hist[:256], symbols[:], symbolCount, 256, maxHuffmanCodeLength)
	compressedBits := 0
	for i, symbol := range symbols {
		if symbol.bits == 0 || symbol.bits > maxHuffmanCodeLength {
			estimate.costs[i] = maxHuffmanCodeLength + 1
			continue
		}
		estimate.costs[i] = int(symbol.bits)
		compressedBits += int(symbol.bits) * int(hist[i])
	}
	headerData := generateHuffmanHeaderData(&symbols, true)

	headerBits := 48 + (intLog2(symbolCount)-4)*5
	for i := 1; i <= maxHuffmanCodeLength+1; i++ {
		headerBits += int(headerData.precodeSymbols[i].bits) * int(headerData.precodeHist[i])
	}
	headerBits += headerData.extraRawBits
	estimate.size = compressedBits/8 + headerBits/8 + 8
	if float64(estimate.size) >= float64(symbolCount)*(8-4*decSpeedBias)/8 {
		estimate.size = symbolCount + 3
		estimate.uncompressed = true
		for i := range estimate.costs {
			estimate.costs[i] = 8
		}
		return estimate
	}
	estimate.uncompressed = false
	return estimate
}

func encodeEntropyHuffman(data []byte, flags int) ([]byte, bool) {
	return encodeEntropyHuffmanWithCodegen(data, flags, true)
}

func encodeEntropyHuffmanWithCodegen(data []byte, flags int, useFastCodegen bool) ([]byte, bool) {
	return appendEntropyHuffmanWithCodegen(nil, data, flags, useFastCodegen)
}

func appendEntropyHuffmanWithCodegen(dst []byte, data []byte, flags int, useFastCodegen bool) ([]byte, bool) {
	var hist [256]uint32
	huffmanHistogramAndAllSame(data, &hist)
	return appendEntropyHuffmanWithHistogram(dst, data, flags, useFastCodegen, &hist)
}

func appendEntropyHuffmanWithHistogram(dst []byte, data []byte, flags int, useFastCodegen bool, hist *[256]uint32) ([]byte, bool) {
	var symbols [256]huffmanSymbol
	if useFastCodegen {
		fastHuffmanCodegen(hist[:], symbols[:], len(data), 256, maxHuffmanCodeLength)
	} else {
		packageMergeHuffmanCodegen(hist[:], symbols[:], 256, maxHuffmanCodeLength)
	}
	createHuffmanCodes(symbols[:], 256, maxHuffmanCodeLength, huffmanCodeSpace)

	streamSizes, streams := encodeHuffmanStreams(data, &symbols)
	headerData := generateHuffmanHeaderData(&symbols, useFastCodegen)
	createHuffmanCodes(headerData.precodeSymbols[1:], maxHuffmanCodeLength+1, maxPrecodeCodeLength, precodeCodeSpace)
	var headerBuf [128]byte
	headerStart := encodeHuffmanHeaderInto(streamSizes, &headerData, &headerBuf)
	headerLen := len(headerBuf) - headerStart
	if headerLen < 4 || headerLen > 127 {
		releaseEncodedHuffmanStreams(streams)
		return nil, false
	}

	totalStreamSize := 0
	for _, stream := range streams {
		totalStreamSize += len(stream.data)
	}
	out := dst
	if cap(out)-len(out) < 3+1+headerLen+totalStreamSize {
		next := make([]byte, len(out), len(out)+3+1+headerLen+totalStreamSize)
		copy(next, out)
		out = next
	}
	out = writeHeader(out, len(data), entropyHuffman, flags)
	out = append(out, byte(headerLen))
	out = append(out, headerBuf[headerStart:]...)
	for _, stream := range streams {
		out = append(out, stream.data...)
	}
	releaseEncodedHuffmanStreams(streams)
	return out, true
}

type packageMergeNode struct {
	histogramSum uint64
	symbolCounts [256]uint8
}

type packageMergeWorkspace struct {
	original     [256]packageMergeNode
	mergedNodes  [2][256]packageMergeNode
	originalList [256]*packageMergeNode
	merged       [512]*packageMergeNode
}

var pooledPackageMergeWorkspaces = make(chan *packageMergeWorkspace, 4)

var fixedLog2FractionTable = [64]uint8{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 11, 22, 33, 43, 53, 63, 73, 82, 91, 100, 109, 117, 125, 134, 141,
	149, 157, 164, 172, 179, 186, 193, 200, 206, 213, 219, 225, 232, 238, 244, 250,
}

func acquirePackageMergeWorkspace() *packageMergeWorkspace {
	select {
	case workspace := <-pooledPackageMergeWorkspaces:
		return workspace
	default:
		return &packageMergeWorkspace{}
	}
}

func releasePackageMergeWorkspace(workspace *packageMergeWorkspace) {
	select {
	case pooledPackageMergeWorkspaces <- workspace:
	default:
	}
}

func fastHuffmanCodegen(hist []uint32, symbols []huffmanSymbol, symbolCount int, uniqueSymbols int, maxCodeLength int) {
	codeSpace := 1 << maxCodeLength
	var sortedErrors [16][256]uint8
	var errorCounts [16]int
	symbolCountLog := fixedLog2(symbolCount)
	usedCodeSpace := 0

	for i := 0; i < uniqueSymbols; i++ {
		if hist[i] == 0 {
			symbols[i].bits = uint8(maxCodeLength + 1)
			continue
		}
		optimal := symbolCountLog - fixedLog2(int(hist[i]))
		codeBits := (optimal + 128) >> 8
		err := optimal - (codeBits << 8)
		if codeBits <= 0 {
			codeBits = 1
		}
		if codeBits > maxCodeLength {
			codeBits = maxCodeLength
		}
		symbols[i].bits = uint8(codeBits)
		usedCodeSpace += codeSpace >> codeBits
		bucket := (err + 128) >> 4
		if bucket >= 16 {
			bucket = 15
		}
		sortedErrors[bucket][errorCounts[bucket]] = uint8(i)
		errorCounts[bucket]++
	}

	bucket := 15
	index := 0
	for usedCodeSpace > codeSpace {
		for index >= errorCounts[bucket] {
			index = 0
			bucket = (bucket - 1) & 15
		}
		symbol := sortedErrors[bucket][index]
		index++
		if symbols[symbol].bits == uint8(maxCodeLength) {
			continue
		}
		symbols[symbol].bits++
		usedCodeSpace -= codeSpace >> symbols[symbol].bits
	}

	remaining := codeSpace - usedCodeSpace
	for remaining != 0 {
		for index == 0 {
			bucket = (bucket + 1) & 15
			index = errorCounts[bucket]
		}
		index--
		symbol := sortedErrors[bucket][index]
		if (codeSpace >> symbols[symbol].bits) <= remaining {
			remaining -= codeSpace >> symbols[symbol].bits
			symbols[symbol].bits--
		}
	}
}

func packageMergeHuffmanCodegen(hist []uint32, symbols []huffmanSymbol, uniqueSymbols int, maxCodeLength int) {
	if uniqueSymbols > len(hist) {
		uniqueSymbols = len(hist)
	}
	if uniqueSymbols > len(symbols) {
		uniqueSymbols = len(symbols)
	}
	workspace := acquirePackageMergeWorkspace()

	originalCount := 0
	for i := 0; i < uniqueSymbols; i++ {
		symbols[i].code = 0
		if hist[i] == 0 {
			symbols[i].bits = uint8(maxCodeLength + 1)
			continue
		}
		symbols[i].bits = 0
		node := &workspace.original[originalCount]
		*node = packageMergeNode{histogramSum: uint64(hist[i])}
		node.symbolCounts[i] = 1
		workspace.originalList[originalCount] = node
		originalCount++
	}
	if originalCount == 0 {
		releasePackageMergeWorkspace(workspace)
		return
	}

	originalList := workspace.originalList[:originalCount]
	merged := workspace.merged[:originalCount]
	copy(merged, originalList)
	mergedNodes := 0
	for depth := 1; depth < maxCodeLength; depth++ {
		sort.Slice(merged, func(i, j int) bool {
			return merged[i].histogramSum < merged[j].histogramSum
		})

		newNodes := workspace.mergedNodes[mergedNodes][:]
		newCount := 0
		for i := 0; i < len(merged)&^1; i += 2 {
			left := merged[i]
			right := merged[i+1]
			node := &newNodes[newCount]
			node.histogramSum = left.histogramSum + right.histogramSum
			for symbol := 0; symbol < uniqueSymbols; symbol++ {
				node.symbolCounts[symbol] = left.symbolCounts[symbol] + right.symbolCounts[symbol]
			}
			newCount++
		}

		merged = workspace.merged[:originalCount+newCount]
		copy(merged, originalList)
		for i := 0; i < newCount; i++ {
			merged[originalCount+i] = &newNodes[i]
		}
		mergedNodes ^= 1
	}

	maxListLength := originalCount*2 - 2
	if maxListLength > len(merged) {
		maxListLength = len(merged)
	}
	for i := 0; i < maxListLength; i++ {
		for symbol := 0; symbol < uniqueSymbols; symbol++ {
			symbols[symbol].bits += merged[i].symbolCounts[symbol]
		}
	}
	releasePackageMergeWorkspace(workspace)
}

func fixedLog2(value int) int {
	base := log2(value)
	value <<= 5
	value >>= base
	return (base << 8) | int(fixedLog2FractionTable[value])
}

func createHuffmanCodes(symbols []huffmanSymbol, uniqueSymbols int, maxCodeLength int, codeSpace int) {
	var codeLengthCounts [maxHuffmanCodeLength + 2]int
	var accumulated [maxHuffmanCodeLength + 2]int
	for i := 0; i < uniqueSymbols; i++ {
		codeLengthCounts[symbols[i].bits]++
	}
	accumulator := 0
	for i := 1; i <= maxCodeLength; i++ {
		accumulated[i] = accumulator
		accumulator += (codeSpace >> i) * codeLengthCounts[i]
	}
	for i := 0; i < uniqueSymbols; i++ {
		codeBits := symbols[i].bits
		if codeBits == 0 || int(codeBits) > maxCodeLength {
			continue
		}
		symbols[i].code = uint16(accumulated[codeBits] >> (maxCodeLength - int(codeBits)))
		accumulated[codeBits] += codeSpace >> codeBits
	}
}

func encodeHuffmanStreams(data []byte, symbols *[256]huffmanSymbol) ([6]int, [6]encodedHuffmanStream) {
	var streamSizes [6]int
	var streams [6]encodedHuffmanStream
	for i := 5; i >= 0; i-- {
		streams[i] = encodeHuffmanStream(data, i, symbols)
		streamSizes[i] = len(streams[i].data)
	}
	return streamSizes, streams
}

func releaseEncodedHuffmanStreams(streams [6]encodedHuffmanStream) {
	for _, stream := range streams {
		releaseByteBuffer(stream.buffer)
	}
}

func encodeHuffmanStream(data []byte, start int, symbols *[256]huffmanSymbol) encodedHuffmanStream {
	symbolCount := 0
	if start < len(data) {
		symbolCount = (len(data)-1-start)/6 + 1
	}
	size := (symbolCount*maxHuffmanCodeLength+7)/8 + 16
	buf := acquireByteBuffer(size)
	buf = buf[:size]
	streamIt := len(buf)
	state := uint64(0)
	bitCount := uint(0)

	fastLoopEnd := len(data) - 30
	symbolIt := start
	for symbolIt < fastLoopEnd {
		h := symbols[data[symbolIt]]
		state = (state << h.bits) | uint64(h.code)
		bitCount += uint(h.bits)
		h = symbols[data[symbolIt+6]]
		state = (state << h.bits) | uint64(h.code)
		bitCount += uint(h.bits)
		h = symbols[data[symbolIt+12]]
		state = (state << h.bits) | uint64(h.code)
		bitCount += uint(h.bits)
		h = symbols[data[symbolIt+18]]
		state = (state << h.bits) | uint64(h.code)
		bitCount += uint(h.bits)
		h = symbols[data[symbolIt+24]]
		state = (state << h.bits) | uint64(h.code)
		bitCount += uint(h.bits)
		symbolIt += 30

		binary.LittleEndian.PutUint64(buf[streamIt-8:], state<<(64-bitCount))
		streamIt -= int(bitCount / 8)
		bitCount &= 7
		if bitCount == 0 {
			state = 0
		} else {
			state &= (uint64(1) << bitCount) - 1
		}
	}
	for ; symbolIt < len(data); symbolIt += 6 {
		h := symbols[data[symbolIt]]
		state = (state << h.bits) | uint64(h.code)
		bitCount += uint(h.bits)
	}
	if streamIt >= 8 {
		binary.LittleEndian.PutUint64(buf[streamIt-8:], state<<(64-bitCount))
		streamIt -= int(bitCount / 8)
		bitCount &= 7
		if bitCount == 0 {
			state = 0
		} else {
			state &= (uint64(1) << bitCount) - 1
		}
	}
	if bitCount > 0 {
		streamIt--
		buf[streamIt] = byte(state << (8 - bitCount))
	}

	return encodedHuffmanStream{data: buf[streamIt:], buffer: buf}
}

func generateHuffmanHeaderData(symbols *[256]huffmanSymbol, useFastCodegen bool) huffmanHeaderData {
	var data huffmanHeaderData
	codeLengthCount := 256
	for codeLengthCount > 0 && symbols[codeLengthCount-1].bits == maxHuffmanCodeLength+1 {
		codeLengthCount--
	}
	if codeLengthCount == 256 {
		codeLengthCount = 255
	}

	runLen := 1
	prevLen := symbols[0].bits
	for i := 1; i <= codeLengthCount; i++ {
		if symbols[i].bits == prevLen && i != codeLengthCount {
			runLen++
			continue
		}
		data.precodeHist[prevLen]++
		data.appendPrecode(precodeStreamSymbol{codeLength: prevLen})
		runBits := log2(runLen)
		for j := 0; j < runBits; j++ {
			data.precodeHist[prevLen]++
			data.appendPrecode(precodeStreamSymbol{
				codeLength: prevLen,
				runBit:     uint8((runLen >> j) & 1),
			})
			data.extraRawBits++
		}
		runLen = 1
		prevLen = symbols[i].bits
	}

	for i := 1; i <= maxHuffmanCodeLength+1; i++ {
		if data.precodeHist[i] == uint32(data.precodeCount) {
			// A single precode symbol would leave no decodable prefix tree.
			data.precodeHist[i]--
			data.precodeHist[1+i%12]++
		}
	}

	if useFastCodegen {
		fastHuffmanCodegen(data.precodeHist[1:], data.precodeSymbols[1:], data.precodeCount, maxHuffmanCodeLength+1, maxPrecodeCodeLength)
	} else {
		packageMergeHuffmanCodegen(data.precodeHist[1:], data.precodeSymbols[1:], maxHuffmanCodeLength+1, maxPrecodeCodeLength)
	}
	return data
}

func encodeHuffmanHeader(streamSizes [6]int, headerData *huffmanHeaderData) []byte {
	var output [128]byte
	outputIt := encodeHuffmanHeaderInto(streamSizes, headerData, &output)
	header := make([]byte, len(output)-outputIt)
	copy(header, output[outputIt:])
	return header
}

func encodeHuffmanHeaderInto(streamSizes [6]int, headerData *huffmanHeaderData, output *[128]byte) int {
	outputIt := len(output)
	state := uint64(0)
	bitCount := uint(0)
	normalize := func() {
		if bitCount == 0 {
			return
		}
		binary.LittleEndian.PutUint32(output[outputIt-4:], uint32(state<<(32-bitCount)))
		outputIt -= int(bitCount / 8)
		bitCount &= 7
	}

	virtualFirstSize := streamSizes[0] + 255
	firstSizeLog := log2(virtualFirstSize)
	state = uint64(firstSizeLog - 8)
	bitCount = 3
	state = (state << firstSizeLog) | uint64(virtualFirstSize&((1<<firstSizeLog)-1))
	bitCount += uint(firstSizeLog)

	var sizeErrors [6]int
	maxError := 0
	for i := 1; i < 6; i++ {
		delta := int(int16(streamSizes[i] - streamSizes[0]))
		sizeErrors[i] = (delta << 1) ^ (delta >> 15)
		if sizeErrors[i] > maxError {
			maxError = sizeErrors[i]
		}
	}

	maxErrorLog := intLog2(maxError) + 1
	state = (state << 4) | uint64(maxErrorLog-1)
	bitCount += 4
	normalize()

	for i := 1; i < 6; i++ {
		state = (state << maxErrorLog) | uint64(sizeErrors[i])
		bitCount += uint(maxErrorLog)
		normalize()
	}

	hskip := 0
	for hskip < 5 && headerData.precodeSymbols[hskip+1].bits == maxPrecodeCodeLength+1 {
		hskip++
	}
	state = (state << 3) | uint64(hskip)
	bitCount += 3

	usedCodeSpace := 0
	for i := hskip + 1; i <= maxHuffmanCodeLength && usedCodeSpace != precodeCodeSpace; i++ {
		codeBits := headerData.precodeSymbols[i].bits
		state = (state << 3) | uint64(codeBits-1)
		bitCount += 3
		usedCodeSpace += precodeCodeSpace >> codeBits
		normalize()
	}

	lastSymbol := uint8(0)
	for i := 0; i < headerData.precodeCount; i++ {
		streamSymbol := headerData.precodeStream[i]
		symbol := streamSymbol.codeLength
		h := headerData.precodeSymbols[symbol]
		state = (state << h.bits) | uint64(h.code)
		bitCount += uint(h.bits)
		if symbol == lastSymbol {
			state = (state << 1) | uint64(streamSymbol.runBit)
			bitCount++
		}
		lastSymbol = symbol
		normalize()
	}

	normalize()
	if bitCount > 0 {
		outputIt--
		output[outputIt] = byte(state << (8 - bitCount))
	}
	return outputIt
}
