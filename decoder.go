package skanda

type decodeState struct {
	scratch         []byte
	distanceScratch []uint32
	huffmanTable    [huffmanCodeSpace]huffmanEntry
}

func (state *decodeState) reset() {
	if state != nil {
		state.scratch = state.scratch[:0]
		state.distanceScratch = state.distanceScratch[:0]
	}
}

func (state *decodeState) release() {
	if state == nil {
		return
	}
	releaseByteBuffer(state.scratch)
	state.scratch = nil
	releaseUint32Buffer(state.distanceScratch)
	state.distanceScratch = nil
}

func (state *decodeState) alloc(size int) []byte {
	if size <= 0 {
		return nil
	}
	if state == nil {
		return make([]byte, size)
	}
	start := len(state.scratch)
	need := start + size
	if cap(state.scratch) < need {
		nextCap := cap(state.scratch) * 2
		if nextCap < need {
			nextCap = need
		}
		next := make([]byte, need, nextCap)
		copy(next, state.scratch)
		state.scratch = next
	} else {
		state.scratch = state.scratch[:need]
	}
	return state.scratch[start:need]
}

func (state *decodeState) uint32s(size int) []uint32 {
	if size <= 0 {
		return nil
	}
	if state == nil {
		return make([]uint32, size)
	}
	if cap(state.distanceScratch) < size {
		releaseUint32Buffer(state.distanceScratch)
		state.distanceScratch = acquireUint32Buffer(size)
	} else {
		state.distanceScratch = state.distanceScratch[:size]
	}
	return state.distanceScratch
}

// Decompress allocates an output buffer of decompressedSize and decodes src into it.
func Decompress(src []byte, decompressedSize int) ([]byte, error) {
	if decompressedSize < 0 {
		return nil, ErrCorrupt
	}
	dst := make([]byte, decompressedSize)
	if err := Decode(dst, src); err != nil {
		return nil, err
	}
	return dst, nil
}

// Decode decodes src into dst. dst must have the exact decompressed size.
func Decode(dst, src []byte) error {
	if len(dst) > maxSharedDecoderOutputSize {
		decodeState := decodeState{scratch: acquireByteBuffer(64 << 10)}
		defer decodeState.release()
		return decodeWithState(dst, src, &decodeState)
	}
	decoder, _ := sharedDecoderPool.Get().(*Decoder)
	if decoder == nil {
		decoder = new(Decoder)
	}
	if decoder.state.scratch == nil {
		decoder.state.scratch = acquireByteBuffer(64 << 10)
	}
	err := decodeWithState(dst, src, &decoder.state)
	decoder.state.reset()
	sharedDecoderPool.Put(decoder)
	return err
}

// Decode decodes src into dst using reusable decoder state.
func (decoder *Decoder) Decode(dst, src []byte) error {
	if decoder == nil {
		return Decode(dst, src)
	}
	if decoder.state.scratch == nil {
		decoder.state.scratch = acquireByteBuffer(64 << 10)
	}
	return decodeWithState(dst, src, &decoder.state)
}

// Close releases scratch memory held by decoder.
func (decoder *Decoder) Close() {
	if decoder == nil {
		return
	}
	decoder.state.release()
}

func decodeWithState(dst, src []byte, decodeState *decodeState) error {
	if len(src) == 0 {
		return ErrCorrupt
	}
	decodeState.reset()

	cpos := 0
	dpos := 0
	repOffsets := [7]int{1, 1, 1, 1, 1, 1, 1}

	for {
		blockSize, blockType, flags, err := readHeader(src, &cpos)
		if err != nil {
			return err
		}

		switch blockType {
		case blockRaw:
			if dpos+blockSize > len(dst) || cpos+blockSize > len(src) {
				return ErrCorrupt
			}
			copy(dst[dpos:dpos+blockSize], src[cpos:cpos+blockSize])
			cpos += blockSize
			dpos += blockSize
			if flags&blockLast != 0 {
				if dpos != len(dst) {
					return ErrCorrupt
				}
				return nil
			}
		case blockCompressed:
			if flags&blockLast != 0 {
				return ErrCorrupt
			}
			decodeState.reset()
			blockEnd := dpos + blockSize
			if blockEnd > len(dst) {
				return ErrCorrupt
			}
			if len(dst) >= lastBytes && blockEnd > len(dst)-lastBytes {
				return ErrCorrupt
			}
			if dpos == 0 {
				if cpos >= len(src) || blockSize == 0 {
					return ErrCorrupt
				}
				dst[dpos] = src[cpos]
				dpos++
				cpos++
			}

			literals, literalFlags, err := decodeEntropyWithState(src, &cpos, decodeState)
			if err != nil {
				return err
			}
			literalStreamCount := 1
			var literalStreams [4][]byte
			if literalFlags&streamLiteralsPosMask3 != 0 {
				literalStreams[0] = literals
				literalStreamCount = 4
				for i := 1; i < 4; i++ {
					stream, _, err := decodeEntropyWithState(src, &cpos, decodeState)
					if err != nil {
						return err
					}
					literalStreams[i] = stream
				}
			}

			tokens, _, err := decodeEntropyWithState(src, &cpos, decodeState)
			if err != nil {
				return err
			}
			distances, distanceFlags, err := decodeEntropyWithState(src, &cpos, decodeState)
			if err != nil {
				return err
			}
			var advancedDistances []uint32
			if distanceFlags&streamDistanceAdvanced != 0 {
				advancedDistances, err = decodeAdvancedDistancesWithState(src, &cpos, distances, decodeState)
				if err != nil {
					return err
				}
			}
			lengths, _, err := decodeEntropyWithState(src, &cpos, decodeState)
			if err != nil {
				return err
			}
			var lzErr error
			if distanceFlags&streamDistanceAdvanced != 0 {
				if literalFlags&(streamLiteralsPosMask3|streamLiteralsDelta) == 0 {
					lzErr = decodeLZBlockAdvancedRawLiterals(dst, &dpos, blockEnd, literals, tokens, advancedDistances, lengths, &repOffsets)
				} else if literalFlags == streamLiteralsDelta {
					lzErr = decodeLZBlockAdvancedDeltaLiterals(dst, &dpos, blockEnd, literals, tokens, advancedDistances, lengths, &repOffsets)
				} else {
					lzErr = decodeLZBlockAdvanced(dst, &dpos, blockEnd, &literalStreams, literalStreamCount, tokens, advancedDistances, lengths, literalFlags, &repOffsets)
				}
			} else if literalFlags&(streamLiteralsPosMask3|streamLiteralsDelta) == 0 {
				lzErr = decodeLZBlockStandardRawLiterals(dst, &dpos, blockEnd, literals, tokens, distances, lengths, &repOffsets)
			} else {
				literalStreams[0] = literals
				lzErr = decodeLZBlockStandard(dst, &dpos, blockEnd, &literalStreams, literalStreamCount, tokens, distances, lengths, literalFlags, &repOffsets)
			}
			if lzErr != nil {
				return lzErr
			}
			dpos = blockEnd
		default:
			return ErrCorrupt
		}
	}
}
