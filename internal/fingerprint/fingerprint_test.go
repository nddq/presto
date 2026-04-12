package fingerprint_test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nddq/presto/internal/audio"
	"github.com/nddq/presto/internal/fingerprint"

	_ "github.com/nddq/presto/internal/fingerprint/constellation"
	_ "github.com/nddq/presto/internal/fingerprint/subband"
)

const (
	testSampleRate = 44100
	testChannels   = 1
)

func generateTone(freq float64, durationSec float64, sampleRate int) []float64 {
	n := int(durationSec * float64(sampleRate))
	samples := make([]float64, n)
	for i := range samples {
		t := float64(i) / float64(sampleRate)
		samples[i] = 0.8 * math.Sin(2*math.Pi*freq*t)
	}
	return samples
}

func generateChord(freqs []float64, durationSec float64, sampleRate int) []float64 {
	n := int(durationSec * float64(sampleRate))
	samples := make([]float64, n)
	amp := 0.8 / float64(len(freqs))
	for i := range samples {
		t := float64(i) / float64(sampleRate)
		for _, f := range freqs {
			samples[i] += amp * math.Sin(2*math.Pi*f*t)
		}
	}
	return samples
}

func writeWAV(t *testing.T, path string, samples []float64, sampleRate int) {
	t.Helper()
	if err := audio.WriteWAV(path, samples, sampleRate, testChannels); err != nil {
		t.Fatalf("failed to write WAV %s: %v", path, err)
	}
}

// mustFingerprint is a test helper that fails the test on any
// fingerprinting error. Used so test bodies can chain fingerprint
// calls without repeating error plumbing.
func mustFP(t testing.TB, filename string, winSize, hopSize int, windowFunction string, noiseScale float64) *fingerprint.FP {
	t.Helper()
	fp, err := fingerprint.Fingerprint(filename, winSize, hopSize, windowFunction, noiseScale)
	if err != nil {
		t.Fatalf("fingerprint %s: %v", filename, err)
	}
	return fp
}

func TestExactClipMatch(t *testing.T) {
	dir := t.TempDir()
	fullPath := filepath.Join(dir, "full.wav")
	clipPath := filepath.Join(dir, "clip.wav")

	full := generateTone(440.0, 5.0, testSampleRate)
	clip := full[2*testSampleRate : 3*testSampleRate]

	writeWAV(t, fullPath, full, testSampleRate)
	writeWAV(t, clipPath, clip, testSampleRate)

	fullFP := mustFP(t, fullPath, 1024, 512, "hann", 0)
	clipFP := mustFP(t, clipPath, 1024, 512, "hann", 0)

	matchRate := fingerprint.Similarity(clipFP, fullFP)
	t.Logf("exact clip match rate: %.4f", matchRate)
	if matchRate < 0.5 {
		t.Errorf("expected high match rate for exact clip, got %.4f", matchRate)
	}
}

func TestDiscrimination(t *testing.T) {
	dir := t.TempDir()

	songs := []struct {
		name string
		freq float64
	}{
		{"tone_440hz.wav", 440.0},
		{"tone_880hz.wav", 880.0},
		{"tone_1320hz.wav", 1320.0},
	}

	for _, s := range songs {
		writeWAV(t, filepath.Join(dir, s.name), generateTone(s.freq, 5.0, testSampleRate), testSampleRate)
	}

	source := generateTone(440.0, 5.0, testSampleRate)
	clipPath := filepath.Join(dir, "clip.wav")
	writeWAV(t, clipPath, source[testSampleRate:2*testSampleRate], testSampleRate)
	clipFP := mustFP(t, clipPath, 1024, 512, "hann", 0)

	bestMatch := ""
	bestRate := -1.0
	for _, s := range songs {
		fp := mustFP(t, filepath.Join(dir, s.name), 1024, 512, "hann", 0)
		rate := fingerprint.Similarity(clipFP, fp)
		t.Logf("  %s: %.4f", s.name, rate)
		if rate > bestRate {
			bestRate = rate
			bestMatch = s.name
		}
	}
	if bestMatch != "tone_440hz.wav" {
		t.Errorf("expected tone_440hz.wav, got %s (rate=%.4f)", bestMatch, bestRate)
	}
}

