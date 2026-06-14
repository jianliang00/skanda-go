package skanda

func decodeEntropy(src []byte, cpos *int) ([]byte, int, error) {
	return decodeEntropyWithState(src, cpos, nil)
}

func decodeEntropyWithState(src []byte, cpos *int, state *decodeState) ([]byte, int, error) {
	symbolCount, mode, flags, err := readHeader(src, cpos)
	if err != nil {
		return nil, 0, err
	}
	switch mode {
	case entropyRaw:
		if *cpos+symbolCount > len(src) {
			return nil, 0, ErrCorrupt
		}
		stream := src[*cpos : *cpos+symbolCount]
		*cpos += symbolCount
		return stream, flags, nil
	case entropyRLE:
		if *cpos >= len(src) {
			return nil, 0, ErrCorrupt
		}
		stream := state.alloc(symbolCount)
		if len(stream) > 0 {
			stream[0] = src[*cpos]
			for copied := 1; copied < len(stream); copied *= 2 {
				copy(stream[copied:], stream[:copied])
			}
		}
		*cpos += 1
		return stream, flags, nil
	case entropyHuffman:
		stream, err := decodeHuffmanEntropyWithState(src, cpos, symbolCount, state)
		if err != nil {
			return nil, 0, err
		}
		return stream, flags, nil
	default:
		return nil, 0, ErrUnsupportedEntropy
	}
}
