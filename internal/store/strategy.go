package store

import "github.com/nddq/presto/internal/fingerprint"

// StoreStrategy encapsulates the algorithm-specific parts of store
// indexing, matching, and serialization. Two implementations are provided:
//
//   - [constellationStoreStrategy] — direct hash inverted index with voting
//   - [subbandStoreStrategy] — LSH inverted index with sliding-window verification
type StoreStrategy interface {
	// BuildIndex constructs an in-memory lookup structure.
	// The returned value is opaque; it is stored on the Store and
	// passed back to Match on every query.
	BuildIndex(songs []Song) (any, error)

	// Match queries the index with a sample FP and returns ranked results.
	Match(index any, songs []Song, sampleFP *fingerprint.FP, topK int) []MatchResult

	// EncodeFP serializes a song's FP for the store file.
	EncodeFP(fp *fingerprint.FP) []byte

	// DecodeFP deserializes a song's FP from a byte region.
	// The returned FP may alias into data (zero-copy for mmap).
	DecodeFP(data []byte) *fingerprint.FP
}

// Algorithm identifier byte stored in the header.
const (
	AlgoConstellation byte = 0
	AlgoSubBand       byte = 1
)

// strategyForAlgo returns the StoreStrategy for the given algorithm byte.
func strategyForAlgo(algo byte) StoreStrategy {
	switch algo {
	case AlgoSubBand:
		return &subbandStoreStrategy{}
	default:
		return &constellationStoreStrategy{}
	}
}

// algoForName returns the header byte for a named strategy.
func algoForName(name string) byte {
	switch name {
	case "subband":
		return AlgoSubBand
	default:
		return AlgoConstellation
	}
}
