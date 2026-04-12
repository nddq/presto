// Package fingerprint provides audio fingerprint generation and direct
// single-target matching. Two algorithm implementations live in
// sub-packages and register themselves via [Register] at init time:
//
//   - [constellation] — peak-pair hashing (Wang 2003, default)
//   - [subband] — Haitsma-Kalker mel-band energy comparison
//
// Callers import the implementations they need as blank imports so the
// init functions fire:
//
//	import _ "github.com/nddq/presto/internal/fingerprint/constellation"
//	import _ "github.com/nddq/presto/internal/fingerprint/subband"
package fingerprint

import (
	"errors"
	"fmt"
	"sync"

	"github.com/nddq/presto/internal/audio"
)

// --- FP: the tagged-union fingerprint type ---

// FP is an audio fingerprint. Depending on which algorithm produced it,
// one of two field groups is populated:
//
//   - Constellation: Hashes + NumFrames
//   - Sub-band: Data + Stride + Frames
type FP struct {
	Hashes    []HashEntry // constellation
	NumFrames int         // constellation

	Data   []byte // sub-band
	Stride int    // sub-band
	Frames int    // sub-band
}

// IsConstellation reports whether this FP was produced by the
// constellation algorithm.
func (fp *FP) IsConstellation() bool { return fp.Hashes != nil }

// HashEntry is one constellation hash and the STFT frame offset in the
// source signal where the anchor peak was located. Defined here (not in
// the constellation sub-package) because the FP struct references it and
// the store package needs the type for serialization.
type HashEntry struct {
	Hash   uint32
	Offset uint32
}

// ErrSignalTooShort is returned when the input signal is nil, empty,
// or shorter than the FFT window size.
var ErrSignalTooShort = errors.New("fingerprint: signal shorter than window size")

// --- Strategy interface and registry ---

// Strategy defines a fingerprinting algorithm. Implementations live in
// sub-packages and call [Register] from init().
type Strategy interface {
	Name() string
	FingerprintSignal(sig *audio.Signal, winSize, hopSize int,
		windowFunc string, noiseScale float64) (*FP, error)
	Similarity(sampleFP, targetFP *FP) float64
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Strategy{}
)

// Register adds a strategy to the global registry.
func Register(s Strategy) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[s.Name()] = s
}

// Get returns the strategy registered under the given name.
func Get(name string) (Strategy, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	s, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown fingerprint strategy %q", name)
	}
	return s, nil
}

// DefaultStrategy is the name used when no --algo flag is given.
const DefaultStrategy = "constellation"

// --- Convenience helpers ---

// ReadWAVForStrategy reads a WAV file into a Signal. Exposed so CLI
// callers don't need to import the audio package directly.
func ReadWAVForStrategy(filename string) (*audio.Signal, error) {
	return audio.ReadWAV(filename)
}


// Fingerprint reads a WAV file and returns its fingerprint using the
// default constellation strategy.
func Fingerprint(filename string, winSize, hopSize int, windowFunction string, noiseScale float64) (*FP, error) {
	sig, err := audio.ReadWAV(filename)
	if err != nil {
		return nil, err
	}
	return FingerprintSignal(sig, winSize, hopSize, windowFunction, noiseScale)
}

// FingerprintSignal computes a fingerprint using the default strategy.
func FingerprintSignal(signal *audio.Signal, winSize, hopSize int, windowFunction string, noiseScale float64) (*FP, error) {
	s, err := Get(DefaultStrategy)
	if err != nil {
		return nil, err
	}
	return s.FingerprintSignal(signal, winSize, hopSize, windowFunction, noiseScale)
}

// Similarity computes direct similarity using the default strategy.
func Similarity(sampleFP, targetFP *FP) float64 {
	s, err := Get(DefaultStrategy)
	if err != nil {
		return 0
	}
	return s.Similarity(sampleFP, targetFP)
}
