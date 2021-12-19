package fingerprint

import (
	"math/rand"

	"github.com/DylanMeeus/GoAudio/wave"
)

type Signal struct {
	SampleRate int
	Samples    []float64
}

// ReadWAV reads .wav file into slice of float64 samples.
func ReadWAV(filename string) *Signal {
	w, err := wave.ReadWaveFile(filename)
	if err != nil {
		panic(err)
	}
	sig := &Signal{}
	sig.SampleRate = w.SampleRate
	sig.Samples = make([]float64, 0)
	for _, f := range w.Frames {
		sig.Samples = append(sig.Samples, float64(f))
	}
	return sig
}

func AddNoise(signal []float64) {
	for i := range signal {
		signal[i] = signal[i] + getRandFloat(-1, 1)
	}
}

func getRandFloat(min, max float64) float64 {
	return min + rand.Float64()*(max-min)

}

func convertToBinary(signal []float64, peaks []int) []int {
	res := make([]int, len(signal))
	for i := range signal {
		if isInSlice(i, peaks) {
			res[i] = 1
		} else {
			res[i] = 0
		}
	}
	return res
}

func isInSlice(elem int, slice []int) bool {
	for _, i := range slice {
		if elem == i {
			return true
		}
	}
	return false
}

func max(ar []float64) float64 {
	max := ar[0]
	for _, v := range ar {
		if v > max {
			max = v
		}
	}
	return max
}

func normalize(signal []float64) {
	max := max(signal)
	for i := range signal {
		signal[i] = signal[i] / max
	}
}
