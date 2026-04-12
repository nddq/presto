// Package audio provides WAV file reading and writing for PCM audio data.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"unsafe"
)

// isAligned reports whether p is aligned to the given byte boundary.
// Used to gate unsafe.Slice casts over byte buffers: if a buffer happens
// to start at an aligned offset we take the zero-copy fast path; if not
// we fall back to a scalar binary.LittleEndian loop. Strict-alignment
// architectures never see an unaligned unsafe read this way.
func isAligned(p unsafe.Pointer, align uintptr) bool {
	return uintptr(p)%align == 0
}

// Signal holds decoded audio samples and their sample rate.
type Signal struct {
	SampleRate int
	Samples    []float64
}

// ReadWAV reads a PCM WAV file and returns the decoded signal with samples
// normalized to the range [-1, 1]. Supports 8, 16, 24, and 32-bit sample
// depths. Multi-channel files are read as interleaved samples.
func ReadWAV(filename string) (*Signal, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return DecodeWAV(data)
}

// DecodeWAV parses in-memory PCM WAV bytes and returns the decoded signal
// with samples normalized to the range [-1, 1]. Supports 8, 16, 24, and
// 32-bit sample depths. Multi-channel files are read as interleaved samples.
func DecodeWAV(data []byte) (*Signal, error) {
	if len(data) < 44 {
		return nil, errors.New("wav: file too short")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("wav: not a RIFF/WAVE file")
	}

	var sampleRate, bitsPerSample int
	var audioData []byte

	pos := 12
	for pos+8 <= len(data) {
		chunkID := string(data[pos : pos+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		if chunkSize < 0 {
			return nil, errors.New("wav: negative chunk size")
		}
		chunkData := data[pos+8:]
		if chunkSize > len(chunkData) {
			chunkSize = len(chunkData)
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, errors.New("wav: fmt chunk too small")
			}
			if binary.LittleEndian.Uint16(chunkData[0:2]) != 1 {
				return nil, errors.New("wav: only PCM format supported")
			}
			sampleRate = int(binary.LittleEndian.Uint32(chunkData[4:8]))
			bitsPerSample = int(binary.LittleEndian.Uint16(chunkData[14:16]))
		case "data":
			audioData = chunkData[:chunkSize]
		}
		pos += 8 + chunkSize
		if chunkSize%2 != 0 {
			pos++
		}
	}
	if audioData == nil {
		return nil, errors.New("wav: no data chunk found")
	}

	bytesPerSample := bitsPerSample / 8
	if bytesPerSample == 0 {
		return nil, errors.New("wav: invalid bits per sample")
	}
	numSamples := len(audioData) / bytesPerSample
	samples := make([]float64, numSamples)
	if numSamples == 0 {
		// Valid header with no sample data — return an empty signal.
		return &Signal{SampleRate: sampleRate, Samples: samples}, nil
	}

	// Hot decode paths: take the zero-alloc unsafe.Slice fast path when
	// audioData happens to land on an aligned boundary (the common case —
	// well-formed WAVs put data at offset 44, which is 4-aligned). Fall
	// back to a scalar binary.LittleEndian loop when it doesn't, so the
	// unsafe path never reads unaligned memory.
	base := unsafe.Pointer(&audioData[0])
	switch bitsPerSample {
	case 8:
		for i := range numSamples {
			samples[i] = (float64(audioData[i]) - 128.0) / 128.0
		}
	case 16:
		const scale = 1.0 / 32768.0
		if isAligned(base, 2) {
			i16s := unsafe.Slice((*int16)(base), numSamples)
			for i, v := range i16s {
				samples[i] = float64(v) * scale
			}
		} else {
			for i := range numSamples {
				v := int16(binary.LittleEndian.Uint16(audioData[i*2 : i*2+2]))
				samples[i] = float64(v) * scale
			}
		}
	case 24:
		for i := range numSamples {
			off := i * 3
			val := int(audioData[off]) | int(audioData[off+1])<<8 | int(audioData[off+2])<<16
			if val >= 0x800000 {
				val -= 0x1000000
			}
			samples[i] = float64(val) / 8388608.0
		}
	case 32:
		const scale = 1.0 / 2147483648.0
		if isAligned(base, 4) {
			i32s := unsafe.Slice((*int32)(base), numSamples)
			for i, v := range i32s {
				samples[i] = float64(v) * scale
			}
		} else {
			for i := range numSamples {
				v := int32(binary.LittleEndian.Uint32(audioData[i*4 : i*4+4]))
				samples[i] = float64(v) * scale
			}
		}
	default:
		return nil, fmt.Errorf("wav: unsupported bits per sample %d", bitsPerSample)
	}

	return &Signal{SampleRate: sampleRate, Samples: samples}, nil
}

// WriteWAV encodes float64 samples as a 16-bit PCM WAV file.
// Samples should be in the range [-1, 1]; values outside this range are clamped.
func WriteWAV(filename string, samples []float64, sampleRate, numChannels int) error {
	return os.WriteFile(filename, EncodeWAV(samples, sampleRate, numChannels), 0644)
}

// EncodeWAV returns the 16-bit PCM WAV byte representation of samples.
// Samples should be in the range [-1, 1]; values outside this range are clamped.
func EncodeWAV(samples []float64, sampleRate, numChannels int) []byte {
	numSamples := len(samples)
	dataSize := numSamples * 2
	buf := make([]byte, 44+dataSize)

	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")

	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], uint16(numChannels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*numChannels*2))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(numChannels*2))
	binary.LittleEndian.PutUint16(buf[34:36], 16)

	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	// Encode through an aligned int16 view of the PCM region. buf[44:]
	// is at a fixed 4-aligned offset from the start of a freshly
	// allocated byte slice, so the unsafe cast is always safe here.
	pcm := unsafe.Slice((*int16)(unsafe.Pointer(&buf[44])), numSamples)
	for i, s := range samples {
		v := s * 32767.0
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		pcm[i] = int16(v)
	}
	return buf
}
