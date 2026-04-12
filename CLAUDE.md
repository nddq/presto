# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Presto is an audio fingerprinting and recognition tool written in Go. It identifies song tracks from audio snippets using two selectable algorithms — constellation peak-pair hashing (Wang 2003, default) and Haitsma-Kalker sub-band mel-band energy comparison — with a persistent file-backed library and vote-based lookup that scales to thousands of songs. See [docs/algorithm.md](docs/algorithm.md) for the full algorithm walkthrough.

## Build, Run, and Test

```bash
# Build
go build ./cmd/presto

# Index an audio directory into a library file
./presto index <audio_dir> <store_file> <winSize> <hopSize> [windowFunc]

# Index with sub-band algorithm instead of constellation
./presto index <audio_dir> <store_file> <winSize> <hopSize> [windowFunc] --algo subband

# Match a sample against an indexed library (one-shot CLI)
./presto match <store_file> <sample_file>

# Run the HTTP server (configured via PRESTO_* env vars)
./presto serve

# Example
./presto index ./songs/ library.prfp 1024 512 hann
./presto match library.prfp clip.wav
PRESTO_STORE_PATH=./library.prfp ./presto serve

# Window function options: hann, hamming, bartlett (omit for none)

# Container build
docker build -t presto:latest .

# Run all tests
go test ./...

# Run a single test
go test ./internal/fingerprint/ -run TestExactClipMatch -v

# Run benchmarks
go test ./internal/fingerprint/ -bench=. -benchmem
go test ./internal/store/ -bench=. -benchmem
```

Input files must be WAV format (PCM, 8/16/24/32-bit).

## Architecture

```text
internal/
  audio/                WAV file I/O
    wav.go              Signal type, ReadWAV, DecodeWAV, EncodeWAV, WriteWAV

  dsp/                  Signal processing primitives
    fft.go              Cooley-Tukey radix-2 FFT, per-call scratch buffer
    window.go           Window functions (hann, hamming, bartlett)
    spectrogram.go      STFT spectrogram, signal normalization, noise injection
    peaks.go            2D time-frequency local maximum detection
    mel.go              Mel-frequency banding, sub-band energy comparison

  fingerprint/          FP type, Strategy interface, registry
    fingerprint.go      FP tagged union, Strategy interface, Get/Register, convenience funcs
    constellation/      Constellation strategy (peak-pair hashing, init-registers)
    subband/            Sub-band strategy (mel-band energy bits, init-registers)

  store/                Persistent library with vote-based lookup
    store.go            Store type, Load (mmap), Save, Match API, StoreStrategy dispatch
    strategy.go         StoreStrategy interface, algorithm byte mapping
    format.go           Binary file format (PRFP v2 with algorithm byte)
    constellation_index.go  Constellation hash inverted index + store strategy
    subband_index.go    LSH inverted index + sub-band store strategy

  metrics/              Stdlib-only Prometheus metrics (Counter/Gauge/Histogram/Registry)
    metrics.go          Metric types, registry, runtime collector

cmd/presto/
  main.go               Subcommand dispatch: index / match / serve
  server.go             HTTP server (POST /v1/match, /v1/stats, /healthz, /readyz, /metrics)
  server_test.go        httptest-based handler tests

deploy/k8s/             Plain YAML manifests for Kubernetes deployment
Dockerfile              Multi-stage build (golang:1.26-alpine -> distroless)
docs/algorithm.md       Algorithm walkthrough covering both strategies
```

**Dependency graph**: `cmd/presto` -> `store`, `metrics` -> `fingerprint` -> `audio`, `dsp`

**Strategy pattern**: Both algorithms register via `init()` + blank import (like `database/sql` drivers). The `Strategy` interface covers fingerprinting and similarity; the `StoreStrategy` interface covers indexing, matching, and serialization. The store header's algorithm byte auto-selects at load time.

**Fingerprinting pipeline** (shared + per-algorithm):

1. Read/decode WAV (`audio.ReadWAV` or `audio.DecodeWAV`) -> float64 samples
2. Normalize + window + FFT (`dsp.Spectrogram`) -> magnitude spectrogram
3a. *Constellation*: `dsp.FindPeaks` -> `constellation.GenerateHashes` -> `FP{Hashes, NumFrames}`
3b. *Sub-band*: `dsp.MelBandsInto` -> `dsp.SubBandFPInto` -> `FP{Data, Stride, Frames}`

## HTTP server (`presto serve`)

Loads one `.prfp` library at startup (async — readyz reflects state). Auto-selects algorithm from header. Concurrent reads are safe (store is read-only after Load).

**Endpoints**: `POST /v1/match` (JSON), `GET /v1/stats`, `GET /healthz`, `GET /readyz`, `GET /metrics`

**Configuration** via environment variables:

- `PRESTO_STORE_PATH` (default `/var/lib/presto/library.prfp`)
- `PRESTO_LISTEN_ADDR` (default `:8080`)
- `PRESTO_MAX_UPLOAD_BYTES` (default 10 MiB)

**Graceful shutdown**: `SIGINT` / `SIGTERM` -> drain with 10 s timeout. Panic recovery middleware on all routes.

## Store

**File format** (single binary file, little-endian, version 2):

```text
Header (32 bytes):
  magic "PRFP", version (2), songCount,
  winSize, hopSize, windowFunc, algorithm (0=constellation, 1=subband), reserved

Per song:
  nameLen uint16, name, numFrames uint32, dataLen uint32, fpData
```

`Load` memory-maps the file and zero-copy slices per-song data via `unsafe.Slice` (with runtime alignment guard). Index is rebuilt on load (not serialized).

**Matching**: direct hash lookup (constellation) or LSH candidate filter + sliding-window Compare (sub-band), both using `(songID, delta)` vote accumulation.

**Size limits**: 65,535 songs (uint16 songID); song length limited only by uint32 frame offset.
