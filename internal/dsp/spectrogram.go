package dsp

import (
	"math"
	"math/rand/v2"
	"time"
)

// Spectrogram computes the short-time Fourier transform (STFT) magnitude
// spectrogram of a signal. The signal is normalized, divided into overlapping
// frames of winSize samples with hopSize stride, and each frame is optionally
// windowed before computing the FFT magnitude spectrum.
//
// Returns nil if the signal is empty, shorter than winSize, or if winSize or
// hopSize are non-positive. No panic paths.
func Spectrogram(samples []float64, sampleRate, winSize, hopSize int, windowFunction string) [][]float64 {
	if winSize <= 0 || hopSize <= 0 || len(samples) < winSize {
		return nil
	}

	// Work on a normalized copy
	norm := make([]float64, len(samples))
	copy(norm, samples)
	normalize(norm)

	numFrames := (len(norm) - winSize) / hopSize
	if numFrames <= 0 {
		return nil
	}

	fftBins := winSize/2 + 1
	winCoeffs := GetWindow(windowFunction, winSize)
	winBuf := make([]float64, winSize)
	fftBuf := make([]float64, fftBins)
	// Per-call FFT scratch buffer. Each Spectrogram invocation owns its
	// own buffer so concurrent callers (e.g. parallel indexing) don't
	// race on shared state.
	fftScratch := make([]complex128, winSize)

	res := make([][]float64, numFrames)
	for f := range numFrames {
		start := f * hopSize
		copy(winBuf, norm[start:start+winSize])
		if winCoeffs != nil {
			ApplyWindow(winBuf, winCoeffs)
		}
		FFTMagnitudeInto(fftBuf, winBuf, fftScratch)
		frame := make([]float64, fftBins)
		copy(frame, fftBuf)
		res[f] = frame
	}
	return res
}

// AddNoise adds uniform random noise to each sample. The scale parameter
// controls noise amplitude as a fraction of the signal's peak value
// (e.g., 0.1 adds noise at 10% of peak amplitude). Safe on empty input.
func AddNoise(signal []float64, scale float64) {
	if len(signal) == 0 || scale == 0 {
		return
	}
	m := maxAbs(signal)
	// Per-call PRNG: avoids the data race that a package-global seed
	// would create under concurrent fingerprinting.
	rng := rand.New(rand.NewPCG(
		uint64(time.Now().UnixNano()),
		uint64(len(signal))^uint64(scale*1e9),
	))
	for i := range signal {
		signal[i] += (rng.Float64()*2 - 1) * m * scale
	}
}

// maxAbs returns the largest absolute value in ar, or 0 if ar is empty.
func maxAbs(ar []float64) float64 {
	if len(ar) == 0 {
		return 0
	}
	m := math.Abs(ar[0])
	for _, v := range ar {
		if a := math.Abs(v); a > m {
			m = a
		}
	}
	return m
}

// normalize scales signal so its peak absolute value is 1.0. No-op on
// empty or zero-signal input.
func normalize(signal []float64) {
	m := maxAbs(signal)
	if m == 0 {
		return
	}
	for i := range signal {
		signal[i] /= m
	}
}
