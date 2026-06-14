package skanda

import "encoding/binary"

func decodeAdvancedDistancesWithState(src []byte, cpos *int, distanceTokens []byte, scratch *decodeState) ([]uint32, error) {
	if len(distanceTokens) == 0 {
		return nil, nil
	}
	if *cpos+8 > len(src) {
		return nil, ErrCorrupt
	}

	decoded := scratch.uint32s(len(distanceTokens))
	pos := *cpos
	state := binary.LittleEndian.Uint64(src[pos:])
	stateBitCount := uint(56)
	pos += 7

	outPos := 0
	for tokenPos := 0; tokenPos < len(distanceTokens); {
		distance, nextState, nextBitCount, err := decodeAdvancedDistanceOp(distanceTokens[tokenPos], state, stateBitCount)
		if err != nil {
			return nil, err
		}
		state = nextState
		stateBitCount = nextBitCount
		decoded[outPos] = distance
		tokenPos++
		outPos++
		if tokenPos == len(distanceTokens) {
			break
		}
		distance, nextState, nextBitCount, err = decodeAdvancedDistanceOp(distanceTokens[tokenPos], state, stateBitCount)
		if err != nil {
			return nil, err
		}
		state = nextState
		stateBitCount = nextBitCount
		decoded[outPos] = distance
		tokenPos++
		outPos++
		var ok bool
		state, pos, stateBitCount, ok = renormalizeAdvancedDistance(src, pos, state, stateBitCount)
		if !ok {
			return nil, ErrCorrupt
		}
	}

	if stateBitCount >= 64 {
		return nil, ErrCorrupt
	}
	*cpos = pos - int(stateBitCount/8)
	if *cpos > len(src) {
		return nil, ErrCorrupt
	}
	return decoded, nil
}

func decodeAdvancedDistanceOp(token byte, state uint64, stateBitCount uint) (uint32, uint64, uint, error) {
	distanceBits := uint(token >> 3)
	distanceLow := uint64(token & 7)
	if distanceBits > 28 {
		return 0, state, stateBitCount, ErrCorrupt
	}
	highBit := uint64(1) << distanceBits
	distance := ((state & (highBit - 1)) | highBit) * 8
	distance -= distanceLow
	state >>= distanceBits
	stateBitCount -= distanceBits
	return uint32(distance), state, stateBitCount, nil
}

func renormalizeAdvancedDistance(src []byte, pos int, state uint64, stateBitCount uint) (uint64, int, uint, bool) {
	if pos < len(src) {
		if pos+8 > len(src) {
			return state, pos, stateBitCount, false
		}
		state |= binary.LittleEndian.Uint64(src[pos:]) << stateBitCount
		pos += int((stateBitCount ^ 63) >> 3)
		stateBitCount |= 56
	}
	return state, pos, stateBitCount, true
}
