package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/nddq/presto/internal/audio"
	"github.com/nddq/presto/internal/dsp"
	"github.com/nddq/presto/internal/fingerprint/constellation"
)

func runAnalyze(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: presto analyze <file.wav> [output_dir]\n")
		fmt.Fprintf(os.Stderr, "       presto analyze --chirp [output_dir]\n")
		os.Exit(1)
	}
	if args[0] == "--chirp" {
		outDir := "."
		if len(args) >= 2 {
			outDir = args[1]
		}
		generateChirp(outDir)
		return
	}

	wavPath := args[0]
	outDir := "."
	if len(args) >= 2 {
		outDir = args[1]
	}

	const winSize = 1024
	const hopSize = 512
	const windowFunc = "hann"

	sig, err := audio.ReadWAV(wavPath)
	if err != nil {
		log.Fatalf("read WAV: %v", err)
	}

	durSec := float64(len(sig.Samples)) / float64(sig.SampleRate)
	fmt.Printf("file:        %s\n", filepath.Base(wavPath))
	fmt.Printf("duration:    %.1f s\n", durSec)
	fmt.Printf("sample rate: %d Hz\n", sig.SampleRate)
	fmt.Printf("samples:     %d\n", len(sig.Samples))

	// Visualize at most 3 seconds from the middle
	vizSamples := sig.Samples
	maxVizSamples := 3 * sig.SampleRate
	if len(vizSamples) > maxVizSamples {
		mid := len(vizSamples) / 2
		vizSamples = vizSamples[mid-maxVizSamples/2 : mid+maxVizSamples/2]
	}

	spect := dsp.Spectrogram(vizSamples, sig.SampleRate, winSize, hopSize, windowFunc)
	if spect == nil {
		log.Fatal("signal too short for spectrogram")
	}
	fmt.Printf("frames:      %d\n", len(spect))
	fmt.Printf("bins:        %d\n", len(spect[0]))

	peaks := dsp.FindPeaks(spect, 5, 12, 0)
	fmt.Printf("peaks:       %d\n", len(peaks))

	hashes := constellation.GenerateHashes(peaks)
	fmt.Printf("hashes:      %d\n", len(hashes))
	fmt.Printf("density:     %.0f hashes/sec\n", float64(len(hashes))/durSec)

	vizDur := float64(len(vizSamples)) / float64(sig.SampleRate)
	img, lo, col := renderSpectrogram(spect, spectrogramParams{
		hzPerBin:     float64(sig.SampleRate) / float64(winSize),
		framesPerSec: float64(sig.SampleRate) / float64(hopSize),
		duration:     vizDur,
		title:        filepath.Base(wavPath),
	})

	spectPath := filepath.Join(outDir, "spectrogram.png")
	savePNG(spectPath, img)

	// --- Peaks overlay ---
	imgP := cloneRGBA(img)
	dotR := max(lo.PixW, lo.PixH) + 1
	for _, p := range peaks {
		if p.Bin >= lo.MaxBin {
			continue
		}
		cx := lo.PlotX0 + p.Frame*lo.PixW + lo.PixW/2
		cy := lo.PlotY0 + (lo.MaxBin-1-p.Bin)*lo.PixH + lo.PixH/2
		drawPeakDot(imgP, cx, cy, dotR, lo.PlotX0, lo.PlotY0, lo.PlotW, lo.PlotH, col.peak, col.peakGlow)
	}

	info := []string{
		fmt.Sprintf("peaks: %d", len(peaks)),
		fmt.Sprintf("hashes: %d", len(hashes)),
		fmt.Sprintf("win=%d hop=%d %s", winSize, hopSize, windowFunc),
		fmt.Sprintf("%.1fs @ %dHz", durSec, sig.SampleRate),
	}
	drawInfoBox(imgP, info, lo, col)

	peaksPath := filepath.Join(outDir, "spectrogram_peaks.png")
	savePNG(peaksPath, imgP)

	fmt.Printf("\ngenerated:\n  %s\n  %s\n", spectPath, peaksPath)
}

// --- Chirp ---

func generateChirp(outDir string) {
	const sr = 44100
	const dur = 1.5
	n := int(dur * float64(sr))
	samples := make([]float64, n)
	for i := range samples {
		t := float64(i) / float64(sr)
		phase := 2 * math.Pi * (200*t + (4000-200)*t*t/(2*dur))
		samples[i] = 0.7 * math.Sin(phase)
	}

	spect := dsp.Spectrogram(samples, sr, 1024, 512, "hann")

	img, _, _ := renderSpectrogram(spect, spectrogramParams{
		hzPerBin:     float64(sr) / 1024.0,
		framesPerSec: float64(sr) / 512.0,
		duration:     dur,
		title:        "chirp 200 -> 4000 Hz",
	})

	outPath := filepath.Join(outDir, "chirp.png")
	savePNG(outPath, img)
	fmt.Printf("generated: %s\n", outPath)
}
