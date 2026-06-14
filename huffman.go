package skanda

import (
	"encoding/binary"
	"math/bits"
)

const (
	maxHuffmanCodeLength = 11
	huffmanCodeSpace     = 1 << maxHuffmanCodeLength
	maxPrecodeCodeLength = 7
	precodeCodeSpace     = 1 << maxPrecodeCodeLength
)

type huffmanEntry uint16

func decodeHuffmanEntropyWithState(src []byte, cpos *int, symbolCount int, state *decodeState) ([]byte, error) {
	if *cpos >= len(src) {
		return nil, ErrCorrupt
	}
	headerSize := int(src[*cpos] & 0x7f)
	*cpos += 1
	if headerSize < 4 || *cpos+headerSize > len(src) {
		return nil, ErrCorrupt
	}

	var localTable [huffmanCodeSpace]huffmanEntry
	table := &localTable
	if state != nil {
		table = &state.huffmanTable
	}
	streamSizes, err := decodeHuffmanHeader(src, *cpos, *cpos+headerSize, table)
	if err != nil {
		return nil, err
	}
	*cpos += headerSize

	totalStreamSize := 0
	for _, size := range streamSizes {
		totalStreamSize += size
	}
	streamStart := *cpos
	streamEnd := streamStart + totalStreamSize
	if streamEnd > len(src) {
		return nil, ErrCorrupt
	}

	var out []byte
	if state != nil {
		out = state.alloc(symbolCount + 30)
	} else {
		out = make([]byte, symbolCount+30)
	}
	if err := decodeHuffmanSymbols(src, streamStart, streamSizes, symbolCount, table, out); err != nil {
		return nil, err
	}
	*cpos = streamEnd
	return out[:symbolCount], nil
}

