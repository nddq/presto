// Package dsp provides digital signal processing primitives for audio
// fingerprinting, including FFT, window functions, spectrograms, and
// mel-frequency analysis.
package dsp

import (
	"math"
	"math/cmplx"
	"sync"
)

// fftPlan holds immutable, pre-computed data for a given FFT size. Plans
// are cached and shared across goroutines, so every field must be read-
// only after construction. The scratch buffer used during a transform
// is supplied by the caller (see [FFTMagnitude]) rather than living on
// the plan — sharing a mutable buffer across concurrent callers would
// corrupt the output.
type fftPlan struct {
	n        int
	log2n    int
	twiddles []complex128
	bitrev   []int
}

var (
	planCache   = map[int]*fftPlan{}
	planCacheMu sync.Mutex
)

func getFFTPlan(n int) *fftPlan {
	planCacheMu.Lock()
	defer planCacheMu.Unlock()
	if p, ok := planCache[n]; ok {
		return p
	}
	p := newFFTPlan(n)
	planCache[n] = p
	return p
}

func newFFTPlan(n int) *fftPlan {
	log2n := 0
	for m := n; m > 1; m >>= 1 {
		log2n++
	}

	bitrev := make([]int, n)
	for i := range bitrev {
		j := 0
		for b := range log2n {
			if i&(1<<b) != 0 {
				j |= 1 << (log2n - 1 - b)
			}
		}
		bitrev[i] = j
	}

	twiddles := make([]complex128, n/2)
	for k := range twiddles {
		angle := -2.0 * math.Pi * float64(k) / float64(n)
		twiddles[k] = complex(math.Cos(angle), math.Sin(angle))
	}

	return &fftPlan{
		n: n, log2n: log2n,
		twiddles: twiddles, bitrev: bitrev,
	}
}

// transform runs the butterfly stages in-place on buf. buf is supplied
// by the caller so concurrent callers can each work on their own
// scratch area.
func (p *fftPlan) transform(buf []complex128) {
	n := p.n
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := n / size
		for start := 0; start < n; start += size {
			for k := range half {
				idx := start + half + k
				t := p.twiddles[k*step] * buf[idx]
				u := buf[start+k]
				buf[start+k] = u + t
				buf[idx] = u - t
			}
		}
	}
}

// FFTMagnitude computes the magnitude spectrum of a real-valued signal
// and writes the positive-frequency magnitudes into dst.
// dst must have length >= len(signal)/2+1.
//
// Allocates a scratch buffer on every call. For bulk use (e.g. frame-by-
// frame inside a spectrogram loop), call [FFTMagnitudeInto] with a
// caller-owned scratch buffer to avoid repeat allocation.
func FFTMagnitude(dst, signal []float64) {
	scratch := make([]complex128, len(signal))
	FFTMagnitudeInto(dst, signal, scratch)
}

// FFTMagnitudeInto is the allocation-free variant of [FFTMagnitude].
// scratch must have length >= len(signal) and is clobbered by the call.
// Each goroutine must use its own scratch buffer.
func FFTMagnitudeInto(dst, signal []float64, scratch []complex128) {
	plan := getFFTPlan(len(signal))

	for i, j := range plan.bitrev {
		scratch[j] = complex(signal[i], 0)
	}
	plan.transform(scratch)

	scale := 2.0 / float64(plan.n)
	for i := range dst {
		dst[i] = cmplx.Abs(scratch[i]) * scale
	}
}

// FFT returns the magnitude spectrum of a real-valued signal.
// The returned slice has length len(signal)/2+1.
func FFT(signal []float64) []float64 {
	out := make([]float64, len(signal)/2+1)
	FFTMagnitude(out, signal)
	return out
}
