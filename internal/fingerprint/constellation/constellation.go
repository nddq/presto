// Package constellation implements the peak-pair constellation
// fingerprinting algorithm described by Avery Wang (ISMIR 2003).
// It registers itself with the [fingerprint] package at init time.
package constellation

import (
	"github.com/nddq/presto/internal/audio"
	"github.com/nddq/presto/internal/dsp"
	"github.com/nddq/presto/internal/fingerprint"
)

func init() { fingerprint.Register(&Strategy{}) }

// Strategy implements [fingerprint.Strategy] for constellation hashing.
type Strategy struct{}

func (*Strategy) Name() string { return "constellation" }

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
	peaks := dsp.FindPeaks(spect, peakTimeRadius, peakFreqRadius, peakThreshold)
	return &fingerprint.FP{
		Hashes:    GenerateHashes(peaks),
		NumFrames: len(spect),
	}, nil
}

func (*Strategy) Similarity(sampleFP, targetFP *fingerprint.FP) float64 {
	if sampleFP == nil || targetFP == nil || len(sampleFP.Hashes) == 0 {
		return 0
	}
	targetByHash := make(map[uint32][]uint32, len(targetFP.Hashes))
	for _, h := range targetFP.Hashes {
		targetByHash[h.Hash] = append(targetByHash[h.Hash], h.Offset)
	}
	votes := make(map[int32]int)
	seen := make(map[int32]struct{})
	best := 0
	for _, h := range sampleFP.Hashes {
		hits, ok := targetByHash[h.Hash]
		if !ok {
			continue
		}
		clear(seen)
		for _, tOff := range hits {
			delta := int32(tOff) - int32(h.Offset)
			if _, dup := seen[delta]; dup {
				continue
			}
			seen[delta] = struct{}{}
			votes[delta]++
			if votes[delta] > best {
				best = votes[delta]
			}
		}
	}
	score := float64(best) / float64(len(sampleFP.Hashes))
	if score > 1 {
		score = 1
	}
	return score
}

// --- Hash generation ---

const (
	peakTimeRadius = 5
	peakFreqRadius = 12
	peakThreshold  = 0.0

	fanoutSize      = 5
	targetMinFrames = 2
	targetMaxFrames = 60
	targetFreqSpan  = 64

	hashBinBits   = 10
	hashDeltaBits = 12
	hashBinMask   = (1 << hashBinBits) - 1
	hashDeltaMask = (1 << hashDeltaBits) - 1
)

func encodeHash(f1, f2, dt int) uint32 {
	return uint32(f1)&hashBinMask |
		(uint32(f2)&hashBinMask)<<hashBinBits |
		(uint32(dt)&hashDeltaMask)<<(2*hashBinBits)
}

// GenerateHashes produces constellation peak-pair hashes from a peak
// list in (frame, bin) scan order.
func GenerateHashes(peaks []dsp.Peak) []fingerprint.HashEntry {
	out := make([]fingerprint.HashEntry, 0, len(peaks)*fanoutSize)
	for i := range peaks {
		a := peaks[i]
		emitted := 0
		for j := i + 1; j < len(peaks); j++ {
			t := peaks[j]
			dt := t.Frame - a.Frame
			if dt < targetMinFrames {
				continue
			}
			if dt > targetMaxFrames {
				break
			}
			df := t.Bin - a.Bin
			if df > targetFreqSpan || df < -targetFreqSpan {
				continue
			}
			out = append(out, fingerprint.HashEntry{
				Hash:   encodeHash(a.Bin, t.Bin, dt),
				Offset: uint32(a.Frame),
			})
			emitted++
			if emitted >= fanoutSize {
				break
			}
		}
	}
	return out
}
