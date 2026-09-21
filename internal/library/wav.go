package library

import (
	"encoding/binary"
	"errors"
	"io"
)

var errMalformedWAV = errors.New("malformed or unsupported PCM WAV")

// validatePCM checks the small RIFF envelope needed for first-generation WAV
// import. It does not decode samples or read the data chunk into memory.
func validatePCM(r io.ReadSeeker, fileSize int64) error {
	if fileSize < 44 {
		return errMalformedWAV
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var header [12]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return errMalformedWAV
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return errMalformedWAV
	}
	limit := int64(binary.LittleEndian.Uint32(header[4:])) + 8
	if limit > fileSize || limit < 44 {
		return errMalformedWAV
	}
	var foundFmt, foundData bool
	for chunks := 0; chunks < 128; chunks++ {
		position, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		if position == limit {
			break
		}
		if limit-position < 8 {
			return errMalformedWAV
		}
		var chunk [8]byte
		if _, err := io.ReadFull(r, chunk[:]); err != nil {
			return errMalformedWAV
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		start := position + 8
		end := start + size + (size & 1)
		if end > limit {
			return errMalformedWAV
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if size < 16 {
				return errMalformedWAV
			}
			var format [16]byte
			if _, err := io.ReadFull(r, format[:]); err != nil {
				return errMalformedWAV
			}
			channels := binary.LittleEndian.Uint16(format[2:])
			bits := binary.LittleEndian.Uint16(format[14:])
			align := binary.LittleEndian.Uint16(format[12:])
			if binary.LittleEndian.Uint16(format[:]) != 1 || channels == 0 || channels > 8 || binary.LittleEndian.Uint32(format[4:]) == 0 || (bits != 8 && bits != 16 && bits != 24 && bits != 32) || int(align) != int(channels)*int(bits)/8 {
				return errMalformedWAV
			}
			foundFmt = true
		case "data":
			if size > 0 {
				foundData = true
			}
		}
		if _, err := r.Seek(end, io.SeekStart); err != nil {
			return err
		}
	}
	if !foundFmt || !foundData {
		return errMalformedWAV
	}
	return nil
}