func decodeHuffmanHeader(src []byte, start, end int, table *[huffmanCodeSpace]huffmanEntry) ([6]int, error) {
	var decodedSymbols [maxHuffmanCodeLength + 2][256]byte
	var decodedCounts [maxHuffmanCodeLength + 2]int
	var streamSizes [6]int

	huffIt := end - 4
	if huffIt < start || huffIt+4 > len(src) {
		return streamSizes, ErrCorrupt
	}
	huffState := binary.LittleEndian.Uint32(src[huffIt:])
	bitCount := uint(32)

	extract := func(n uint) uint32 {
		result := huffState >> (32 - n)
		huffState <<= n
		bitCount -= n
		return result
	}
	renormalize := func() error {
		if huffIt > start {
			if huffIt-4 < 0 {
				return ErrCorrupt
			}
			huffState |= binary.LittleEndian.Uint32(src[huffIt-4:]) >> bitCount
			huffIt -= int((bitCount ^ 31) >> 3)
			bitCount |= 24
		}
		return nil
	}

	firstSizeLog := int(extract(3)) + 8
	streamSizes[0] = int((uint32(1)<<firstSizeLog)|extract(uint(firstSizeLog))) - 255
	errorBits := uint(extract(4) + 1)
	if err := renormalize(); err != nil {
		return streamSizes, err
	}

	for i := 1; i < 6; i++ {
		foldedError := int(extract(errorBits))
		unfoldedError := foldedError >> 1
		if foldedError&1 != 0 {
			unfoldedError = -unfoldedError - 1
		}
		streamSizes[i] = (streamSizes[0] + unfoldedError) & 0xffff
		if err := renormalize(); err != nil {
			return streamSizes, err
		}
	}

	hskip := int(extract(3)) + 1
	usedCodeSpace := 0
	for i := hskip; i <= maxHuffmanCodeLength && usedCodeSpace < precodeCodeSpace; i++ {
		codeBits := int(extract(3)) + 1
		decodedSymbols[codeBits][decodedCounts[codeBits]] = byte(i)
		decodedCounts[codeBits]++
		usedCodeSpace += precodeCodeSpace >> codeBits
		if err := renormalize(); err != nil {
			return streamSizes, err
		}
	}
	if usedCodeSpace < precodeCodeSpace {
		if usedCodeSpace < precodeCodeSpace/2 {
			return streamSizes, ErrCorrupt
		}
		codeBits := maxPrecodeCodeLength - log2(precodeCodeSpace-usedCodeSpace)
		decodedSymbols[codeBits][decodedCounts[codeBits]] = maxHuffmanCodeLength + 1
		decodedCounts[codeBits]++
		usedCodeSpace += precodeCodeSpace >> codeBits
	}
	if usedCodeSpace != precodeCodeSpace {
		return streamSizes, ErrCorrupt
	}

	precodeTable, err := buildPrecodeDecodeTable(&decodedSymbols, &decodedCounts)
	if err != nil {
		return streamSizes, err
	}

	for i := range decodedCounts {
		decodedCounts[i] = 0
	}
	usedCodeSpace = 0
	lastSymbol := 0
	symbolRunLen := 0
	readSymbols := 0

	precodeOp := func() error {
		value := huffState >> (32 - maxPrecodeCodeLength)
		entry := precodeTable[value]
		entryBits := entry >> 8
		if entryBits == 0 {
			return ErrCorrupt
		}
		newSymbol := int(byte(entry))
		huffState <<= entryBits
		bitCount -= uint(entryBits)

		if newSymbol != lastSymbol {
			decodedSymbols[newSymbol][decodedCounts[newSymbol]] = byte(readSymbols)
			decodedCounts[newSymbol]++
			readSymbols++
			usedCodeSpace += huffmanCodeSpace >> newSymbol
			lastSymbol = newSymbol
			symbolRunLen = 0
			return nil
		}

		bit := int(extract(1))
		extraLen := (1 + bit) << symbolRunLen
		symbolRunLen++
		loopEnd := readSymbols + extraLen
		if loopEnd > 255 {
			return ErrCorrupt
		}
		for readSymbols < loopEnd {
			decodedSymbols[newSymbol][decodedCounts[newSymbol]] = byte(readSymbols)
			decodedCounts[newSymbol]++
			readSymbols++
		}
		usedCodeSpace += (huffmanCodeSpace >> newSymbol) * extraLen
		return nil
	}

	for readSymbols < 255 && usedCodeSpace != huffmanCodeSpace {
		if err := precodeOp(); err != nil {
			return streamSizes, err
		}
		if !(readSymbols < 255 && usedCodeSpace != huffmanCodeSpace) {
			break
		}
		if err := precodeOp(); err != nil {
			return streamSizes, err
		}
		if !(readSymbols < 255 && usedCodeSpace != huffmanCodeSpace) {
			break
		}
		if err := precodeOp(); err != nil {
			return streamSizes, err
		}
		if err := renormalize(); err != nil {
			return streamSizes, err
		}
	}

	if usedCodeSpace < huffmanCodeSpace {
		if usedCodeSpace < huffmanCodeSpace/2 {
			return streamSizes, ErrCorrupt
		}
		codeBits := maxHuffmanCodeLength - log2(huffmanCodeSpace-usedCodeSpace)
		decodedSymbols[codeBits][decodedCounts[codeBits]] = 255
		decodedCounts[codeBits]++
		usedCodeSpace += huffmanCodeSpace >> codeBits
	}
	if usedCodeSpace != huffmanCodeSpace {
		return streamSizes, ErrCorrupt
	}

	return streamSizes, buildDecodeTable(table, &decodedSymbols, &decodedCounts, maxHuffmanCodeLength)
}

func buildDecodeTable(table *[huffmanCodeSpace]huffmanEntry, decodedSymbols *[maxHuffmanCodeLength + 2][256]byte, decodedCounts *[maxHuffmanCodeLength + 2]int, maxCodeLength int) error {
	tableSize := 1 << maxCodeLength
	index := 0
	for codeBits := 1; codeBits <= maxCodeLength; codeBits++ {
		for i := 0; i < decodedCounts[codeBits]; i++ {
			repeat := 1 << (maxCodeLength - codeBits)
			if index+repeat > tableSize {
				return ErrCorrupt
			}
			entry := huffmanEntry(uint16(decodedSymbols[codeBits][i]) | uint16(codeBits)<<8)
			fillHuffmanEntries(table[index:index+repeat], entry)
			index += repeat
		}
	}
	if index != tableSize {
		return ErrCorrupt
	}
	return nil
}