func TestChordDiscrimination(t *testing.T) {
	dir := t.TempDir()

	chords := []struct {
		name  string
		freqs []float64
	}{
		{"c_major.wav", []float64{261.63, 329.63, 392.00}},
		{"a_minor.wav", []float64{220.00, 261.63, 329.63}},
		{"g_major.wav", []float64{196.00, 246.94, 293.66}},
	}

	for _, c := range chords {
		writeWAV(t, filepath.Join(dir, c.name), generateChord(c.freqs, 5.0, testSampleRate), testSampleRate)
	}

	source := generateChord(chords[0].freqs, 5.0, testSampleRate)
	clipPath := filepath.Join(dir, "clip.wav")
	writeWAV(t, clipPath, source[testSampleRate:2*testSampleRate], testSampleRate)
	clipFP := mustFP(t, clipPath, 1024, 512, "hann", 0)

	bestMatch := ""
	bestRate := -1.0
	for _, c := range chords {
		fp := mustFP(t, filepath.Join(dir, c.name), 1024, 512, "hann", 0)
		rate := fingerprint.Similarity(clipFP, fp)
		t.Logf("  %s: %.4f", c.name, rate)
		if rate > bestRate {
			bestRate = rate
			bestMatch = c.name
		}
	}
	if bestMatch != "c_major.wav" {
		t.Errorf("expected c_major.wav, got %s (rate=%.4f)", bestMatch, bestRate)
	}
}

func TestNoiseRobustness(t *testing.T) {
	dir := t.TempDir()

	songs := []struct {
		name  string
		freqs []float64
	}{
		{"song_a.wav", []float64{440.0, 554.37, 659.25}},
		{"song_b.wav", []float64{293.66, 369.99, 440.0}},
		{"song_c.wav", []float64{523.25, 659.25, 783.99}},
	}

	for _, s := range songs {
		writeWAV(t, filepath.Join(dir, s.name), generateChord(s.freqs, 5.0, testSampleRate), testSampleRate)
	}

	source := generateChord(songs[0].freqs, 5.0, testSampleRate)
	clip := make([]float64, testSampleRate)
	copy(clip, source[testSampleRate:2*testSampleRate])
	clipPath := filepath.Join(dir, "noisy_clip.wav")
	writeWAV(t, clipPath, clip, testSampleRate)
	clipFP := mustFP(t, clipPath, 1024, 512, "hann", 0.03)

	bestMatch := ""
	bestRate := -1.0
	for _, s := range songs {
		fp := mustFP(t, filepath.Join(dir, s.name), 1024, 512, "hann", 0)
		rate := fingerprint.Similarity(clipFP, fp)
		t.Logf("  %s: %.4f", s.name, rate)
		if rate > bestRate {
			bestRate = rate
			bestMatch = s.name
		}
	}
	if bestMatch != "song_a.wav" {
		t.Errorf("expected song_a.wav, got %s (rate=%.4f)", bestMatch, bestRate)
	}
}

func TestVolumeInvariance(t *testing.T) {
	dir := t.TempDir()

	songA := generateTone(440.0, 5.0, testSampleRate)
	songB := generateTone(880.0, 5.0, testSampleRate)
	pathA := filepath.Join(dir, "a.wav")
	pathB := filepath.Join(dir, "b.wav")
	writeWAV(t, pathA, songA, testSampleRate)
	writeWAV(t, pathB, songB, testSampleRate)

	clip := make([]float64, testSampleRate)
	for i := range clip {
		clip[i] = songA[testSampleRate+i] * 0.25
	}
	writeWAV(t, filepath.Join(dir, "quiet.wav"), clip, testSampleRate)

	fpA := mustFP(t, pathA, 1024, 512, "hann", 0)
	fpB := mustFP(t, pathB, 1024, 512, "hann", 0)
	fpClip := mustFP(t, filepath.Join(dir, "quiet.wav"), 1024, 512, "hann", 0)

	rateA := fingerprint.Similarity(fpClip, fpA)
	rateB := fingerprint.Similarity(fpClip, fpB)
	t.Logf("quiet clip vs A: %.4f, vs B: %.4f", rateA, rateB)
	if rateA <= rateB {
		t.Errorf("expected A > B (A=%.4f, B=%.4f)", rateA, rateB)
	}
}

