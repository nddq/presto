// Package store provides persistent fingerprint libraries with fast
// vote-based lookup. A Store holds a collection of named fingerprints and
// an inverted index that enables sub-linear matching against the library.
//
// Two algorithm strategies are supported: constellation (Wang 2003
// peak-pair hashing) and sub-band (Haitsma-Kalker mel-band comparison).
// The choice is encoded in the library file header so match and serve
// auto-select the same algorithm that was used at index time.
package store

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"syscall"
	"unsafe"

	"github.com/nddq/presto/internal/fingerprint"
)

// Store is a persistent library of audio fingerprints with an in-memory
// inverted index for fast lookup.
type Store struct {
	Songs      []Song
	WinSize    int
	HopSize    int
	WindowFunc string
	AlgoName   string // "constellation" or "subband"

	strategy  StoreStrategy
	algo      byte // header byte
	index     any  // opaque, built by strategy
	mmapData  []byte
	closeOnce sync.Once
	closeErr  error
}

// Song pairs a name (typically the source filename) with a fingerprint.
type Song struct {
	Name string
	FP   *fingerprint.FP
}

// MatchResult is one ranked match produced by [Store.Match].
type MatchResult struct {
	Name   string
	Score  float64
	Votes  int
	Offset int
}

// New returns an empty Store configured with the given analysis parameters
// and algorithm name ("constellation" or "subband").
func New(winSize, hopSize int, windowFunc, algoName string) *Store {
	if algoName == "" {
		algoName = fingerprint.DefaultStrategy
	}
	algo := algoForName(algoName)
	return &Store{
		WinSize:    winSize,
		HopSize:    hopSize,
		WindowFunc: windowFunc,
		AlgoName:   algoName,
		strategy:   strategyForAlgo(algo),
		algo:       algo,
	}
}

// Add appends a song to the store.
func (s *Store) Add(name string, fp *fingerprint.FP) {
	s.Songs = append(s.Songs, Song{Name: name, FP: fp})
}

// MaxSongs is the upper bound on the number of songs in a store.
const MaxSongs = 65535

// ErrTooManySongs is returned when a library exceeds [MaxSongs].
var ErrTooManySongs = errors.New("store: library exceeds 65535 songs")

// Build constructs the inverted index.
func (s *Store) Build() error {
	if len(s.Songs) > MaxSongs {
		return ErrTooManySongs
	}
	var err error
	s.index, err = s.strategy.BuildIndex(s.Songs)
	return err
}

// Save writes the store to disk.
func (s *Store) Save(path string) error {
	if len(s.Songs) > MaxSongs {
		return ErrTooManySongs
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	bw := bufio.NewWriter(f)
	winCode, err := encodeWindowFunc(s.WindowFunc)
	if err != nil {
		return err
	}
	if err := writeHeader(bw, fileHeader{
		SongCount:  uint32(len(s.Songs)),
		WinSize:    uint32(s.WinSize),
		HopSize:    uint32(s.HopSize),
		WindowFunc: winCode,
		Algorithm:  s.algo,
	}); err != nil {
		return err
	}
	for _, song := range s.Songs {
		fpBytes := s.strategy.EncodeFP(song.FP)
		if err := writeSongEntry(bw, song.Name, uint32(max(song.FP.NumFrames, song.FP.Frames)), fpBytes); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// hashesToBytes returns the raw byte view of a HashEntry slice.
func hashesToBytes(h []fingerprint.HashEntry) []byte {
	if len(h) == 0 {
		return nil
	}
	const entrySize = 8
	return unsafe.Slice((*byte)(unsafe.Pointer(&h[0])), len(h)*entrySize)
}

// bytesToHashes casts a byte region to a HashEntry slice with an alignment
// guard: zero-copy when aligned, one-shot copy otherwise.
func bytesToHashes(b []byte) []fingerprint.HashEntry {
	if len(b) == 0 {
		return nil
	}
	const entrySize = 8
	n := len(b) / entrySize
	if uintptr(unsafe.Pointer(&b[0]))%4 == 0 {
		return unsafe.Slice((*fingerprint.HashEntry)(unsafe.Pointer(&b[0])), n)
	}
	out := make([]fingerprint.HashEntry, n)
	for i := range n {
		out[i].Hash = binary.LittleEndian.Uint32(b[i*entrySize : i*entrySize+4])
		out[i].Offset = binary.LittleEndian.Uint32(b[i*entrySize+4 : i*entrySize+8])
	}
	return out
}

// Load opens a store file and returns a ready-to-query *Store.
func Load(path string) (*Store, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := int(info.Size())
	if size < headerSize {
		return nil, errors.New("store file smaller than header")
	}

	data, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}

	hdr, err := parseHeaderBytes(data)
	if err != nil {
		_ = syscall.Munmap(data)
		return nil, err
	}
	winFunc, err := decodeWindowFunc(hdr.WindowFunc)
	if err != nil {
		_ = syscall.Munmap(data)
		return nil, err
	}

	strat := strategyForAlgo(hdr.Algorithm)
	s := &Store{
		WinSize:    int(hdr.WinSize),
		HopSize:    int(hdr.HopSize),
		WindowFunc: winFunc,
		AlgoName:   algoNameForByte(hdr.Algorithm),
		strategy:   strat,
		algo:       hdr.Algorithm,
		Songs:      make([]Song, 0, hdr.SongCount),
		mmapData:   data,
	}
	pos := headerSize
	for i := uint32(0); i < hdr.SongCount; i++ {
		name, numFrames, fpBytes, next, err := parseSongEntryBytes(data, pos)
		if err != nil {
			_ = syscall.Munmap(data)
			return nil, fmt.Errorf("read song %d: %w", i, err)
		}
		pos = next
		fp := strat.DecodeFP(fpBytes)
		// Restore NumFrames/Frames from the file — DecodeFP only sets
		// algorithm-specific fields.
		if fp.IsConstellation() {
			fp.NumFrames = int(numFrames)
		} else {
			fp.Frames = int(numFrames)
		}
		s.Songs = append(s.Songs, Song{Name: name, FP: fp})
	}
	if err := s.Build(); err != nil {
		_ = syscall.Munmap(data)
		return nil, err
	}
	return s, nil
}

func algoNameForByte(b byte) string {
	switch b {
	case AlgoSubBand:
		return "subband"
	default:
		return "constellation"
	}
}

// Close unmaps the backing file. Idempotent, goroutine-safe.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		if s.mmapData == nil {
			return
		}
		s.closeErr = syscall.Munmap(s.mmapData)
		s.mmapData = nil
	})
	return s.closeErr
}

// Match identifies the best matches in the store for the given sample
// fingerprint. Returns at most topK results sorted by score descending.
func (s *Store) Match(sampleFP *fingerprint.FP, topK int) []MatchResult {
	if topK <= 0 {
		topK = 5
	}
	if sampleFP == nil || len(s.Songs) == 0 {
		return nil
	}
	results := s.strategy.Match(s.index, s.Songs, sampleFP, topK)
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}
