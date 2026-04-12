// Package subband implements the Haitsma-Kalker mel-band sub-band
// energy fingerprinting algorithm (ISMIR 2002). It registers itself
// with the [fingerprint] package at init time.
package subband

import (
	"math"
	"unsafe"

	"github.com/nddq/presto/internal/audio"
	"github.com/nddq/presto/internal/dsp"
	"github.com/nddq/presto/internal/fingerprint"
)

func init() { fingerprint.Register(&Strategy{}) }

// Strategy implements [fingerprint.Strategy] for sub-band fingerprinting.
type Strategy struct{}

func (*Strategy) Name() string { return "subband" }

func (*Strategy) FingerprintSignal(
	signal *audio.Signal, winSize, hopSize int,
	windowFunc string, noiseScale float64,
) (*fingerprint.FP, error) {
	if signal == nil || len(signal.Samples) < winSize {
		return nil, fingerprint.ErrSignalTooShort
	}
	if noiseScale > 0 {
		dsp.AddNoise(signal.Samples, noiseScale)
	}
	spect := dsp.Spectrogram(signal.Samples, signal.SampleRate, winSize, hopSize, windowFunc)
	if len(spect) == 0 {
		return nil, fingerprint.ErrSignalTooShort
	}

	stride := dsp.NumMelBands - 1
	numFrames := len(spect)
	data := make([]byte, numFrames*stride)
	bands := make([]float64, dsp.NumMelBands)

	for f, frame := range spect {
		dsp.MelBandsInto(bands, frame, winSize, signal.SampleRate, dsp.NumMelBands)
		dsp.SubBandFPInto(data[f*stride:(f+1)*stride], bands)
	}

	return &fingerprint.FP{
		Data:   data,
		Stride: stride,
		Frames: numFrames,
	}, nil
}

func (*Strategy) Similarity(sampleFP, targetFP *fingerprint.FP) float64 {
	return getHighestMatchRate(sampleFP, targetFP)
}

// --- Sub-band comparison ---

// Compare computes bit agreement between two equal-length sub-band
// regions using unsafe uint64 XOR. Returns 0..1 scaled from random=0.5.
func Compare(a, b []byte) float64 {
	n := len(a)
	if n == 0 {
		return 0
	}
	mismatches := xorCount(a, b)
	raw := float64(n-mismatches) / float64(n)
	return math.Max(0, (raw-0.5)*2.0)
}

func xorCount(a, b []byte) int {
	n := len(a)
	mismatches := 0
	chunks := n / 8
	if chunks > 0 {
		ap := unsafe.Pointer(unsafe.SliceData(a))
		bp := unsafe.Pointer(unsafe.SliceData(b))
		for i := range chunks {
			off := uintptr(i * 8)
			xor := *(*uint64)(unsafe.Add(ap, off)) ^ *(*uint64)(unsafe.Add(bp, off))
			mismatches += int((xor * 0x0101010101010101) >> 56)
		}
	}
	for i := chunks * 8; i < n; i++ {
		if a[i] != b[i] {
			mismatches++
		}
	}
	return mismatches
}

func getHighestMatchRate(sampleFP, targetFP *fingerprint.FP) float64 {
	if sampleFP == nil || targetFP == nil {
		return 0
	}
	if sampleFP.Frames > targetFP.Frames || sampleFP.Stride != targetFP.Stride {
		return -1.0
	}
	stride := sampleFP.Stride
	sampleBytes := sampleFP.Frames * stride
	best := -1.0
	for i := 0; i <= targetFP.Frames-sampleFP.Frames; i++ {
		off := i * stride
		rate := Compare(sampleFP.Data[:sampleBytes], targetFP.Data[off:off+sampleBytes])
		if rate > best {
			best = rate
		}
	}
	return best
}
