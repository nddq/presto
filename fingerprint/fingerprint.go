package fingerprint

import (
	"fmt"
	"math"

	"github.com/goccmack/godsp/ppeaks"
)

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

// Fingerprint takes a .wav file and output its fingerprint.
func Fingerprint(filename string, winSize, hopSize int, windowFunction string, addNoise bool) [][]int {
	fmt.Printf("Fingerprinting %s\n", filename)
	signal := ReadWAV(filename)
	if addNoise {
		AddNoise(signal.Samples)
	}
	spect := Spectrogram(signal, winSize, hopSize, windowFunction)
	var res [][]int
	for _, s := range spect {
		p := ppeaks.GetPeaks(s)                            // peak picking function
		minPer, _ := p.MinMaxPersistence()                 // similar to prominent
		newSig := convertToBinary(s, p.GetIndices(minPer)) // convert all peaks to 1s, the rest to 0s
		res = append(res, newSig)
	}
	return res
}
