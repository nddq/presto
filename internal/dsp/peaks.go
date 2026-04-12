package dsp

// Peak is a local maximum in a time-frequency spectrogram.
// Frame is the time-axis index (STFT frame number) and Bin is the
// frequency-axis index (FFT bin).
type Peak struct {
	Frame int
	Bin   int
}

// FindPeaks returns the local maxima in a magnitude spectrogram. A point
// (t, f) is kept if its magnitude is strictly greater than every other
// point in the (2*timeRadius+1)×(2*freqRadius+1) neighbourhood centred
// on it and above minAmplitude.
//
// Peaks are returned in scan order (frame-major, then bin).
//
// The neighbourhood is the main noise filter: a larger window keeps only
// the most dominant peaks and produces a sparser constellation, while a
// smaller window keeps more peaks at the cost of less robustness.
func FindPeaks(spect [][]float64, timeRadius, freqRadius int, minAmplitude float64) []Peak {
	if len(spect) == 0 {
		return nil
	}
	numFrames := len(spect)
	numBins := len(spect[0])

	var out []Peak
	for t := range numFrames {
		row := spect[t]
		for f := range numBins {
			v := row[f]
			if v <= minAmplitude {
				continue
			}
			if !isLocalMax(spect, t, f, v, numFrames, numBins, timeRadius, freqRadius) {
				continue
			}
			out = append(out, Peak{Frame: t, Bin: f})
		}
	}
	return out
}

// isLocalMax reports whether spect[t][f] == v is strictly greater than
// every other value in the rectangular neighbourhood. Broken out so the
// hot loop can reuse bounds-checked index variables.
func isLocalMax(spect [][]float64, t, f int, v float64, numFrames, numBins, tr, fr int) bool {
	tLo := t - tr
	if tLo < 0 {
		tLo = 0
	}
	tHi := t + tr
	if tHi >= numFrames {
		tHi = numFrames - 1
	}
	fLo := f - fr
	if fLo < 0 {
		fLo = 0
	}
	fHi := f + fr
	if fHi >= numBins {
		fHi = numBins - 1
	}
	for tt := tLo; tt <= tHi; tt++ {
		row := spect[tt]
		for ff := fLo; ff <= fHi; ff++ {
			if tt == t && ff == f {
				continue
			}
			if row[ff] >= v {
				return false
			}
		}
	}
	return true
}
