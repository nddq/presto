package store

import (
	"sort"

	"github.com/nddq/presto/internal/fingerprint"
)

// maxPostingLen caps inverted-index posting-list length. Any hash value
// that would accumulate more than this many entries (usually silence or
// whitenoise patterns that collide across the entire library) is
// dropped. These patterns carry no discriminative signal and would
// otherwise dominate the vote map with O(N²) work at query time.
const maxPostingLen = 10000

// indexEntry is one posting in the inverted index: the song the hash
// came from and the frame offset of its anchor peak in that song.
type indexEntry struct {
	SongID uint16
	Offset uint32
}

// buildConstellationIndex walks every song's hash list and builds a map from hash
// value to the (songID, offset) pairs that produced it. Hashes whose
// posting list would exceed maxPostingLen are dropped entirely.
func buildConstellationIndex(songs []Song) map[uint32][]indexEntry {
	idx := make(map[uint32][]indexEntry)
	overflowed := make(map[uint32]bool)

	for songID, song := range songs {
		if songID > 0xFFFF {
			break // uint16 cap on song ID
		}
		for _, h := range song.FP.Hashes {
			if overflowed[h.Hash] {
				continue
			}
			list := idx[h.Hash]
			if len(list) >= maxPostingLen {
				delete(idx, h.Hash)
				overflowed[h.Hash] = true
				continue
			}
			idx[h.Hash] = append(list, indexEntry{
				SongID: uint16(songID),
				Offset: h.Offset,
			})
		}
	}
	return idx
}

// candidate is one (songID, delta) pair with its accumulated vote count.
type candidate struct {
	SongID uint16
	Delta  int32
	Votes  int
}

// queryConstellationIndex looks up every hash in sampleHashes, casts a vote for every
// (songID, delta) consistent hit, and returns the top-K candidates by
// raw vote count. For each song only the best (highest-vote) delta is
// kept, so a long song with many scattered collisions cannot crowd out
// a shorter song whose votes concentrate at its one correct delta.
func queryConstellationIndex(idx map[uint32][]indexEntry, sampleHashes []fingerprint.HashEntry, topK int) []candidate {
	if len(sampleHashes) == 0 {
		return nil
	}

	votes := make(map[uint64]int)
	for _, h := range sampleHashes {
		hits, ok := idx[h.Hash]
		if !ok {
			continue
		}
		for _, hit := range hits {
			delta := int32(hit.Offset) - int32(h.Offset)
			key := uint64(hit.SongID)<<32 | uint64(uint32(delta))
			votes[key]++
		}
	}
	if len(votes) == 0 {
		return nil
	}

	// Collapse to per-song peaks: for each song, keep only the delta
	// with the highest vote count.
	perSong := make(map[uint16]candidate)
	for k, v := range votes {
		songID := uint16(k >> 32)
		delta := int32(uint32(k))
		cur, ok := perSong[songID]
		if !ok || v > cur.Votes {
			perSong[songID] = candidate{SongID: songID, Delta: delta, Votes: v}
		}
	}

	cands := make([]candidate, 0, len(perSong))
	for _, c := range perSong {
		cands = append(cands, c)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Votes > cands[j].Votes })
	if len(cands) > topK {
		cands = cands[:topK]
	}
	return cands
}

// --- constellationStoreStrategy ---

type constellationStoreStrategy struct{}

func (s *constellationStoreStrategy) BuildIndex(songs []Song) (any, error) {
	return buildConstellationIndex(songs), nil
}

func (s *constellationStoreStrategy) Match(index any, songs []Song, sampleFP *fingerprint.FP, topK int) []MatchResult {
	idx := index.(map[uint32][]indexEntry)
	cands := queryConstellationIndex(idx, sampleFP.Hashes, topK)
	if len(cands) == 0 {
		return nil
	}
	total := float64(len(sampleFP.Hashes))
	results := make([]MatchResult, 0, len(cands))
	for _, c := range cands {
		results = append(results, MatchResult{
			Name:   songs[c.SongID].Name,
			Score:  float64(c.Votes) / total,
			Votes:  c.Votes,
			Offset: int(c.Delta),
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results
}

func (s *constellationStoreStrategy) EncodeFP(fp *fingerprint.FP) []byte {
	return hashesToBytes(fp.Hashes)
}

func (s *constellationStoreStrategy) DecodeFP(data []byte) *fingerprint.FP {
	return &fingerprint.FP{
		Hashes: bytesToHashes(data),
	}
}
