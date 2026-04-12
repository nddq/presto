package store

import (
	"math"
	"math/rand/v2"
	"sort"
	"unsafe"

	"github.com/nddq/presto/internal/dsp"
	"github.com/nddq/presto/internal/fingerprint"
)

// --- Sub-band LSH inverted index ---
//
// Each frame is 39 bytes (NumMelBands-1) of 0/1 values. We project each
// frame to numLSHHashes independent random bit selections and build a
// posting list keyed by (hashFuncIdx, hashValue). The random bits are
// seeded with a fixed constant so all stores use the same projections.

const (
	numLSHHashes  = 8
	bitsPerLSH    = 12
	subbandMaxPL  = 10000 // posting list cap
	subbandStride = dsp.NumMelBands - 1
)

type subbandIndexEntry struct {
	SongID uint16
	Frame  uint16
}

// lshBits holds the precomputed random bit selections.
var lshBits [numLSHHashes][bitsPerLSH]uint8

func init() {
	const frameBits = 39
	rng := rand.New(rand.NewPCG(0xC0FFEE, 0xDECADE))
	for h := range numLSHHashes {
		perm := make([]uint8, frameBits)
		for i := range perm {
			perm[i] = uint8(i)
		}
		for i := len(perm) - 1; i > 0; i-- {
			j := rng.IntN(i + 1)
			perm[i], perm[j] = perm[j], perm[i]
		}
		for b := range bitsPerLSH {
			lshBits[h][b] = perm[b]
		}
	}
}

func packSubbandFrame(frame []byte) uint64 {
	var v uint64
	for i, b := range frame {
		v |= uint64(b&1) << i
	}
	return v
}

func lshHashFrame(packed uint64, hashFuncIdx int) uint32 {
	var h uint32
	for b, bit := range lshBits[hashFuncIdx] {
		if packed&(1<<bit) != 0 {
			h |= 1 << b
		}
	}
	return uint32(hashFuncIdx)<<bitsPerLSH | h
}

type subbandIndex struct {
	idx map[uint32][]subbandIndexEntry
}

func buildSubbandIndex(songs []Song, indexStride int) *subbandIndex {
	idx := make(map[uint32][]subbandIndexEntry)
	overflowed := make(map[uint32]bool)

	for songID, song := range songs {
		if songID > 0xFFFF {
			break
		}
		data := song.FP.Data
		nFrames := song.FP.Frames
		stride := song.FP.Stride
		for f := 0; f < nFrames; f += indexStride {
			if f > 0xFFFF {
				break
			}
			packed := packSubbandFrame(data[f*stride : (f+1)*stride])
			entry := subbandIndexEntry{SongID: uint16(songID), Frame: uint16(f)}
			for h := range numLSHHashes {
				key := lshHashFrame(packed, h)
				if overflowed[key] {
					continue
				}
				list := idx[key]
				if len(list) >= subbandMaxPL {
					delete(idx, key)
					overflowed[key] = true
					continue
				}
				idx[key] = append(list, entry)
			}
		}
	}
	return &subbandIndex{idx: idx}
}

func querySubbandIndex(si *subbandIndex, sampleData []byte, stride, topK int) []candidate {
	if stride == 0 {
		return nil
	}
	sampleFrames := len(sampleData) / stride
	votes := make(map[uint64]int)
	seen := make(map[uint64]struct{})

	for i := 0; i < sampleFrames; i++ {
		clear(seen)
		packed := packSubbandFrame(sampleData[i*stride : (i+1)*stride])
		for h := range numLSHHashes {
			key := lshHashFrame(packed, h)
			list, ok := si.idx[key]
			if !ok {
				continue
			}
			for _, hit := range list {
				delta := int32(hit.Frame) - int32(i)
				voteK := uint64(hit.SongID)<<32 | uint64(uint32(delta))
				if _, dup := seen[voteK]; dup {
					continue
				}
				seen[voteK] = struct{}{}
				votes[voteK]++
			}
		}
	}
	if len(votes) == 0 {
		return nil
	}

	// Per-song peak
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

// --- subbandStoreStrategy ---

type subbandStoreStrategy struct{}

const subbandIndexStride = 2

func (s *subbandStoreStrategy) BuildIndex(songs []Song) (any, error) {
	return buildSubbandIndex(songs, subbandIndexStride), nil
}

func (s *subbandStoreStrategy) Match(index any, songs []Song, sampleFP *fingerprint.FP, topK int) []MatchResult {
	si := index.(*subbandIndex)
	verifyLimit := max(topK*2, 10)
	cands := querySubbandIndex(si, sampleFP.Data, sampleFP.Stride, verifyLimit)
	if len(cands) == 0 {
		return nil
	}

	const searchRadius = 8
	results := make([]MatchResult, 0, topK)
	for _, c := range cands {
		if c.Votes < 2 {
			continue
		}
		song := songs[c.SongID]
		score, offset := subbandScoreCandidate(sampleFP, song.FP, c.Delta, searchRadius)
		if score <= 0 {
			continue
		}
		results = append(results, MatchResult{
			Name:   song.Name,
			Score:  score,
			Votes:  c.Votes,
			Offset: offset,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

func subbandScoreCandidate(sampleFP, songFP *fingerprint.FP, delta int32, radius int) (float64, int) {
	maxOffset := songFP.Frames - sampleFP.Frames
	if maxOffset < 0 {
		return 0, 0
	}
	center := int(delta)
	lo := max(center-radius, 0)
	hi := min(center+radius, maxOffset)
	stride := sampleFP.Stride
	sampleLen := sampleFP.Frames * stride
	bestScore := 0.0
	bestOffset := lo
	for off := lo; off <= hi; off++ {
		start := off * stride
		score := subbandCompareBytes(sampleFP.Data[:sampleLen], songFP.Data[start:start+sampleLen])
		if score > bestScore {
			bestScore = score
			bestOffset = off
		}
	}
	return bestScore, bestOffset
}

func subbandCompareBytes(a, b []byte) float64 {
	n := len(a)
	if n == 0 {
		return 0
	}
	mismatches := 0
	chunks := n / 8
	if chunks > 0 {
		ap := unsafe.Pointer(unsafe.SliceData(a))
		bp := unsafe.Pointer(unsafe.SliceData(b))
		for i := range chunks {
			off := uintptr(i * 8)
			xor := *(*uint64)(unsafe.Add(ap, off)) ^ *(*uint64)(unsafe.Add(bp, off))
			mismatches += int((xor * 0x0101010101010101) >> 56)
		}
	}
	for i := chunks * 8; i < n; i++ {
		if a[i] != b[i] {
			mismatches++
		}
	}
	raw := float64(n-mismatches) / float64(n)
	return math.Max(0, (raw-0.5)*2.0)
}

func (s *subbandStoreStrategy) EncodeFP(fp *fingerprint.FP) []byte {
	// Data is already raw bytes, just return it
	return fp.Data
}

func (s *subbandStoreStrategy) DecodeFP(data []byte) *fingerprint.FP {
	frames := len(data) / subbandStride
	return &fingerprint.FP{
		Data:   data,
		Stride: subbandStride,
		Frames: frames,
	}
}