func buildPrecodeDecodeTable(decodedSymbols *[maxHuffmanCodeLength + 2][256]byte, decodedCounts *[maxHuffmanCodeLength + 2]int) ([precodeCodeSpace]huffmanEntry, error) {
	var table [precodeCodeSpace]huffmanEntry
	index := 0
	for codeBits := 1; codeBits <= maxPrecodeCodeLength; codeBits++ {
		for i := 0; i < decodedCounts[codeBits]; i++ {
			repeat := 1 << (maxPrecodeCodeLength - codeBits)
			if index+repeat > precodeCodeSpace {
				return table, ErrCorrupt
			}
			entry := huffmanEntry(uint16(decodedSymbols[codeBits][i]) | uint16(codeBits)<<8)
			fillHuffmanEntries(table[index:index+repeat], entry)
			index += repeat
		}
	}
	if index != precodeCodeSpace {
		return table, ErrCorrupt
	}
	return table, nil
}

func fillHuffmanEntries(dst []huffmanEntry, entry huffmanEntry) {
	switch len(dst) {
	case 0:
		return
	case 1:
		dst[0] = entry
		return
	case 2:
		dst[0] = entry
		dst[1] = entry
		return
	case 4:
		dst[0] = entry
		dst[1] = entry
		dst[2] = entry
		dst[3] = entry
		return
	case 8:
		dst[0] = entry
		dst[1] = entry
		dst[2] = entry
		dst[3] = entry
		dst[4] = entry
		dst[5] = entry
		dst[6] = entry
		dst[7] = entry
		return
	}
	for len(dst) >= 16 {
		dst[0] = entry
		dst[1] = entry
		dst[2] = entry
		dst[3] = entry
		dst[4] = entry
		dst[5] = entry
		dst[6] = entry
		dst[7] = entry
		dst[8] = entry
		dst[9] = entry
		dst[10] = entry
		dst[11] = entry
		dst[12] = entry
		dst[13] = entry
		dst[14] = entry
		dst[15] = entry
		dst = dst[16:]
	}
	for i := range dst {
		dst[i] = entry
	}
}

