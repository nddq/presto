package dsp

import "math"

// NumMelBands is the number of mel-frequency bands used by the sub-band
// fingerprinting strategy.
const NumMelBands = 40

type melEdges struct {
	fftSize, sampleRate, numBands int
	edges                        []int
}

var cachedEdges *melEdges

func getMelBandEdges(fftSize, sampleRate, numBands int) []int {
	if c := cachedEdges; c != nil && c.fftSize == fftSize && c.sampleRate == sampleRate && c.numBands == numBands {
		return c.edges
	}
	maxMel := hzToMel(float64(sampleRate) / 2.0)
	numBins := fftSize/2 + 1

	edges := make([]int, numBands+2)
	for i := range edges {
		hz := melToHz(maxMel * float64(i) / float64(numBands+1))
		bin := int(math.Round(hz * float64(fftSize) / float64(sampleRate)))
		if bin >= numBins {
			bin = numBins - 1
		}
		edges[i] = bin
	}
	cachedEdges = &melEdges{fftSize, sampleRate, numBands, edges}
	return edges
}

func hzToMel(hz float64) float64  { return 2595.0 * math.Log10(1.0+hz/700.0) }
func melToHz(mel float64) float64 { return 700.0 * (math.Pow(10.0, mel/2595.0) - 1.0) }

// MelBandsInto groups an FFT magnitude spectrum into mel-frequency bands
// and writes the mean energy of each band into dst (length numBands).
func MelBandsInto(dst, spectrum []float64, fftSize, sampleRate, numBands int) {
	edges := getMelBandEdges(fftSize, sampleRate, numBands)
	specLen := len(spectrum)
	for b := range numBands {
		lo := edges[b]
		hi := edges[b+1]
		if hi <= lo {
			hi = lo + 1
		}
		sum := 0.0
		count := 0
		for i := lo; i < hi && i < specLen; i++ {
			v := spectrum[i]
			sum += v * v
			count++
		}
		if count > 0 {
			dst[b] = sum / float64(count)
		} else {
			dst[b] = 0
		}
	}
}

// SubBandFPInto compares energy between adjacent mel bands and writes the
// result as binary values into dst: 1 if bands[i] > bands[i+1], else 0.
// dst must have length >= len(bands)-1.
func SubBandFPInto(dst []byte, bands []float64) {
	for i := range dst {
		if bands[i] > bands[i+1] {
			dst[i] = 1
		} else {
			dst[i] = 0
		}
	}
}
