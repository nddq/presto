package audio

import (
	"encoding/binary"
	"testing"
)

// FuzzDecodeWAV exercises the WAV decoder against arbitrary byte inputs.
// The decoder must never panic — it accepts untrusted bytes from the
// HTTP /v1/match endpoint — and must always either return a Signal or
// an error.
//
// Run with: go test ./internal/audio -fuzz=FuzzDecodeWAV -fuzztime=60s
func FuzzDecodeWAV(f *testing.F) {
	// Seed with a handful of valid and deliberately-malformed inputs so
	// the fuzzer explores meaningful structural mutations.
	f.Add(EncodeWAV(sineSamples(440, 0.01), testSampleRate, 1))
	f.Add(EncodeWAV(sineSamples(880, 0.02), testSampleRate, 2)) // stereo

	// Minimal 44-byte PCM header with zero-length data chunk
	minimal := make([]byte, 44)
	copy(minimal[0:4], "RIFF")
	binary.LittleEndian.PutUint32(minimal[4:8], 36)
	copy(minimal[8:12], "WAVE")
	copy(minimal[12:16], "fmt ")
	binary.LittleEndian.PutUint32(minimal[16:20], 16)
	binary.LittleEndian.PutUint16(minimal[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(minimal[22:24], 1)  // channels
	binary.LittleEndian.PutUint32(minimal[24:28], 44100)
	binary.LittleEndian.PutUint32(minimal[28:32], 88200)
	binary.LittleEndian.PutUint16(minimal[32:34], 2)
	binary.LittleEndian.PutUint16(minimal[34:36], 16)
	copy(minimal[36:40], "data")
	binary.LittleEndian.PutUint32(minimal[40:44], 0)
	f.Add(minimal)

	// Degenerate cases likely to expose bounds bugs
	f.Add([]byte{})
	f.Add([]byte("RIFF\x00\x00\x00\x00WAVE"))
	f.Add(make([]byte, 44))

	f.Fuzz(func(t *testing.T, data []byte) {
		// The contract: DecodeWAV never panics. Any unexpected panic is
		// logged by the fuzzer as a failure.
		_, _ = DecodeWAV(data)
	})
}
