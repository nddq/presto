package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/nddq/presto/internal/fingerprint"
	"github.com/nddq/presto/internal/store"

	// Register the available fingerprint strategies. Without these
	// blank imports, fingerprint.Get("constellation") etc. would fail.
	_ "github.com/nddq/presto/internal/fingerprint/constellation"
	_ "github.com/nddq/presto/internal/fingerprint/subband"
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  presto index <audio_dir> <store_file> <winSize> <hopSize> [windowFunc] [--algo constellation|subband]
      Fingerprint every WAV file in <audio_dir> and save the resulting
      library to <store_file>. Bad WAV files are logged and skipped.
      Window function options: hann, hamming, bartlett (omit for none).
      Algorithm options: constellation (default), subband.

  presto match <store_file> <sample_file>
      Load <store_file>, fingerprint <sample_file> using the library's
      own analysis parameters and algorithm, and print the top 5 matches.

  presto analyze <file.wav> [output_dir]
      Generate annotated spectrogram PNGs with peak detection overlay,
      axis labels, and analysis metadata. Writes spectrogram.png and
      spectrogram_peaks.png to the output directory (default: current).

  presto serve
      Run an HTTP server that loads a store and answers match requests.
      Configured via environment variables:
        PRESTO_STORE_PATH       path to the .prfp file (default /var/lib/presto/library.prfp)
        PRESTO_LISTEN_ADDR      listen address (default :8080)
        PRESTO_MAX_UPLOAD_BYTES max /v1/match body size in bytes (default 10485760)
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "index":
		runIndex(os.Args[2:])
	case "match":
		runMatch(os.Args[2:])
	case "analyze":
		runAnalyze(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

// parseAlgoFlag scans args for "--algo <name>" and returns the name and
// the args slice with the flag pair removed. If absent, returns the
// default strategy.
func parseAlgoFlag(args []string) (string, []string) {
	for i, a := range args {
		if a == "--algo" && i+1 < len(args) {
			name := args[i+1]
			rest := make([]string, 0, len(args)-2)
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+2:]...)
			return name, rest
		}
	}
	return fingerprint.DefaultStrategy, args
}

func runIndex(args []string) {
	algoName, args := parseAlgoFlag(args)
	if len(args) < 4 {
		usage()
		os.Exit(1)
	}
	audioDir := args[0]
	storePath := args[1]
	winSize, err := strconv.Atoi(args[2])
	if err != nil {
		log.Fatalf("invalid window size: %v", err)
	}
	hopSize, err := strconv.Atoi(args[3])
	if err != nil {
		log.Fatalf("invalid hop size: %v", err)
	}
	winFunc := ""
	if len(args) >= 5 {
		winFunc = args[4]
	}

	algo, err := fingerprint.Get(algoName)
	if err != nil {
		log.Fatal(err)
	}

	entries, err := os.ReadDir(audioDir)
	if err != nil {
		log.Fatal(err)
	}

	type result struct {
		name string
		fp   *fingerprint.FP
		err  error
	}

	var wg sync.WaitGroup
	ch := make(chan result)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".wav" {
			continue
		}
		name := entry.Name()
		wg.Go(func() {
			sig, err := fingerprint.ReadWAVForStrategy(filepath.Join(audioDir, name))
			if err != nil {
				ch <- result{name: name, err: err}
				return
			}
			fp, err := algo.FingerprintSignal(sig, winSize, hopSize, winFunc, 0)
			ch <- result{name: name, fp: fp, err: err}
		})
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

	s := store.New(winSize, hopSize, winFunc, algoName)
	skipped := 0
	for r := range ch {
		if r.err != nil {
			log.Printf("skip %s: %v", r.name, r.err)
			skipped++
			continue
		}
		s.Add(r.name, r.fp)
	}
	if err := s.Build(); err != nil {
		log.Fatalf("build index: %v", err)
	}

	if err := s.Save(storePath); err != nil {
		log.Fatalf("save store: %v", err)
	}
	fmt.Printf("indexed %d songs (%d skipped, algo=%s) -> %s\n",
		len(s.Songs), skipped, algoName, storePath)
}

func runMatch(args []string) {
	if len(args) < 2 {
		usage()
		os.Exit(1)
	}
	storePath := args[0]
	samplePath := args[1]

	s, err := store.Load(storePath)
	if err != nil {
		log.Fatalf("load store: %v", err)
	}
	fmt.Printf("loaded %d songs (winSize=%d hopSize=%d window=%q algo=%s)\n",
		len(s.Songs), s.WinSize, s.HopSize, s.WindowFunc, s.AlgoName)

	// Use the same algorithm as the store
	algo, err := fingerprint.Get(s.AlgoName)
	if err != nil {
		log.Fatal(err)
	}
	sig, err := fingerprint.ReadWAVForStrategy(samplePath)
	if err != nil {
		log.Fatalf("read sample: %v", err)
	}
	sampleFP, err := algo.FingerprintSignal(sig, s.WinSize, s.HopSize, s.WindowFunc, 0)
	if err != nil {
		log.Fatalf("fingerprint sample: %v", err)
	}

	results := s.Match(sampleFP, 5)
	if len(results) == 0 {
		fmt.Println("no match found")
		return
	}
	fmt.Println("top matches:")
	for i, r := range results {
		fmt.Printf("  %d. %s  score=%.4f  offset=%d\n", i+1, r.Name, r.Score, r.Offset)
	}
}
