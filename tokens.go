package skanda

import (
	"encoding/binary"
	"math/bits"
)

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