func TestWindowFunctions(t *testing.T) {
	dir := t.TempDir()

	songA := generateTone(440.0, 3.0, testSampleRate)
	songB := generateTone(880.0, 3.0, testSampleRate)
	pathA := filepath.Join(dir, "a.wav")
	pathB := filepath.Join(dir, "b.wav")
	writeWAV(t, pathA, songA, testSampleRate)
	writeWAV(t, pathB, songB, testSampleRate)

	clipPath := filepath.Join(dir, "clip.wav")
	writeWAV(t, clipPath, songA[testSampleRate/2:testSampleRate], testSampleRate)

	for _, wf := range []string{"", "hann", "hamming", "bartlett"} {
		name := wf
		if name == "" {
			name = "none"
		}
		t.Run(name, func(t *testing.T) {
			fpA := mustFP(t, pathA, 1024, 512, wf, 0)
			fpB := mustFP(t, pathB, 1024, 512, wf, 0)
			fpClip := mustFP(t, clipPath, 1024, 512, wf, 0)

			rateA := fingerprint.Similarity(fpClip, fpA)
			rateB := fingerprint.Similarity(fpClip, fpB)
			t.Logf("window=%s: rateA=%.4f, rateB=%.4f", name, rateA, rateB)
			if rateA <= rateB {
				t.Errorf("expected A > B (A=%.4f, B=%.4f)", rateA, rateB)
			}
		})
	}
}

func TestReadWriteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wav")

	original := generateTone(440.0, 0.1, testSampleRate)
	writeWAV(t, path, original, testSampleRate)

	sig, err := audio.ReadWAV(path)
	if err != nil {
		t.Fatal(err)
	}
	if sig.SampleRate != testSampleRate {
		t.Errorf("expected sample rate %d, got %d", testSampleRate, sig.SampleRate)
	}
	if len(sig.Samples) != len(original) {
		t.Errorf("expected %d samples, got %d", len(original), len(sig.Samples))
	}
}

func generateRichSignal(fundamental float64, durationSec float64, sampleRate int) []float64 {
	n := int(durationSec * float64(sampleRate))
	samples := make([]float64, n)
	harmonics := []struct{ mult, amp float64 }{
		{1.0, 0.5}, {2.0, 0.25}, {3.0, 0.12}, {4.0, 0.06}, {5.0, 0.03},
	}
	for i := range samples {
		t := float64(i) / float64(sampleRate)
		env := 1.0
		if t < 0.05 {
			env = t / 0.05
		} else if t > durationSec-0.05 {
			env = (durationSec - t) / 0.05
		}
		freqMod := 1.0 + 0.002*math.Sin(2*math.Pi*5.0*t)
		for _, h := range harmonics {
			samples[i] += env * h.amp * math.Sin(2*math.Pi*fundamental*h.mult*freqMod*t)
		}
	}
	return samples
}

