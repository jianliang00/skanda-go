package skanda

import "math"

var entropyCountLog2 [maxBlockSize + 1]float64

func init() {
	for i := 1; i < len(entropyCountLog2); i++ {
		entropyCountLog2[i] = float64(i) * math.Log2(float64(i))
	}
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
