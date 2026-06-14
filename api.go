package skanda

import "errors"

var (
	ErrCorrupt            = errors.New("skanda: corrupt input")
	ErrUnsupportedEntropy = errors.New("skanda: unsupported entropy stream")
)

type ProgressFunc func(processedBytes, compressedBytes int) bool

type Options struct {
	// Level follows Skanda v1.0's public range and is clamped to 0..10.
	Level        int
	DecSpeedBias float64
	Progress     ProgressFunc
}

type Option func(*Options)

type Encoder struct {
	state             compressState
	levelOptions      compressorLevelOptions
	levelOptionsValid bool
	sourceSize        int
	matchState        *optimalMatchState
	splitter          *blockSplitter
}

type Decoder struct {
	state decodeState
}

func WithLevel(level int) Option {
	return func(o *Options) {
		o.Level = level
	}
}

func WithDecSpeedBias(decSpeedBias float64) Option {
	return func(o *Options) {
		o.DecSpeedBias = decSpeedBias
	}
}

func WithProgress(progress ProgressFunc) Option {
	return func(o *Options) {
		o.Progress = progress
	}
}

func defaultOptions() Options {
	return Options{
		Level:        2,
		DecSpeedBias: 0.5,
	}
}

func normalizeOptions(options []Option) Options {
	opts := defaultOptions()
	for _, option := range options {
		option(&opts)
	}
	if opts.Level < 0 {
		opts.Level = 0
	}
	if opts.Level > 10 {
		opts.Level = 10
	}
	if opts.DecSpeedBias < 0 {
		opts.DecSpeedBias = 0
	}
	if opts.DecSpeedBias > 1 {
		opts.DecSpeedBias = 1
	}
	return opts
}

func CompressBound(size int) int {
	if size < 0 {
		return 0
	}
	return size + size/1024 + 128
}

func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedEntropy)
}