func TestHeavyNoise(t *testing.T) {
	dir := t.TempDir()

	songs := []struct {
		name  string
		freqs []float64
	}{
		{"song_a.wav", []float64{440.0, 554.37, 659.25}},
		{"song_b.wav", []float64{293.66, 369.99, 440.0}},
		{"song_c.wav", []float64{523.25, 659.25, 783.99}},
	}

	for _, s := range songs {
		writeWAV(t, filepath.Join(dir, s.name), generateChord(s.freqs, 5.0, testSampleRate), testSampleRate)
	}

	source := generateChord(songs[0].freqs, 5.0, testSampleRate)
	clip := make([]float64, testSampleRate)
	copy(clip, source[testSampleRate:2*testSampleRate])
	clipPath := filepath.Join(dir, "clip.wav")
	writeWAV(t, clipPath, clip, testSampleRate)
	clipFP := mustFP(t, clipPath, 1024, 512, "hann", 0.05)

	bestMatch := ""
	bestRate := -1.0
	for _, s := range songs {
		fp := mustFP(t, filepath.Join(dir, s.name), 1024, 512, "hann", 0)
		rate := fingerprint.Similarity(clipFP, fp)
		t.Logf("  %s: %.4f", s.name, rate)
		if rate > bestRate {
			bestRate = rate
			bestMatch = s.name
		}
	}
	if bestMatch != "song_a.wav" {
		t.Errorf("expected song_a.wav, got %s (rate=%.4f)", bestMatch, bestRate)
	}
}

func TestLargeLibrary(t *testing.T) {
	dir := t.TempDir()

	fundamentals := []float64{220, 277, 330, 392, 440, 523, 587, 659, 740, 880}
	for i, f := range fundamentals {
		writeWAV(t, filepath.Join(dir, fmt.Sprintf("song_%02d.wav", i)), generateRichSignal(f, 5.0, testSampleRate), testSampleRate)
	}

	targetIdx := 4
	source := generateRichSignal(fundamentals[targetIdx], 5.0, testSampleRate)
	clipPath := filepath.Join(dir, "clip.wav")
	writeWAV(t, clipPath, source[2*testSampleRate:3*testSampleRate], testSampleRate)
	clipFP := mustFP(t, clipPath, 1024, 512, "hann", 0)

	bestMatch := ""
	bestRate := -1.0
	for i := range fundamentals {
		name := fmt.Sprintf("song_%02d.wav", i)
		fp := mustFP(t, filepath.Join(dir, name), 1024, 512, "hann", 0)
		rate := fingerprint.Similarity(clipFP, fp)
		t.Logf("  %s (%.0f Hz): %.4f", name, fundamentals[i], rate)
		if rate > bestRate {
			bestRate = rate
			bestMatch = name
		}
	}
	expected := fmt.Sprintf("song_%02d.wav", targetIdx)
	if bestMatch != expected {
		t.Errorf("expected %s, got %s (rate=%.4f)", expected, bestMatch, bestRate)
	}
}

func TestRichSignalDiscrimination(t *testing.T) {
	dir := t.TempDir()

	songs := []struct {
		name string
		fund float64
	}{
		{"violin_a4.wav", 440.0},
		{"violin_c5.wav", 523.25},
		{"violin_e4.wav", 329.63},
	}

	for _, s := range songs {
		writeWAV(t, filepath.Join(dir, s.name), generateRichSignal(s.fund, 5.0, testSampleRate), testSampleRate)
	}

	source := generateRichSignal(songs[0].fund, 5.0, testSampleRate)
	clip := make([]float64, testSampleRate)
	for i := range clip {
		clip[i] = source[testSampleRate+i] * 0.5
	}
	clipPath := filepath.Join(dir, "clip.wav")
	writeWAV(t, clipPath, clip, testSampleRate)
	clipFP := mustFP(t, clipPath, 1024, 512, "hann", 0.04)

	bestMatch := ""
	bestRate := -1.0
	for _, s := range songs {
		fp := mustFP(t, filepath.Join(dir, s.name), 1024, 512, "hann", 0)
		rate := fingerprint.Similarity(clipFP, fp)
		t.Logf("  %s: %.4f", s.name, rate)
		if rate > bestRate {
			bestRate = rate
			bestMatch = s.name
		}
	}
	if bestMatch != "violin_a4.wav" {
		t.Errorf("expected violin_a4.wav, got %s (rate=%.4f)", bestMatch, bestRate)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