func decodeHuffmanSymbols(src []byte, streamStart int, streamSizes [6]int, symbolCount int, table *[huffmanCodeSpace]huffmanEntry, out []byte) error {
	stream0 := streamStart + streamSizes[0] - 8
	stream1 := stream0 + streamSizes[1]
	stream2 := stream1 + streamSizes[2]
	stream3 := stream2 + streamSizes[3]
	stream4 := stream3 + streamSizes[4]
	stream5 := stream4 + streamSizes[5]
	if stream0 < 0 || stream0+8 > len(src) ||
		stream1 < 0 || stream1+8 > len(src) ||
		stream2 < 0 || stream2+8 > len(src) ||
		stream3 < 0 || stream3+8 > len(src) ||
		stream4 < 0 || stream4+8 > len(src) ||
		stream5 < 0 || stream5+8 > len(src) {
		return ErrCorrupt
	}
	state0 := binary.LittleEndian.Uint64(src[stream0:]) | 1
	state1 := binary.LittleEndian.Uint64(src[stream1:]) | 1
	state2 := binary.LittleEndian.Uint64(src[stream2:]) | 1
	state3 := binary.LittleEndian.Uint64(src[stream3:]) | 1
	state4 := binary.LittleEndian.Uint64(src[stream4:]) | 1
	state5 := binary.LittleEndian.Uint64(src[stream5:]) | 1

	outPos := 0
	fastLoopEnd := symbolCount - symbolCount%30
	for outPos < fastLoopEnd {
		// The bounded chunk lets the compiler remove per-symbol output checks.
		outChunk := out[outPos:]
		_ = outChunk[29]
		state0, outChunk[0] = decodeHuffmanOpState(state0, table)
		state1, outChunk[1] = decodeHuffmanOpState(state1, table)
		state2, outChunk[2] = decodeHuffmanOpState(state2, table)
		state3, outChunk[3] = decodeHuffmanOpState(state3, table)
		state4, outChunk[4] = decodeHuffmanOpState(state4, table)
		state5, outChunk[5] = decodeHuffmanOpState(state5, table)
		state0, outChunk[6] = decodeHuffmanOpState(state0, table)
		state1, outChunk[7] = decodeHuffmanOpState(state1, table)
		state2, outChunk[8] = decodeHuffmanOpState(state2, table)
		state3, outChunk[9] = decodeHuffmanOpState(state3, table)
		state4, outChunk[10] = decodeHuffmanOpState(state4, table)
		state5, outChunk[11] = decodeHuffmanOpState(state5, table)
		state0, outChunk[12] = decodeHuffmanOpState(state0, table)
		state1, outChunk[13] = decodeHuffmanOpState(state1, table)
		state2, outChunk[14] = decodeHuffmanOpState(state2, table)
		state3, outChunk[15] = decodeHuffmanOpState(state3, table)
		state4, outChunk[16] = decodeHuffmanOpState(state4, table)
		state5, outChunk[17] = decodeHuffmanOpState(state5, table)
		state0, outChunk[18] = decodeHuffmanOpState(state0, table)
		state1, outChunk[19] = decodeHuffmanOpState(state1, table)
		state2, outChunk[20] = decodeHuffmanOpState(state2, table)
		state3, outChunk[21] = decodeHuffmanOpState(state3, table)
		state4, outChunk[22] = decodeHuffmanOpState(state4, table)
		state5, outChunk[23] = decodeHuffmanOpState(state5, table)
		state0, outChunk[24] = decodeHuffmanOpState(state0, table)
		state1, outChunk[25] = decodeHuffmanOpState(state1, table)
		state2, outChunk[26] = decodeHuffmanOpState(state2, table)
		state3, outChunk[27] = decodeHuffmanOpState(state3, table)
		state4, outChunk[28] = decodeHuffmanOpState(state4, table)
		state5, outChunk[29] = decodeHuffmanOpState(state5, table)

		if stream0 >= streamStart {
			var ok bool
			state0, stream0, ok = renormalizeHuffmanFast(state0, stream0, src)
			if !ok {
				return ErrCorrupt
			}
		}
		if stream1 >= streamStart {
			var ok bool
			state1, stream1, ok = renormalizeHuffmanFast(state1, stream1, src)
			if !ok {
				return ErrCorrupt
			}
		}
		if stream2 >= streamStart {
			var ok bool
			state2, stream2, ok = renormalizeHuffmanFast(state2, stream2, src)
			if !ok {
				return ErrCorrupt
			}
		}
		if stream3 >= streamStart {
			var ok bool
			state3, stream3, ok = renormalizeHuffmanFast(state3, stream3, src)
			if !ok {
				return ErrCorrupt
			}
		}
		if stream4 >= streamStart {
			var ok bool
			state4, stream4, ok = renormalizeHuffmanFast(state4, stream4, src)
			if !ok {
				return ErrCorrupt
			}
		}
		if stream5 >= streamStart {
			var ok bool
			state5, stream5, ok = renormalizeHuffmanFast(state5, stream5, src)
			if !ok {
				return ErrCorrupt
			}
		}
		outPos += 30
	}

	for outPos < symbolCount {
		// The output buffer has 30 bytes of padding for this six-stream tail.
		outChunk := out[outPos:]
		_ = outChunk[5]
		state0, outChunk[0] = decodeHuffmanOpState(state0, table)
		state1, outChunk[1] = decodeHuffmanOpState(state1, table)
		state2, outChunk[2] = decodeHuffmanOpState(state2, table)
		state3, outChunk[3] = decodeHuffmanOpState(state3, table)
		state4, outChunk[4] = decodeHuffmanOpState(state4, table)
		state5, outChunk[5] = decodeHuffmanOpState(state5, table)
		outPos += 6
	}
	return nil
}

func decodeHuffmanOpState(state uint64, table *[huffmanCodeSpace]huffmanEntry) (uint64, byte) {
	value := state >> (64 - maxHuffmanCodeLength)
	entry := table[value]
	return state << (entry >> 8), byte(entry)
}

func renormalizeHuffmanFast(state uint64, stream int, src []byte) (uint64, int, bool) {
	consumedBits := bits.TrailingZeros64(state)
	stream -= consumedBits >> 3
	if stream < 0 || stream+8 > len(src) {
		return 0, stream, false
	}
	state = binary.LittleEndian.Uint64(src[stream:]) | 1
	state <<= consumedBits & 7
	return state, stream, true
}

func log2(value int) int {
	return bits.Len(uint(value)) - 1
}

func intLog2(value int) int {
	if value == 0 {
		return 0
	}
	return log2(value)
}
