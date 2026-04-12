package dsp

import (
	"testing"
)

func TestFindPeaksSinglePeak(t *testing.T) {
	// 10x10 spectrogram, one clear peak at (5, 5)
	spect := make([][]float64, 10)
	for i := range spect {
		spect[i] = make([]float64, 10)
	}
	spect[5][5] = 1.0

	peaks := FindPeaks(spect, 2, 2, 0.1)
	if len(peaks) != 1 {
		t.Fatalf("expected 1 peak, got %d", len(peaks))
	}
	if peaks[0].Frame != 5 || peaks[0].Bin != 5 {
		t.Errorf("expected peak at (5,5), got (%d,%d)", peaks[0].Frame, peaks[0].Bin)
	}
}

func TestFindPeaksRespectsThreshold(t *testing.T) {
	spect := make([][]float64, 5)
	for i := range spect {
		spect[i] = make([]float64, 5)
	}
	spect[2][2] = 0.05 // below threshold

	peaks := FindPeaks(spect, 1, 1, 0.1)
	if len(peaks) != 0 {
		t.Errorf("expected no peaks below threshold, got %d", len(peaks))
	}
}

func TestFindPeaksMultipleLocalMaxima(t *testing.T) {
	spect := make([][]float64, 20)
	for i := range spect {
		spect[i] = make([]float64, 20)
	}
	// Two peaks far enough apart that neither suppresses the other
	spect[3][5] = 1.0
	spect[15][15] = 0.8

	peaks := FindPeaks(spect, 2, 2, 0.1)
	if len(peaks) != 2 {
		t.Fatalf("expected 2 peaks, got %d: %v", len(peaks), peaks)
	}
}

func TestFindPeaksSuppressesNearbyLowerValue(t *testing.T) {
	spect := make([][]float64, 10)
	for i := range spect {
		spect[i] = make([]float64, 10)
	}
	spect[5][5] = 1.0
	spect[5][6] = 0.9 // neighbour, should be suppressed

	peaks := FindPeaks(spect, 2, 2, 0.1)
	if len(peaks) != 1 {
		t.Fatalf("expected 1 peak (neighbor suppressed), got %d: %v", len(peaks), peaks)
	}
}

func TestFindPeaksPlateauTieBreaking(t *testing.T) {
	spect := make([][]float64, 10)
	for i := range spect {
		spect[i] = make([]float64, 10)
	}
	// Plateau: two equal adjacent values. The "strictly greater" rule
	// means neither qualifies as a local max.
	spect[5][5] = 1.0
	spect[5][6] = 1.0

	peaks := FindPeaks(spect, 2, 2, 0.1)
	if len(peaks) != 0 {
		t.Errorf("expected 0 peaks on plateau (strict max), got %d", len(peaks))
	}
}

func TestFindPeaksHandlesEdges(t *testing.T) {
	spect := make([][]float64, 5)
	for i := range spect {
		spect[i] = make([]float64, 5)
	}
	// Peak right at (0, 0), the corner
	spect[0][0] = 1.0

	peaks := FindPeaks(spect, 2, 2, 0.1)
	if len(peaks) != 1 {
		t.Fatalf("expected 1 peak at corner, got %d", len(peaks))
	}
}

func TestFindPeaksOnRealSpectrogram(t *testing.T) {
	// Spectrogram of a pure 440 Hz tone: the 10th-ish bin at every frame
	// should contain a local maximum. Actually a pure sine wave produces
	// the same magnitude at every frame, so "strictly greater than all
	// time neighbours" rejects the tone entirely. This test confirms
	// that behaviour, which is by design — pure steady tones don't
	// produce constellation-style peaks.
	numFrames, numBins := 20, 50
	spect := make([][]float64, numFrames)
	for i := range spect {
		spect[i] = make([]float64, numBins)
		spect[i][10] = 1.0 // same value at every frame
	}
	peaks := FindPeaks(spect, 2, 2, 0.1)
	if len(peaks) != 0 {
		t.Errorf("steady tone should not produce peaks under strict local max, got %d", len(peaks))
	}
}
