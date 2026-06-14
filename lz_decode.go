package skanda

import "encoding/binary"

func decodeLZBlockStandard(dst []byte, dpos *int, blockEnd int, literalStreams *[4][]byte, literalStreamCount int, tokens, distances, lengths []byte, literalFlags int, repOffsets *[7]int) error {
	if literalFlags&(streamLiteralsPosMask3|streamLiteralsDelta) == 0 {
		if literalStreamCount == 0 {
			return ErrCorrupt
		}
		return decodeLZBlockStandardRawLiterals(dst, dpos, blockEnd, literalStreams[0], tokens, distances, lengths, repOffsets)
	}

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	var literalPos [4]int
	distance := repOffsets[3]
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if outPos+litLen > blockEnd {
			return ErrCorrupt
		}
		if litLen > 0 {
			if err := copyLiterals(dst, outPos, litLen, literalStreams, literalStreamCount, &literalPos, literalFlags, distance); err != nil {
				return err
			}
		}
		outPos += litLen
		if outPos >= blockEnd {
			break
		}

		switch token & 0x18 {
		case 0x08:
			if distancePos >= len(distances) {
				return ErrCorrupt
			}
			distance = int(distances[distancePos])
			distancePos++
		case 0x10:
			if distancePos+2 > len(distances) {
				return ErrCorrupt
			}
			distance = int(binary.LittleEndian.Uint16(distances[distancePos:]))
			distancePos += 2
		case 0x18:
			if distancePos+3 > len(distances) {
				return ErrCorrupt
			}
			distance = int(uint32(distances[distancePos]) | uint32(distances[distancePos+1])<<8 | uint32(distances[distancePos+2])<<16)
			distancePos += 3
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > blockEnd {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	repOffsets[3] = distance
	*dpos = outPos
	return nil
}

func decodeLZBlockStandardRawLiterals(dst []byte, dpos *int, blockEnd int, rawLiterals, tokens, distances, lengths []byte, repOffsets *[7]int) error {
	if blockEnd > len(dst) {
		return ErrCorrupt
	}
	dst = dst[:blockEnd]

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	rawLiteralPos := 0
	distance := repOffsets[3]
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if outPos+litLen > len(dst) || rawLiteralPos+litLen > len(rawLiterals) {
			return ErrCorrupt
		}
		if litLen > 0 {
			switch litLen {
			case 1:
				dst[outPos] = rawLiterals[rawLiteralPos]
			case 2:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
			case 3:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
			case 4:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
			case 5:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
			case 6:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
				dst[outPos+5] = rawLiterals[rawLiteralPos+5]
			default:
				copy(dst[outPos:outPos+litLen], rawLiterals[rawLiteralPos:rawLiteralPos+litLen])
			}
			rawLiteralPos += litLen
		}
		outPos += litLen
		if outPos >= len(dst) {
			break
		}

		switch token & 0x18 {
		case 0x08:
			if distancePos >= len(distances) {
				return ErrCorrupt
			}
			distance = int(distances[distancePos])
			distancePos++
		case 0x10:
			if distancePos+2 > len(distances) {
				return ErrCorrupt
			}
			distance = int(binary.LittleEndian.Uint16(distances[distancePos:]))
			distancePos += 2
		case 0x18:
			if distancePos+3 > len(distances) {
				return ErrCorrupt
			}
			distance = int(uint32(distances[distancePos]) | uint32(distances[distancePos+1])<<8 | uint32(distances[distancePos+2])<<16)
			distancePos += 3
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > len(dst) {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	repOffsets[3] = distance
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvanced(dst []byte, dpos *int, blockEnd int, literalStreams *[4][]byte, literalStreamCount int, tokens []byte, advancedDistances []uint32, lengths []byte, literalFlags int, repOffsets *[7]int) error {
	if literalFlags&(streamLiteralsPosMask3|streamLiteralsDelta) == 0 {
		if literalStreamCount == 0 {
			return ErrCorrupt
		}
		return decodeLZBlockAdvancedRawLiterals(dst, dpos, blockEnd, literalStreams[0], tokens, advancedDistances, lengths, repOffsets)
	}
	if literalFlags == streamLiteralsDelta {
		if literalStreamCount == 0 {
			return ErrCorrupt
		}
		return decodeLZBlockAdvancedDeltaLiterals(dst, dpos, blockEnd, literalStreams[0], tokens, advancedDistances, lengths, repOffsets)
	}

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	var literalPos [4]int
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	distance := rep0
	outPos := *dpos

	for outPos < blockEnd {
		if tokenPos >= len(tokens) {
			return ErrCorrupt
		}
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if outPos+litLen > blockEnd {
			return ErrCorrupt
		}
		if litLen > 0 {
			if err := copyLiterals(dst, outPos, litLen, literalStreams, literalStreamCount, &literalPos, literalFlags, distance); err != nil {
				return err
			}
		}
		outPos += litLen
		if outPos >= blockEnd {
			break
		}

		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > blockEnd {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < blockEnd {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvancedDeltaLiterals(dst []byte, dpos *int, blockEnd int, deltaLiterals, tokens []byte, advancedDistances []uint32, lengths []byte, repOffsets *[7]int) error {
	if lengthStreamUsesSingleByteCodes(lengths) {
		return decodeLZBlockAdvancedDeltaLiteralsSingleByteLengths(dst, dpos, blockEnd, deltaLiterals, tokens, advancedDistances, lengths, repOffsets)
	}

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	deltaLiteralPos := 0
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	distance := rep0
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if litLen > 0 {
			outLitEnd := outPos + litLen
			deltaLiteralEnd := deltaLiteralPos + litLen
			if outLitEnd > blockEnd || deltaLiteralEnd > len(deltaLiterals) {
				return ErrCorrupt
			}
			if distance <= 0 || distance > outPos {
				return ErrCorrupt
			}
			refPos := outPos - distance
			switch litLen {
			case 1:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
			case 2:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
			case 3:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
				dst[outPos+2] = deltaLiterals[deltaLiteralPos+2] + dst[refPos+2]
			case 4:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
				dst[outPos+2] = deltaLiterals[deltaLiteralPos+2] + dst[refPos+2]
				dst[outPos+3] = deltaLiterals[deltaLiteralPos+3] + dst[refPos+3]
			default:
				dstRun := dst[outPos:outLitEnd]
				deltaRun := deltaLiterals[deltaLiteralPos:deltaLiteralEnd]
				refRun := dst[refPos : refPos+litLen]
				reconstructDeltaLiterals(dstRun, deltaRun, refRun)
			}
			deltaLiteralPos = deltaLiteralEnd
			outPos = outLitEnd
		}
		if outPos >= blockEnd {
			break
		}

		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > blockEnd {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < blockEnd {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvancedDeltaLiteralsSingleByteLengths(dst []byte, dpos *int, blockEnd int, deltaLiterals, tokens []byte, advancedDistances []uint32, lengths []byte, repOffsets *[7]int) error {
	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	deltaLiteralPos := 0
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	distance := rep0
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			litLen += int(lengths[lengthPos])
			lengthPos++
		}
		if litLen > 0 {
			outLitEnd := outPos + litLen
			deltaLiteralEnd := deltaLiteralPos + litLen
			if outLitEnd > blockEnd || deltaLiteralEnd > len(deltaLiterals) {
				return ErrCorrupt
			}
			if distance <= 0 || distance > outPos {
				return ErrCorrupt
			}
			refPos := outPos - distance
			switch litLen {
			case 1:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
			case 2:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
			case 3:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
				dst[outPos+2] = deltaLiterals[deltaLiteralPos+2] + dst[refPos+2]
			case 4:
				dst[outPos] = deltaLiterals[deltaLiteralPos] + dst[refPos]
				dst[outPos+1] = deltaLiterals[deltaLiteralPos+1] + dst[refPos+1]
				dst[outPos+2] = deltaLiterals[deltaLiteralPos+2] + dst[refPos+2]
				dst[outPos+3] = deltaLiterals[deltaLiteralPos+3] + dst[refPos+3]
			default:
				dstRun := dst[outPos:outLitEnd]
				deltaRun := deltaLiterals[deltaLiteralPos:deltaLiteralEnd]
				refRun := dst[refPos : refPos+litLen]
				reconstructDeltaLiterals(dstRun, deltaRun, refRun)
			}
			deltaLiteralPos = deltaLiteralEnd
			outPos = outLitEnd
		}
		if outPos >= blockEnd {
			break
		}

		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			matchLen = int(lengths[lengthPos]) + minMatchLength + 7
			lengthPos++
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > blockEnd {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < blockEnd {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvancedRawLiterals(dst []byte, dpos *int, blockEnd int, rawLiterals, tokens []byte, advancedDistances []uint32, lengths []byte, repOffsets *[7]int) error {
	if lengthStreamUsesSingleByteCodes(lengths) {
		return decodeLZBlockAdvancedRawLiteralsSingleByteLengths(dst, dpos, blockEnd, rawLiterals, tokens, advancedDistances, lengths, repOffsets)
	}
	if blockEnd > len(dst) {
		return ErrCorrupt
	}
	dst = dst[:blockEnd]

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	rawLiteralPos := 0
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			litLen += extra
		}
		if outPos+litLen > len(dst) || rawLiteralPos+litLen > len(rawLiterals) {
			return ErrCorrupt
		}
		if litLen > 0 {
			switch litLen {
			case 1:
				dst[outPos] = rawLiterals[rawLiteralPos]
			case 2:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
			case 3:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
			case 4:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
			case 5:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
			case 6:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
				dst[outPos+5] = rawLiterals[rawLiteralPos+5]
			default:
				copy(dst[outPos:outPos+litLen], rawLiterals[rawLiteralPos:rawLiteralPos+litLen])
			}
			rawLiteralPos += litLen
		}
		outPos += litLen
		if outPos >= len(dst) {
			break
		}

		var distance int
		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			extra := int(lengths[lengthPos])
			lengthPos++
			if extra > 223 {
				if lengthPos >= len(lengths) {
					return ErrCorrupt
				}
				second := int(lengths[lengthPos])
				lengthPos++
				if second <= 223 {
					extra = (second << 5) + extra
				} else {
					if lengthPos >= len(lengths) {
						return ErrCorrupt
					}
					third := int(lengths[lengthPos])
					lengthPos++
					extra = (((third << 5) + second) << 5) + extra
				}
			}
			matchLen = extra + minMatchLength + 7
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > len(dst) {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < len(dst) {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func decodeLZBlockAdvancedRawLiteralsSingleByteLengths(dst []byte, dpos *int, blockEnd int, rawLiterals, tokens []byte, advancedDistances []uint32, lengths []byte, repOffsets *[7]int) error {
	if blockEnd > len(dst) {
		return ErrCorrupt
	}
	dst = dst[:blockEnd]

	tokenPos := 0
	distancePos := 0
	lengthPos := 0
	rawLiteralPos := 0
	rep0 := repOffsets[3]
	rep1 := repOffsets[4]
	rep2 := repOffsets[5]
	outPos := *dpos

	for tokenPos < len(tokens) && outPos < len(dst) {
		token := tokens[tokenPos]
		tokenPos++

		litLen := int(token >> 5)
		if litLen >= 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			litLen += int(lengths[lengthPos])
			lengthPos++
		}
		if outPos+litLen > len(dst) || rawLiteralPos+litLen > len(rawLiterals) {
			return ErrCorrupt
		}
		if litLen > 0 {
			switch litLen {
			case 1:
				dst[outPos] = rawLiterals[rawLiteralPos]
			case 2:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
			case 3:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
			case 4:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
			case 5:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
			case 6:
				dst[outPos] = rawLiterals[rawLiteralPos]
				dst[outPos+1] = rawLiterals[rawLiteralPos+1]
				dst[outPos+2] = rawLiterals[rawLiteralPos+2]
				dst[outPos+3] = rawLiterals[rawLiteralPos+3]
				dst[outPos+4] = rawLiterals[rawLiteralPos+4]
				dst[outPos+5] = rawLiterals[rawLiteralPos+5]
			default:
				copy(dst[outPos:outPos+litLen], rawLiterals[rawLiteralPos:rawLiteralPos+litLen])
			}
			rawLiteralPos += litLen
		}
		outPos += litLen
		if outPos >= len(dst) {
			break
		}

		var distance int
		if token&0x18 == 0 {
			distance = rep0
		} else {
			switch token & 0x18 {
			case 0x08:
				distance = rep1
				rep1 = rep0
				rep0 = distance
			case 0x10:
				distance = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			default:
				if distancePos >= len(advancedDistances) {
					return ErrCorrupt
				}
				distance = int(advancedDistances[distancePos])
				distancePos++
				rep2 = rep1
				rep1 = rep0
				rep0 = distance
			}
		}
		if distance <= 0 || distance > outPos {
			return ErrCorrupt
		}

		matchLen := int(token & 7)
		if matchLen == 7 {
			if lengthPos >= len(lengths) {
				return ErrCorrupt
			}
			matchLen = int(lengths[lengthPos]) + minMatchLength + 7
			lengthPos++
		} else {
			matchLen += minMatchLength
		}
		if outPos+matchLen > len(dst) {
			return ErrCorrupt
		}
		copyMatch(dst, outPos, distance, matchLen)
		outPos += matchLen
	}
	if outPos < len(dst) {
		return ErrCorrupt
	}
	repOffsets[3] = rep0
	repOffsets[4] = rep1
	repOffsets[5] = rep2
	*dpos = outPos
	return nil
}

func lengthStreamUsesSingleByteCodes(lengths []byte) bool {
	const highBitMask = 0x8080808080808080
	i := 0
	for ; i+16 <= len(lengths); i += 16 {
		// Bytes without the high bit set cannot be extended-length markers.
		left := binary.LittleEndian.Uint64(lengths[i:])
		right := binary.LittleEndian.Uint64(lengths[i+8:])
		if (left|right)&highBitMask != 0 {
			for j := 0; j < 16; j++ {
				if lengths[i+j] > 223 {
					return false
				}
			}
		}
	}
	for ; i+8 <= len(lengths); i += 8 {
		// Bytes without the high bit set cannot be extended-length markers.
		if binary.LittleEndian.Uint64(lengths[i:])&highBitMask != 0 {
			if lengths[i] > 223 || lengths[i+1] > 223 || lengths[i+2] > 223 || lengths[i+3] > 223 ||
				lengths[i+4] > 223 || lengths[i+5] > 223 || lengths[i+6] > 223 || lengths[i+7] > 223 {
				return false
			}
		}
	}
	for ; i < len(lengths); i++ {
		value := lengths[i]
		if value > 223 {
			return false
		}
	}
	return true
}

func reconstructDeltaLiterals(dstRun, deltaRun, refRun []byte) {
	n := len(deltaRun)
	if n == 0 {
		return
	}
	_ = dstRun[n-1]
	_ = refRun[n-1]
	i := 0
	for ; i+8 <= n; i += 8 {
		dstRun[i] = deltaRun[i] + refRun[i]
		dstRun[i+1] = deltaRun[i+1] + refRun[i+1]
		dstRun[i+2] = deltaRun[i+2] + refRun[i+2]
		dstRun[i+3] = deltaRun[i+3] + refRun[i+3]
		dstRun[i+4] = deltaRun[i+4] + refRun[i+4]
		dstRun[i+5] = deltaRun[i+5] + refRun[i+5]
		dstRun[i+6] = deltaRun[i+6] + refRun[i+6]
		dstRun[i+7] = deltaRun[i+7] + refRun[i+7]
	}
	for ; i < n; i++ {
		dstRun[i] = deltaRun[i] + refRun[i]
	}
}

func copyLiterals(dst []byte, dpos int, length int, streams *[4][]byte, streamCount int, positions *[4]int, flags int, distance int) error {
	if flags&streamLiteralsPosMask3 == 0 {
		if streamCount == 0 || positions[0]+length > len(streams[0]) {
			return ErrCorrupt
		}
		if flags&streamLiteralsDelta == 0 {
			copy(dst[dpos:dpos+length], streams[0][positions[0]:positions[0]+length])
		} else {
			if distance <= 0 || distance > dpos {
				return ErrCorrupt
			}
			dstRun := dst[dpos : dpos+length]
			deltaRun := streams[0][positions[0] : positions[0]+length]
			refRun := dst[dpos-distance : dpos-distance+length]
			reconstructDeltaLiterals(dstRun, deltaRun, refRun)
		}
		positions[0] += length
		return nil
	}

	if streamCount < 4 {
		return ErrCorrupt
	}
	for i := 0; i < length; i++ {
		stream := (dpos + i) & 3
		if positions[stream] >= len(streams[stream]) {
			return ErrCorrupt
		}
		value := streams[stream][positions[stream]]
		positions[stream]++
		if flags&streamLiteralsDelta != 0 {
			if distance <= 0 || distance > dpos+i {
				return ErrCorrupt
			}
			value += dst[dpos+i-distance]
		}
		dst[dpos+i] = value
	}
	return nil
}

func copyMatch(dst []byte, pos, distance, length int) {
	if distance >= length {
		copy(dst[pos:pos+length], dst[pos-distance:pos-distance+length])
		return
	}
	copied := copy(dst[pos:pos+length], dst[pos-distance:pos])
	for copied < length {
		copied += copy(dst[pos+copied:pos+length], dst[pos:pos+copied])
	}
}
