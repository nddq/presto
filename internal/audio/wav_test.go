package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

const testSampleRate = 44100

// sineSamples generates a pure tone as float64 samples in [-1, 1].
func sineSamples(freq, durationSec float64) []float64 {
	n := int(durationSec * float64(testSampleRate))
	out := make([]float64, n)
	for i := range out {
		out[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(testSampleRate))
	}
	return out
}

// roughlyEqual returns whether two samples are within tol of each other.
// PCM quantization noise is inherent to any lossy encode/decode roundtrip.
func roughlyEqual(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestEncodeDecodeRoundtrip16bit(t *testing.T) {
	original := sineSamples(440.0, 0.1) // 4410 samples
	buf := EncodeWAV(original, testSampleRate, 1)

	sig, err := DecodeWAV(buf)
	if err != nil {
		t.Fatalf("DecodeWAV returned error: %v", err)
	}
	if sig.SampleRate != testSampleRate {
		t.Errorf("sample rate: got %d, want %d", sig.SampleRate, testSampleRate)
	}
	if len(sig.Samples) != len(original) {
		t.Errorf("sample count: got %d, want %d", len(sig.Samples), len(original))
	}
	// 16-bit PCM has resolution of 1/32768 ≈ 3.05e-5. Allow 2 steps.
	const tol = 2.0 / 32768.0
	for i := range original {
		if !roughlyEqual(original[i], sig.Samples[i], tol) {
			t.Fatalf("sample %d: got %.6f want %.6f (diff %.6f)",
				i, sig.Samples[i], original[i], original[i]-sig.Samples[i])
		}
	}
}

func TestDecodeWAVRejectsShortInput(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte("RIFF"),
		bytes.Repeat([]byte{0}, 43), // one byte short of the minimum header
	}
	for _, c := range cases {
		if _, err := DecodeWAV(c); err == nil {
			t.Errorf("expected error for input of length %d", len(c))
		}
	}
}

func TestDecodeWAVRejectsBadMagic(t *testing.T) {
	buf := EncodeWAV(sineSamples(440, 0.01), testSampleRate, 1)
	// Corrupt the RIFF header
	copy(buf[0:4], "XXXX")
	if _, err := DecodeWAV(buf); err == nil {
		t.Error("expected error for bad RIFF magic")
	}

	buf2 := EncodeWAV(sineSamples(440, 0.01), testSampleRate, 1)
	copy(buf2[8:12], "XXXX")
	if _, err := DecodeWAV(buf2); err == nil {
		t.Error("expected error for bad WAVE magic")
	}
}

func TestDecodeWAVRejectsNonPCMFormat(t *testing.T) {
	buf := EncodeWAV(sineSamples(440, 0.01), testSampleRate, 1)
	// Audio format field is at offset 20, two bytes
	binary.LittleEndian.PutUint16(buf[20:22], 3) // IEEE float, not PCM
	if _, err := DecodeWAV(buf); err == nil {
		t.Error("expected error for non-PCM audio format")
	}
}

func TestDecodeWAVRejectsInvalidBitsPerSample(t *testing.T) {
	buf := EncodeWAV(sineSamples(440, 0.01), testSampleRate, 1)
	// Bits-per-sample is at offset 34, two bytes
	binary.LittleEndian.PutUint16(buf[34:36], 12) // unsupported depth
	if _, err := DecodeWAV(buf); err == nil {
		t.Error("expected error for unsupported bits per sample")
	}
}

func TestDecodeWAVRejectsMissingDataChunk(t *testing.T) {
	buf := EncodeWAV(sineSamples(440, 0.01), testSampleRate, 1)
	// Overwrite the "data" chunk header with a garbage chunk ID
	copy(buf[36:40], "junk")
	if _, err := DecodeWAV(buf); err == nil {
		t.Error("expected error for missing data chunk")
	}
}

func TestWriteReadWAVFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tone.wav")

	samples := sineSamples(440, 0.05)
	if err := WriteWAV(path, samples, testSampleRate, 1); err != nil {
		t.Fatal(err)
	}
	sig, err := ReadWAV(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig.Samples) != len(samples) {
		t.Errorf("sample count mismatch: got %d want %d", len(sig.Samples), len(samples))
	}
}

func TestDecodeWAVClampsOversizedChunkSize(t *testing.T) {
	// Build a WAV header where the data chunk advertises more bytes than
	// are actually present. DecodeWAV should clamp the declared size to
	// what's available rather than panicking.
	buf := EncodeWAV(sineSamples(440, 0.01), testSampleRate, 1)
	// Data subchunk size is at offset 40, four bytes
	binary.LittleEndian.PutUint32(buf[40:44], 0xFFFFFFFF)
	if _, err := DecodeWAV(buf); err != nil {
		t.Errorf("DecodeWAV should tolerate oversized chunk size, got: %v", err)
	}
}

// TestDecodeRealWAVFiles makes sure the decoder handles the actual test
// assets in testSamples/ if they have been extracted. Skips when missing.
func TestDecodeRealWAVFiles(t *testing.T) {
	dir := "../../testSamples"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skip("testSamples directory not present; skipping")
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".wav" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sig, err := DecodeWAV(data)
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if sig.SampleRate == 0 || len(sig.Samples) == 0 {
			t.Errorf("%s: empty signal", e.Name())
		}
	}
}
