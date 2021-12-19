package fingerprint

import (
	"fmt"
	"math"
	"math/cmplx"

	"github.com/DylanMeeus/GoAudio/wave"
	"github.com/goccmack/godsp/ppeaks"
	"github.com/mjibson/go-dsp/fft"
)

// FFT performs Fast Fourier Transform on the given signal.
func FFT(signal []float64) []float64 {
	fft_arr := fft.FFTReal(signal)

	var out []float64
	for i := 0; i < (len(fft_arr)/2)+1; i++ {
		num := 2.0 * cmplx.Abs(fft_arr[i]) / float64(len(fft_arr))
		out = append(out, num)
	}
	return out
}

// Spectrogram returns the spectrogram of the input signal.
func Spectrogram(signal []float64, winSize int, hopLength int) [][]float64 {
	normalize(signal)
	var res [][]float64
	for i := 0; i < len(signal)-winSize; i += hopLength {
		res = append(res, FFT(signal[i:i+winSize]))
	}
	return res
}

// GetHighestMatchRate returns the highest match rate between the fingerprint of the sample and the fingerprint of the target audio.
func GetHighestMatchRate(sampleFilename string, sampleFingerprint [][]int, audioFilename string, audioFingerprint [][]int) float64 {
	fmt.Printf("Comparing %s to %s\n", sampleFilename, audioFilename)
	highestMatchRate := -1.0
	if len(sampleFingerprint) > len(audioFingerprint) { // sample is shorter than actual audio file
		return highestMatchRate
	}
	for i := 0; i <= len(audioFingerprint)-len(sampleFingerprint); i++ { // shift the sample fingerprint down the target audio's fingerprint
		matchRate := Compare(sampleFingerprint, audioFingerprint[i:i+len(sampleFingerprint)]) // get the match rate at this shift
		if matchRate > highestMatchRate {
			highestMatchRate = matchRate // update highest match rate
		}
	}
	return highestMatchRate
}

// Compare compares two 2-D slice and return the match rate a.k.a the number of matching elements over the total number of elements.
func Compare(sourceFP [][]int, destFP [][]int) float64 {
	diffPositions := 0.0
	for i := range sourceFP {
		for j := range sourceFP[i] {
			diffPositions += math.Abs(float64(sourceFP[i][j] - destFP[i][j])) // Add the absolute different between two elements
		}
	}
	totalPosition := float64(len(sourceFP) * len(sourceFP[0])) // number of elements in the 2-D array
	matchRate := 1 - (diffPositions / totalPosition)           // 1 - non-match rate
	return matchRate
}

// ReadWAV reads .wav file into slice of float64 samples.
func ReadWAV(filename string) []float64 {
	w, err := wave.ReadWaveFile(filename)
	if err != nil {
		panic(err)
	}
	samples := make([]float64, 0)
	for _, f := range w.Frames {
		samples = append(samples, float64(f))
	}
	return samples
}

// Fingerprint takes a .wav file and output its fingerprint.
func Fingerprint(filename string) [][]int {
	fmt.Printf("Fingerprinting %s\n", filename)
	signal := ReadWAV(filename)
	spect := Spectrogram(signal, 2048, 512)
	var res [][]int
	for _, s := range spect {
		p := ppeaks.GetPeaks(s)
		minPer, _ := p.MinMaxPersistence()
		newSig := convertToBinary(s, p.GetIndices(minPer))
		res = append(res, newSig)
	}
	return res
}
