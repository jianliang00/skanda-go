package skanda

import "math/bits"

var windowLogs = [...]int{31, 24, 24, 23, 23, 22, 22, 21, 21, 20, 20}

// EstimateMemory estimates the scratch memory needed for a compression run.
func EstimateMemory(size int, level int, decSpeedBias float64) int {
	if size <= lastBytes+1 {
		return 0
	}
	opts := normalizeCompressorLevelOptions(compressorLevelOptionsForSize(level, decSpeedBias, size))
	intSize := bits.UintSize / 8
	blockSize := min(size, maxBlockSize)
	memory := blockSize*3 + 4096

	switch opts.parser {
	case compressorParserUltraFast, compressorParserGreedy:
		memory += (1 << effectiveLevelHashLog(opts)) * intSize
	case compressorParserLazyFast:
		memory += 2 * (1 << effectiveLevelHashLog(opts)) * intSize
	case compressorParserLazy, compressorParserOptimal1:
		entries := 1 << cacheFinderEntriesLog(opts)
		tableSize := (1 << effectiveLevelHashLog(opts)) * entries * intSize
		memory += 2 * tableSize
		if opts.parser == compressorParserOptimal1 {
			memory += (1 << 14) * intSize
		}
	case compressorParserOptimal2, compressorParserOptimal3:
		memory += binaryMatchFinderMemory(size, opts)
		if useBufferedMatches(opts) {
			maxPerPos := min((1<<opts.hashEntriesLog)+1, 24)
			memory += blockSize * (maxPerPos*2*intSize + intSize)
		}
	}
	if opts.parser == compressorParserOptimal1 || opts.parser == compressorParserOptimal2 || opts.parser == compressorParserOptimal3 {
		memory += blockSplitterMemory(opts)
		arrivals := max(opts.maxArrivals, 1)
		memory += (opts.optimalBlockSize + opts.niceLength + 1) * arrivals * 64
	}
	return memory
}

func binaryMatchFinderMemory(blockSize int, opts compressorLevelOptions) int {
	const int32Size = 4
	binaryTreeWindow := min(opts.hashLog, opts.windowLog)
	nodeListSize := blockSize
	if binaryTreeWindow < bits.UintSize-1 {
		windowSize := 1 << binaryTreeWindow
		if blockSize >= windowSize {
			nodeListSize = windowSize
		}
	}
	if nodeListSize < 1 {
		nodeListSize = 1
	}

	chain3HashLog := min(14, max(opts.windowLog-3, 1))
	chain3EntriesLog := 0
	if opts.parser == compressorParserOptimal3 {
		chain3HashLog = min(16, max(opts.windowLog-3, 1))
		chain3EntriesLog = max(opts.hashEntriesLog-4, 0)
	}
	nodeLookupHashLog := min(20, max(opts.windowLog-3, 1))
	return nodeListSize*2*int32Size + (1<<nodeLookupHashLog)*int32Size + (1<<chain3HashLog)*(1<<chain3EntriesLog)*int32Size
}

func skandaWindowLog(size int, decSpeedBias float64) int {
	if size <= 1 {
		return 6
	}
	if decSpeedBias < 0 {
		decSpeedBias = 0
	}
	if decSpeedBias > 1 {
		decSpeedBias = 1
	}
	index := int(decSpeedBias * 10)
	if index < 0 {
		index = 0
	}
	if index >= len(windowLogs) {
		index = len(windowLogs) - 1
	}
	windowLog := bits.Len(uint(size - 1))
	if windowLog > windowLogs[index] {
		windowLog = windowLogs[index]
	}
	if windowLog < 6 {
		windowLog = 6
	}
	return windowLog
}
