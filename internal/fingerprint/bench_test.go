package fingerprint_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nddq/presto/internal/audio"
	"github.com/nddq/presto/internal/dsp"
	"github.com/nddq/presto/internal/fingerprint"

	_ "github.com/nddq/presto/internal/fingerprint/constellation"
)

var benchDir string
var benchFullPath, benchClipPath string
var benchFullFP, benchClipFP *fingerprint.FP

func setupBenchFiles(b *testing.B) {
	b.Helper()
	if benchDir != "" {
		return
	}
	var err error
	benchDir, err = os.MkdirTemp("", "presto-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	benchFullPath = filepath.Join(benchDir, "full.wav")
	benchClipPath = filepath.Join(benchDir, "clip.wav")

	n := 5 * 44100
	full := make([]float64, n)
	for i := range full {
		t := float64(i) / 44100.0
		full[i] = 0.8 * math.Sin(2*math.Pi*440.0*t)
	}
	audio.WriteWAV(benchFullPath, full, 44100, 1)
	audio.WriteWAV(benchClipPath, full[44100:2*44100], 44100, 1)

	benchFullFP, err = fingerprint.Fingerprint(benchFullPath, 1024, 512, "hann", 0)
	if err != nil {
		b.Fatal(err)
	}
	benchClipFP, err = fingerprint.Fingerprint(benchClipPath, 1024, 512, "hann", 0)
	if err != nil {
		b.Fatal(err)
	}
}

func BenchmarkFFT(b *testing.B) {
	signal := make([]float64, 1024)
	for i := range signal {
		signal[i] = 0.8 * math.Sin(2*math.Pi*440.0*float64(i)/44100.0)
	}
	b.ResetTimer()
	for b.Loop() {
		dsp.FFT(signal)
	}
}

func BenchmarkSpectrogram(b *testing.B) {
	n := 5 * 44100
	samples := make([]float64, n)
	for i := range samples {
		samples[i] = 0.8 * math.Sin(2*math.Pi*440.0*float64(i)/44100.0)
	}
	b.ResetTimer()
	for b.Loop() {
		dsp.Spectrogram(samples, 44100, 1024, 512, "hann")
	}
}

func BenchmarkFingerprint(b *testing.B) {
	setupBenchFiles(b)
	b.ResetTimer()
	for b.Loop() {
		fingerprint.Fingerprint(benchFullPath, 1024, 512, "hann", 0)
	}
}

func BenchmarkSimilarity(b *testing.B) {
	setupBenchFiles(b)
	b.ResetTimer()
	for b.Loop() {
		fingerprint.Similarity(benchClipFP, benchFullFP)
	}
}
