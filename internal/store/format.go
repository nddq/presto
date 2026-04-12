package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Binary file format constants.
//
// v2 layout (current):
//
//	Header, 32 bytes fixed:
//	  [0:4]   magic "PRFP"
//	  [4:8]   version uint32 (2)
//	  [8:12]  songCount uint32
//	  [12:16] winSize uint32
//	  [16:20] hopSize uint32
//	  [20:21] windowFunc uint8
//	  [21:32] reserved (zero)
//
//	Per song:
//	  nameLen    uint16
//	  name       [nameLen]byte
//	  numFrames  uint32
//	  numHashes  uint32
//	  hashes     [numHashes]{hash uint32, offset uint32}  // little endian
//
// The hash array is laid out as back-to-back uint32 pairs so that the
// mmap Load path can cast the byte range directly to []fingerprint.HashEntry
// via unsafe.Slice with no copy.
const (
	magic      = "PRFP"
	version    = uint32(2)
	headerSize = 32
)

// Window function encoding for the file header.
const (
	winFuncNone     uint8 = 0
	winFuncHann     uint8 = 1
	winFuncHamming  uint8 = 2
	winFuncBartlett uint8 = 3
)

func encodeWindowFunc(name string) (uint8, error) {
	switch name {
	case "":
		return winFuncNone, nil
	case "hann":
		return winFuncHann, nil
	case "hamming":
		return winFuncHamming, nil
	case "bartlett":
		return winFuncBartlett, nil
	}
	return 0, fmt.Errorf("unknown window function %q", name)
}

func decodeWindowFunc(v uint8) (string, error) {
	switch v {
	case winFuncNone:
		return "", nil
	case winFuncHann:
		return "hann", nil
	case winFuncHamming:
		return "hamming", nil
	case winFuncBartlett:
		return "bartlett", nil
	}
	return "", fmt.Errorf("unknown window function code %d", v)
}

// fileHeader mirrors the on-disk header layout.
type fileHeader struct {
	SongCount  uint32
	WinSize    uint32
	HopSize    uint32
	WindowFunc uint8
	Algorithm  uint8 // 0 = constellation, 1 = subband
}

func writeHeader(w io.Writer, h fileHeader) error {
	buf := make([]byte, headerSize)
	copy(buf[0:4], magic)
	binary.LittleEndian.PutUint32(buf[4:8], version)
	binary.LittleEndian.PutUint32(buf[8:12], h.SongCount)
	binary.LittleEndian.PutUint32(buf[12:16], h.WinSize)
	binary.LittleEndian.PutUint32(buf[16:20], h.HopSize)
	buf[20] = h.WindowFunc
	buf[21] = h.Algorithm
	// bytes 22..32 reserved, already zero
	_, err := w.Write(buf)
	return err
}

// parseHeaderBytes decodes a file header from a byte slice.
func parseHeaderBytes(buf []byte) (fileHeader, error) {
	if len(buf) < headerSize {
		return fileHeader{}, errors.New("store file too small for header")
	}
	if string(buf[0:4]) != magic {
		return fileHeader{}, errors.New("not a presto fingerprint store (bad magic)")
	}
	v := binary.LittleEndian.Uint32(buf[4:8])
	if v != version {
		return fileHeader{}, fmt.Errorf("unsupported store version %d (expected %d)", v, version)
	}
	return fileHeader{
		SongCount:  binary.LittleEndian.Uint32(buf[8:12]),
		WinSize:    binary.LittleEndian.Uint32(buf[12:16]),
		HopSize:    binary.LittleEndian.Uint32(buf[16:20]),
		WindowFunc: buf[20],
		Algorithm:  buf[21],
	}, nil
}

// writeSongEntry serialises a single song entry to w.
// The fpData is opaque bytes whose layout depends on the algorithm —
// constellation uses packed (hash, offset) pairs; sub-band uses a flat
// byte array of frame data. Both are prefixed by numFrames and a
// dataLen uint32 so the reader knows how many bytes to consume.
func writeSongEntry(w io.Writer, name string, numFrames uint32, fpData []byte) error {
	if len(name) > 65535 {
		return fmt.Errorf("song name too long: %d bytes", len(name))
	}
	dataLen := uint32(len(fpData))

	var hdr [10]byte
	binary.LittleEndian.PutUint16(hdr[0:2], uint16(len(name)))
	if _, err := w.Write(hdr[:2]); err != nil {
		return err
	}
	if _, err := io.WriteString(w, name); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(hdr[2:6], numFrames)
	binary.LittleEndian.PutUint32(hdr[6:10], dataLen)
	if _, err := w.Write(hdr[2:10]); err != nil {
		return err
	}
	_, err := w.Write(fpData)
	return err
}

// parseSongEntryBytes decodes one song entry starting at data[pos].
// Returns the song name, frame count, a zero-copy byte slice for the
// FP payload, and the advanced position.
func parseSongEntryBytes(data []byte, pos int) (name string, numFrames uint32, fpBytes []byte, next int, err error) {
	if pos+2 > len(data) {
		return "", 0, nil, 0, errors.New("truncated nameLen")
	}
	nameLen := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if pos+nameLen > len(data) {
		return "", 0, nil, 0, errors.New("truncated name")
	}
	name = string(data[pos : pos+nameLen])
	pos += nameLen
	if pos+8 > len(data) {
		return "", 0, nil, 0, errors.New("truncated frame/data counts")
	}
	numFrames = binary.LittleEndian.Uint32(data[pos : pos+4])
	dataLen := binary.LittleEndian.Uint32(data[pos+4 : pos+8])
	pos += 8
	// Validate before the multiply.
	remaining := len(data) - pos
	if uint64(dataLen) > uint64(remaining) {
		return "", 0, nil, 0, errors.New("truncated fp data")
	}
	fpBytes = data[pos : pos+int(dataLen)]
	return name, numFrames, fpBytes, pos + int(dataLen), nil
}
