package dsp

import (
	"math"
	"sync"
)

var (
	windowCache   = map[string][]float64{}
	windowCacheMu sync.Mutex
)

// GetWindow returns the window function coefficients for the given name and size.
// Supported names are "hann", "hamming", and "bartlett".
// Returns nil if name is empty or unrecognized.
func GetWindow(name string, n int) []float64 {
	if name == "" {
		return nil
	}
	key := name + ":" + itoa(n)
	windowCacheMu.Lock()
	defer windowCacheMu.Unlock()
	if w, ok := windowCache[key]; ok {
		return w
	}
	var w []float64
	switch name {
	case "hann":
		w = hannCoeffs(n)
	case "hamming":
		w = hammingCoeffs(n)
	case "bartlett":
		w = bartlettCoeffs(n)
	default:
		return nil
	}
	windowCache[key] = w
	return w
}

// ApplyWindow multiplies each element of dst by the corresponding window coefficient.
func ApplyWindow(dst, coeffs []float64) {
	for i := range dst {
		dst[i] *= coeffs[i]
	}
}

func hannCoeffs(n int) []float64 {
	w := make([]float64, n)
	f := 2.0 * math.Pi / float64(n-1)
	for i := range w {
		w[i] = 0.5 * (1.0 - math.Cos(f*float64(i)))
	}
	return w
}

func hammingCoeffs(n int) []float64 {
	w := make([]float64, n)
	f := 2.0 * math.Pi / float64(n-1)
	for i := range w {
		w[i] = 0.54 - 0.46*math.Cos(f*float64(i))
	}
	return w
}

func bartlettCoeffs(n int) []float64 {
	w := make([]float64, n)
	d := float64(n - 1)
	for i := range w {
		w[i] = 1.0 - math.Abs(2.0*float64(i)/d-1.0)
	}
	return w
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
