package store

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nddq/presto/internal/audio"
	"github.com/nddq/presto/internal/fingerprint"

	_ "github.com/nddq/presto/internal/fingerprint/constellation"
	_ "github.com/nddq/presto/internal/fingerprint/subband"
)

// genDistinctSignal generates a synthetic track whose spectral content is
// deterministic in seed but genuinely different from other seeds. Each
// track gets its own fundamental, harmonic amplitude profile, and a
// light inharmonic partial — enough variation to produce fingerprints
// that are distinguishable by the constellation matcher.
func genDistinctSignal(seed int, durationSec float64) []float64 {
	rng := rand.New(rand.NewPCG(uint64(seed)*7919+1, uint64(seed)*104729+3))

	fund := 110.0 + rng.Float64()*660.0

	amps := [5]float64{}
	for i := range amps {
		amps[i] = 0.1 + rng.Float64()*0.4
	}

	inharmonic := 1.6 + rng.Float64()*1.2
	inharmonicAmp := 0.15 + rng.Float64()*0.2

	vibratoRate := 4.0 + rng.Float64()*4.0
	vibratoDepth := 0.001 + rng.Float64()*0.004

	n := int(durationSec * float64(testSampleRate))
	samples := make([]float64, n)
	for i := range samples {
		t := float64(i) / float64(testSampleRate)

		env := 1.0
		if t < 0.05 {
			env = t / 0.05
		} else if t > durationSec-0.05 {
			env = (durationSec - t) / 0.05
		}

		freqMod := 1.0 + vibratoDepth*math.Sin(2*math.Pi*vibratoRate*t)
		for h, amp := range amps {
			samples[i] += env * amp * math.Sin(2*math.Pi*fund*float64(h+1)*freqMod*t)
		}
		samples[i] += env * inharmonicAmp * math.Sin(2*math.Pi*fund*inharmonic*t)
	}
	return samples
}

// TestRealMusicPerformance indexes the actual songs found under ./audio/
// (extracted from git history) and matches the clips under ./testSamples/
// against them. Skipped if the audio directory is missing.
//
// Run with: go test ./internal/store -run TestRealMusicPerformance -v
func TestRealMusicPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("long-running real-music perf test; omit -short to run")
	}
	audioDir := "../../audio"
	clipDir := "../../testSamples"
	if _, err := os.Stat(audioDir); err != nil {
		t.Skipf("no %s directory; run after extracting real songs from git history", audioDir)
	}

	songs, _ := filepath.Glob(filepath.Join(audioDir, "*.wav"))
	clips, _ := filepath.Glob(filepath.Join(clipDir, "*.wav"))
	if len(songs) == 0 || len(clips) == 0 {
		t.Skipf("no songs or clips found (songs=%d clips=%d)", len(songs), len(clips))
	}

	buildStart := time.Now()
	s := New(testWinSize, testHopSize, testWindow, "constellation")
	var totalHashes int
	for _, p := range songs {
		fp, err := fingerprint.Fingerprint(p, testWinSize, testHopSize, testWindow, 0)
		if err != nil {
			t.Fatal(err)
		}
		s.Add(filepath.Base(p), fp)
		totalHashes += len(fp.Hashes)
	}
	s.Build()
	t.Logf("indexed %d real songs, %d total hashes (%.1fMB index blob) in %.1fs",
		len(songs), totalHashes, float64(totalHashes*8)/(1024*1024),
		time.Since(buildStart).Seconds())
	t.Log("")
	t.Logf("  %-26s %-12s %-12s %s", "clip", "matchMs", "score", "top match")
	t.Logf("  %-26s %-12s %-12s %s", "----", "-------", "-----", "---------")

	for _, clipPath := range clips {
		clipFP, err := fingerprint.Fingerprint(clipPath, testWinSize, testHopSize, testWindow, 0)
		if err != nil {
			t.Error(err)
			continue
		}

		const runs = 3
		var lshTotal time.Duration
		var lshResults []MatchResult
		for range runs {
			t0 := time.Now()
			lshResults = s.Match(clipFP, 5)
			lshTotal += time.Since(t0)
		}
		lshAvg := lshTotal / runs

		lshTop := ""
		lshScore := 0.0
		if len(lshResults) > 0 {
			lshTop = lshResults[0].Name
			lshScore = lshResults[0].Score
		}
		t.Logf("  %-26s %-12s %-12s %s",
			filepath.Base(clipPath),
			fmt.Sprintf("%.2fms", float64(lshAvg.Microseconds())/1000.0),
			fmt.Sprintf("%.4f", lshScore),
			lshTop,
		)
	}
}

func TestScalingPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("long-running perf test; omit -short to run")
	}

	countScenarios := []struct {
		numSongs    int
		durationSec float64
	}{
		{50, 30},
		{100, 30},
		{250, 30},
		{500, 30},
	}

	durationScenarios := []struct {
		numSongs    int
		durationSec float64
	}{
		{100, 15},
		{100, 30},
		{100, 60},
		{100, 120},
	}

	t.Log("")
	t.Log("=== Scaling on song count (duration fixed at 30s) ===")
	runScalingScenarios(t, countScenarios)

	t.Log("")
	t.Log("=== Scaling on track duration (library fixed at 100 songs) ===")
	runScalingScenarios(t, durationScenarios)
}

func runScalingScenarios(t *testing.T, scenarios []struct {
	numSongs    int
	durationSec float64
}) {
	t.Log("")
	t.Logf("  %-6s %-6s %-10s %-10s %-10s %-10s %-9s %-9s %s",
		"songs", "dur", "hashes", "fpAvgMs", "buildMs", "matchMs", "score", "correct", "top")
	t.Logf("  %-6s %-6s %-10s %-10s %-10s %-10s %-9s %-9s %s",
		"-----", "---", "------", "-------", "-------", "-------", "-----", "-------", "---")

	for _, sc := range scenarios {
		r := runOneScenario(t, sc.numSongs, sc.durationSec)
		t.Logf("  %-6d %-6s %-10d %-10s %-10s %-10s %-9s %-9v %s",
			r.numSongs,
			fmt.Sprintf("%.0fs", sc.durationSec),
			r.totalHashes,
			fmt.Sprintf("%.1f", r.avgFingerprintMs),
			fmt.Sprintf("%.0f", r.indexBuildMs),
			fmt.Sprintf("%.2f", r.matchMs),
			fmt.Sprintf("%.3f", r.winnerScore),
			r.correct,
			r.winner,
		)
	}
}

type scenarioResult struct {
	numSongs         int
	totalHashes      int
	avgFingerprintMs float64
	indexBuildMs     float64
	matchMs          float64
	correct          bool
	winner           string
	winnerScore      float64
}

func runOneScenario(t *testing.T, numSongs int, durationSec float64) scenarioResult {
	t.Helper()

	fps := make([]*fingerprint.FP, numSongs)
	names := make([]string, numSongs)
	var totalHashes int
	fpStart := time.Now()
	for i := range numSongs {
		samples := genDistinctSignal(i, durationSec)
		sig := &audio.Signal{SampleRate: testSampleRate, Samples: samples}
		fp, err := fingerprint.FingerprintSignal(sig, testWinSize, testHopSize, testWindow, 0)
		if err != nil {
			t.Fatal(err)
		}
		fps[i] = fp
		names[i] = fmt.Sprintf("song_%04d.wav", i)
		totalHashes += len(fp.Hashes)
	}
	fpElapsed := time.Since(fpStart)

	buildStart := time.Now()
	s := New(testWinSize, testHopSize, testWindow, "constellation")
	for i, fp := range fps {
		s.Add(names[i], fp)
	}
	s.Build()
	buildElapsed := time.Since(buildStart)

	// Clip: 5s from the middle of song targetIdx
	targetIdx := numSongs / 2
	clipStart := int((durationSec/2 - 2.5) * float64(testSampleRate))
	if clipStart < 0 {
		clipStart = 0
	}
	clipEnd := clipStart + 5*testSampleRate
	src := genDistinctSignal(targetIdx, durationSec)
	if clipEnd > len(src) {
		clipEnd = len(src)
	}
	clipSig := &audio.Signal{SampleRate: testSampleRate, Samples: src[clipStart:clipEnd]}
	clipFP, err := fingerprint.FingerprintSignal(clipSig, testWinSize, testHopSize, testWindow, 0)
	if err != nil {
		t.Fatal(err)
	}

	const runs = 3
	var matchTotal time.Duration
	var lshResults []MatchResult
	for range runs {
		t0 := time.Now()
		lshResults = s.Match(clipFP, 5)
		matchTotal += time.Since(t0)
	}
	matchAvg := matchTotal / runs

	expected := names[targetIdx]
	winner := ""
	winnerScore := 0.0
	if len(lshResults) > 0 {
		winner = lshResults[0].Name
		winnerScore = lshResults[0].Score
	}
	correct := winner == expected

	runtime.GC()

	return scenarioResult{
		numSongs:         numSongs,
		totalHashes:      totalHashes,
		avgFingerprintMs: float64(fpElapsed.Milliseconds()) / float64(numSongs),
		indexBuildMs:     float64(buildElapsed.Microseconds()) / 1000.0,
		matchMs:          float64(matchAvg.Microseconds()) / 1000.0,
		correct:          correct,
		winner:           winner,
		winnerScore:      winnerScore,
	}
}
