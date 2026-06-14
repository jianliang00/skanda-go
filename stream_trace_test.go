//go:build skandatrace

package skanda

import (
	"encoding/binary"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"
	"testing"
)

var (
	traceInput       = flag.String("skanda_trace_input", "", "compressed stream to trace")
	traceDecodedSize = flag.Int("skanda_trace_decoded_size", -1, "expected decoded size")
	traceLabel       = flag.String("skanda_trace_label", "stream", "label written to the trace output")
)

type entropyTrace struct {
	mode         int
	flags        int
	symbolCount  int
	encodedStart int
	encodedEnd   int
}

func TestTraceCompressedStream(t *testing.T) {
	if *traceInput == "" {
		t.Skip("missing -skanda_trace_input")
	}
	if *traceDecodedSize < 0 {
		t.Skip("missing -skanda_trace_decoded_size")
	}
	src, err := os.ReadFile(*traceInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeStreamTrace(os.Stdout, *traceLabel, src, *traceDecodedSize); err != nil {
		t.Fatal(err)
	}
}

func writeStreamTrace(output *os.File, label string, src []byte, decodedSize int) error {
	dst := make([]byte, decodedSize)
	writer := csv.NewWriter(output)
	defer writer.Flush()
	if err := writer.Write([]string{
		"label",
		"block_index",
		"decoded_start",
		"decoded_size",
		"block_type",
		"block_flags",
		"compressed_start",
		"compressed_end",
		"compressed_size",
		"first_byte_seed",
		"literal_flags",
		"literal_streams",
		"literal_symbols",
		"literal_encoded_bytes",
		"token_symbols",
		"token_encoded_bytes",
		"distance_flags",
		"distance_token_symbols",
		"distance_token_encoded_bytes",
		"distance_advanced_payload_bytes",
		"distance_decoded_bytes",
		"length_symbols",
		"length_encoded_bytes",
	}); err != nil {
		return err
	}

	cpos := 0
	dpos := 0
	blockIndex := 0
	repOffsets := [7]int{1, 1, 1, 1, 1, 1, 1}
	for {
		compressedStart := cpos
		blockSize, blockType, blockFlags, err := readHeader(src, &cpos)
		if err != nil {
			return fmt.Errorf("block %d header at compressed offset %d: %w", blockIndex, compressedStart, err)
		}
		decodedStart := dpos
		switch blockType {
		case blockRaw:
			if dpos+blockSize > len(dst) || cpos+blockSize > len(src) {
				return ErrCorrupt
			}
			copy(dst[dpos:dpos+blockSize], src[cpos:cpos+blockSize])
			cpos += blockSize
			dpos += blockSize
			if err := writer.Write([]string{
				label,
				strconv.Itoa(blockIndex),
				strconv.Itoa(decodedStart),
				strconv.Itoa(blockSize),
				"raw",
				strconv.Itoa(blockFlags),
				strconv.Itoa(compressedStart),
				strconv.Itoa(cpos),
				strconv.Itoa(cpos - compressedStart),
				"false",
				"",
				"",
				"",
				"",
				"",
				"",
				"",
				"",
				"",
				"",
				"",
				"",
				"",
			}); err != nil {
				return err
			}
			blockIndex++
			if blockFlags&blockLast != 0 {
				if dpos != len(dst) {
					return ErrCorrupt
				}
				return nil
			}
		case blockCompressed:
			if blockFlags&blockLast != 0 {
				return ErrCorrupt
			}
			blockEnd := dpos + blockSize
			if blockEnd > len(dst) {
				return ErrCorrupt
			}
			if len(dst) >= lastBytes && blockEnd > len(dst)-lastBytes {
				return ErrCorrupt
			}
			firstByteSeed := false
			if dpos == 0 {
				if cpos >= len(src) || blockSize == 0 {
					return ErrCorrupt
				}
				dst[dpos] = src[cpos]
				dpos++
				cpos++
				firstByteSeed = true
			}

			literalTrace, literals, literalFlags, err := traceEntropy(src, &cpos)
			if err != nil {
				return fmt.Errorf("block %d literal entropy at compressed offset %d: %w", blockIndex, cpos, err)
			}
			literalTraces := []entropyTrace{literalTrace}
			var literalStreams [4][]byte
			literalStreams[0] = literals
			literalStreamCount := 1
			if literalFlags&streamLiteralsPosMask3 != 0 {
				literalStreamCount = 4
				for i := 1; i < 4; i++ {
					streamTrace, stream, _, err := traceEntropy(src, &cpos)
					if err != nil {
						return fmt.Errorf("block %d literal substream %d at compressed offset %d: %w", blockIndex, i, cpos, err)
					}
					literalTraces = append(literalTraces, streamTrace)
					literalStreams[i] = stream
				}
			}

			tokenTrace, tokens, _, err := traceEntropy(src, &cpos)
			if err != nil {
				return fmt.Errorf("block %d token entropy at compressed offset %d: %w", blockIndex, cpos, err)
			}
			distanceTrace, distanceTokens, distanceFlags, err := traceEntropy(src, &cpos)
			if err != nil {
				return fmt.Errorf("block %d distance entropy at compressed offset %d: %w", blockIndex, cpos, err)
			}
			distances := distanceTokens
			var advancedDistances []uint32
			advancedPayloadBytes := 0
			if distanceFlags&streamDistanceAdvanced != 0 {
				advancedStart := cpos
				advancedDistances, err = decodeAdvancedDistancesWithState(src, &cpos, distanceTokens, nil)
				if err != nil {
					return fmt.Errorf("block %d advanced distance payload at compressed offset %d: %w", blockIndex, cpos, err)
				}
				advancedPayloadBytes = cpos - advancedStart
			}
			lengthTrace, lengths, _, err := traceEntropy(src, &cpos)
			if err != nil {
				return fmt.Errorf("block %d length entropy at compressed offset %d: %w", blockIndex, cpos, err)
			}
			if err := decodeLZBlock(dst, &dpos, blockEnd, &literalStreams, literalStreamCount, tokens, distances, advancedDistances, lengths, literalFlags, distanceFlags, &repOffsets); err != nil {
				return fmt.Errorf("block %d lz decode decoded_start=%d compressed_start=%d: %w", blockIndex, decodedStart, compressedStart, err)
			}
			dpos = blockEnd
			if err := writer.Write([]string{
				label,
				strconv.Itoa(blockIndex),
				strconv.Itoa(decodedStart),
				strconv.Itoa(blockSize),
				"compressed",
				strconv.Itoa(blockFlags),
				strconv.Itoa(compressedStart),
				strconv.Itoa(cpos),
				strconv.Itoa(cpos - compressedStart),
				strconv.FormatBool(firstByteSeed),
				strconv.Itoa(literalFlags),
				strconv.Itoa(len(literalTraces)),
				strconv.Itoa(totalEntropySymbols(literalTraces)),
				strconv.Itoa(totalEntropyEncodedBytes(literalTraces)),
				strconv.Itoa(tokenTrace.symbolCount),
				strconv.Itoa(tokenTrace.encodedEnd - tokenTrace.encodedStart),
				strconv.Itoa(distanceFlags),
				strconv.Itoa(distanceTrace.symbolCount),
				strconv.Itoa(distanceTrace.encodedEnd - distanceTrace.encodedStart),
				strconv.Itoa(advancedPayloadBytes),
				strconv.Itoa(len(distances)),
				strconv.Itoa(lengthTrace.symbolCount),
				strconv.Itoa(lengthTrace.encodedEnd - lengthTrace.encodedStart),
			}); err != nil {
				return err
			}
			blockIndex++
		default:
			return ErrCorrupt
		}
	}
}

func traceEntropy(src []byte, cpos *int) (entropyTrace, []byte, int, error) {
	encodedStart := *cpos
	symbolCount, mode, flags, err := readHeader(src, cpos)
	if err != nil {
		return entropyTrace{}, nil, 0, err
	}
	switch mode {
	case entropyRaw:
		if *cpos+symbolCount > len(src) {
			return entropyTrace{}, nil, 0, ErrCorrupt
		}
		stream := src[*cpos : *cpos+symbolCount]
		*cpos += symbolCount
		return entropyTrace{mode: mode, flags: flags, symbolCount: symbolCount, encodedStart: encodedStart, encodedEnd: *cpos}, stream, flags, nil
	case entropyRLE:
		if *cpos >= len(src) {
			return entropyTrace{}, nil, 0, ErrCorrupt
		}
		stream := make([]byte, symbolCount)
		for i := range stream {
			stream[i] = src[*cpos]
		}
		(*cpos)++
		return entropyTrace{mode: mode, flags: flags, symbolCount: symbolCount, encodedStart: encodedStart, encodedEnd: *cpos}, stream, flags, nil
	case entropyHuffman:
		stream, err := decodeHuffmanEntropyForTrace(src, cpos, symbolCount)
		if err != nil {
			return entropyTrace{}, nil, 0, err
		}
		return entropyTrace{mode: mode, flags: flags, symbolCount: symbolCount, encodedStart: encodedStart, encodedEnd: *cpos}, stream, flags, nil
	default:
		return entropyTrace{}, nil, 0, fmt.Errorf("%w: entropy mode %d", ErrCorrupt, mode)
	}
}

func decodeHuffmanEntropyForTrace(src []byte, cpos *int, symbolCount int) ([]byte, error) {
	if *cpos >= len(src) {
		return nil, ErrCorrupt
	}
	headerSizePos := *cpos
	headerSize := int(src[*cpos] & 0x7f)
	*cpos += 1
	headerStart := *cpos
	headerEnd := headerStart + headerSize
	if headerSize < 4 || headerEnd > len(src) {
		return nil, fmt.Errorf("%w: huffman header size=%d header_start=%d", ErrCorrupt, headerSize, headerStart)
	}

	table, streamSizes, err := decodeHuffmanHeaderForTrace(src, headerStart, headerEnd)
	if err != nil {
		return nil, fmt.Errorf("%w: huffman header size_pos=%d size=%d start=%d end=%d stream_sizes=%v", err, headerSizePos, headerSize, headerStart, headerEnd, streamSizes)
	}
	*cpos = headerEnd

	totalStreamSize := 0
	for _, size := range streamSizes {
		totalStreamSize += size
	}
	streamStart := *cpos
	streamEnd := streamStart + totalStreamSize
	if streamEnd > len(src) {
		return nil, fmt.Errorf("%w: huffman streams sizes=%v start=%d end=%d input=%d", ErrCorrupt, streamSizes, streamStart, streamEnd, len(src))
	}

	out := make([]byte, symbolCount+30)
	if err := decodeHuffmanSymbols(src, streamStart, streamSizes, symbolCount, &table, out); err != nil {
		return nil, fmt.Errorf("%w: huffman symbols count=%d sizes=%v start=%d end=%d", err, symbolCount, streamSizes, streamStart, streamEnd)
	}
	*cpos = streamEnd
	return out[:symbolCount], nil
}

func decodeHuffmanHeaderForTrace(src []byte, start, end int) ([huffmanCodeSpace]huffmanEntry, [6]int, error) {
	var table [huffmanCodeSpace]huffmanEntry
	var streamSizes [6]int
	var decodedSymbols [maxHuffmanCodeLength + 2][256]byte
	var decodedCounts [maxHuffmanCodeLength + 2]int

	huffIt := end - 4
	if huffIt < start || huffIt+4 > len(src) {
		return table, streamSizes, fmt.Errorf("%w: huffman header initial word start=%d end=%d", ErrCorrupt, start, end)
	}
	huffState := binary.LittleEndian.Uint32(src[huffIt:])
	bitCount := uint(32)

	extract := func(n uint) uint32 {
		result := huffState >> (32 - n)
		huffState <<= n
		bitCount -= n
		return result
	}
	renormalize := func(phase string) error {
		if huffIt > start {
			if huffIt-4 < 0 {
				return fmt.Errorf("%w: huffman header %s refill before input huff_it=%d start=%d", ErrCorrupt, phase, huffIt, start)
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
	if err := renormalize("sizes"); err != nil {
		return table, streamSizes, err
	}
	for i := 1; i < 6; i++ {
		foldedError := int(extract(errorBits))
		unfoldedError := foldedError >> 1
		if foldedError&1 != 0 {
			unfoldedError = -unfoldedError - 1
		}
		streamSizes[i] = (streamSizes[0] + unfoldedError) & 0xffff
		if err := renormalize(fmt.Sprintf("size%d", i)); err != nil {
			return table, streamSizes, err
		}
	}

	hskip := int(extract(3)) + 1
	usedCodeSpace := 0
	for i := hskip; i <= maxHuffmanCodeLength && usedCodeSpace < precodeCodeSpace; i++ {
		codeBits := int(extract(3)) + 1
		decodedSymbols[codeBits][decodedCounts[codeBits]] = byte(i)
		decodedCounts[codeBits]++
		usedCodeSpace += precodeCodeSpace >> codeBits
		if err := renormalize(fmt.Sprintf("precode-len%d", i)); err != nil {
			return table, streamSizes, err
		}
	}
	if usedCodeSpace < precodeCodeSpace {
		if usedCodeSpace < precodeCodeSpace/2 {
			return table, streamSizes, fmt.Errorf("%w: precode used code space=%d hskip=%d counts=%v", ErrCorrupt, usedCodeSpace, hskip, decodedCounts)
		}
		codeBits := maxPrecodeCodeLength - log2(precodeCodeSpace-usedCodeSpace)
		decodedSymbols[codeBits][decodedCounts[codeBits]] = maxHuffmanCodeLength + 1
		decodedCounts[codeBits]++
		usedCodeSpace += precodeCodeSpace >> codeBits
	}
	if usedCodeSpace != precodeCodeSpace {
		return table, streamSizes, fmt.Errorf("%w: precode final used code space=%d hskip=%d counts=%v", ErrCorrupt, usedCodeSpace, hskip, decodedCounts)
	}

	precodeTable, err := buildPrecodeDecodeTable(&decodedSymbols, &decodedCounts)
	if err != nil {
		return table, streamSizes, fmt.Errorf("%w: build precode table counts=%v", err, decodedCounts)
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
			return fmt.Errorf("%w: precode lookup value=%d read_symbols=%d used_code_space=%d huff_it=%d bit_count=%d", ErrCorrupt, value, readSymbols, usedCodeSpace, huffIt, bitCount)
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
			return fmt.Errorf("%w: symbol run too long symbol=%d loop_end=%d read_symbols=%d", ErrCorrupt, newSymbol, loopEnd, readSymbols)
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
			return table, streamSizes, err
		}
		if !(readSymbols < 255 && usedCodeSpace != huffmanCodeSpace) {
			break
		}
		if err := precodeOp(); err != nil {
			return table, streamSizes, err
		}
		if !(readSymbols < 255 && usedCodeSpace != huffmanCodeSpace) {
			break
		}
		if err := precodeOp(); err != nil {
			return table, streamSizes, err
		}
		if err := renormalize("symbols"); err != nil {
			return table, streamSizes, err
		}
	}

	if usedCodeSpace < huffmanCodeSpace {
		if usedCodeSpace < huffmanCodeSpace/2 {
			return table, streamSizes, fmt.Errorf("%w: huffman used code space=%d read_symbols=%d counts=%v", ErrCorrupt, usedCodeSpace, readSymbols, decodedCounts)
		}
		codeBits := maxHuffmanCodeLength - log2(huffmanCodeSpace-usedCodeSpace)
		decodedSymbols[codeBits][decodedCounts[codeBits]] = 255
		decodedCounts[codeBits]++
		usedCodeSpace += huffmanCodeSpace >> codeBits
	}
	if usedCodeSpace != huffmanCodeSpace {
		return table, streamSizes, fmt.Errorf("%w: huffman final used code space=%d read_symbols=%d counts=%v", ErrCorrupt, usedCodeSpace, readSymbols, decodedCounts)
	}

	if err := buildDecodeTable(&table, &decodedSymbols, &decodedCounts, maxHuffmanCodeLength); err != nil {
		return table, streamSizes, fmt.Errorf("%w: build huffman table counts=%v", err, decodedCounts)
	}
	return table, streamSizes, nil
}

func totalEntropySymbols(traces []entropyTrace) int {
	total := 0
	for _, trace := range traces {
		total += trace.symbolCount
	}
	return total
}

func totalEntropyEncodedBytes(traces []entropyTrace) int {
	total := 0
	for _, trace := range traces {
		total += trace.encodedEnd - trace.encodedStart
	}
	return total
}
