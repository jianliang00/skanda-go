package skanda

const (
	minMatchLength = 2
	lastBytes      = 31
	maxBlockSize   = 262143
	maxDistance    = 1<<31 - 1
	maxStdDistance = 1<<24 - 1

	entropyRaw     = 0
	entropyHuffman = 1
	entropyRLE     = 2

	blockCompressed = 0
	blockRaw        = 1

	blockLast = 1

	streamDistanceAdvanced = 1
	streamLiteralsDelta    = 1
	streamLiteralsPosMask3 = 4

	maxSharedEncoderSourceSize = 16 << 20
	maxSharedDecoderOutputSize = 16 << 20
)

func writeEntropyRaw(dst []byte, flags int, data []byte) []byte {
	dst = writeHeader(dst, len(data), entropyRaw, flags)
	return append(dst, data...)
}

func writeHeader(dst []byte, blockSize, typ, flags int) []byte {
	data := uint32(typ) | uint32(flags<<2) | uint32(blockSize<<6)
	return append(dst, byte(data), byte(data>>8), byte(data>>16))
}

func readHeader(src []byte, pos *int) (blockSize int, typ int, flags int, err error) {
	if *pos+3 > len(src) {
		return 0, 0, 0, ErrCorrupt
	}
	data := uint32(src[*pos]) | uint32(src[*pos+1])<<8 | uint32(src[*pos+2])<<16
	*pos += 3
	typ = int(data & 3)
	flags = int((data >> 2) & 0xf)
	blockSize = int(data >> 6)
	return blockSize, typ, flags, nil
}
