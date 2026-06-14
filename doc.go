// Package skanda implements the Skanda v1.0 block compression format in pure Go.
//
// This module is a Go rewrite of the original C++ Calorado/Skanda
// implementation. The compatibility target is the Skanda v1.0 stream format:
// Go-compressed streams should decode with Skanda v1.0 decoders, and
// Skanda v1.0 streams produced by the C++ implementation should decode with
// this package.
//
// Compressed output is not required to be byte-for-byte identical to the C++
// encoder output. Different match choices and entropy-mode choices may produce
// different valid streams for the same input.
package skanda
