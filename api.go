package skanda

import "errors"

var (
	// ErrCorrupt reports malformed input or an output-size mismatch.
	ErrCorrupt = errors.New("skanda: corrupt input")
	// ErrUnsupportedEntropy reports a recognized stream that uses an unsupported entropy mode.
	ErrUnsupportedEntropy = errors.New("skanda: unsupported entropy stream")
	// ErrInterrupted reports compression stopped because a progress callback returned true.
	ErrInterrupted = errors.New("skanda: interrupted")
)

// ProgressFunc observes compression progress.
//
// processedBytes is the number of source bytes consumed, and compressedBytes
// is the number of bytes appended to the destination. Returning true stops
// compression early and causes Encode or Compress to return ErrInterrupted.
type ProgressFunc func(processedBytes, compressedBytes int) bool

// Options contains compression settings.
type Options struct {
	// Level follows Skanda v1.0's public range and is clamped to 0..10.
	Level int
	// DecSpeedBias trades compression ratio for decoder speed and is clamped to 0..1.
	DecSpeedBias float64
	// Progress observes compression progress and can interrupt long encodes.
	Progress ProgressFunc
}

// Option configures compression.
type Option func(*Options)

// Encoder reuses compression scratch memory across calls.
//
// An Encoder is not safe for concurrent use. Call Close when the encoder is no
// longer needed to release pooled scratch buffers.
type Encoder struct {
	state             compressState
	levelOptions      compressorLevelOptions
	levelOptionsValid bool
	sourceSize        int
	matchState        *optimalMatchState
	splitter          *blockSplitter
}

// Decoder reuses decompression scratch memory across calls.
//
// A Decoder is not safe for concurrent use. Call Close when the decoder is no
// longer needed to release pooled scratch buffers.
type Decoder struct {
	state decodeState
}

// WithLevel sets the compression level. Values outside 0..10 are clamped.
func WithLevel(level int) Option {
	return func(o *Options) {
		o.Level = level
	}
}

// WithDecSpeedBias sets the decoder-speed bias. Values outside 0..1 are clamped.
func WithDecSpeedBias(decSpeedBias float64) Option {
	return func(o *Options) {
		o.DecSpeedBias = decSpeedBias
	}
}

// WithProgress installs a compression progress callback.
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

// CompressBound returns a conservative upper bound for compressed output size.
func CompressBound(size int) int {
	if size < 0 {
		return 0
	}
	return size + size/1024 + 128
}

// IsUnsupported reports whether err indicates an unsupported encoded feature.
func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedEntropy)
}
