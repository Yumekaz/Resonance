package metadata

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"
)

const (
	MaxMetadataBytes int64 = 4 << 20
	MaxArtworkBytes  int64 = 2 << 20
	MaxTextBytes     int64 = 64 << 10
	MaxTagEntries          = 1024
)

// This is an admission check for the evaluated subset, not another tag parser.
// No unreviewed OGG/MP4/DSF dispatch or encoded ID3 extensions reach the library.
func validateEnvelope(r *boundedReadSeeker) error {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return ErrMalformedMetadata
	}
	switch {
	case string(magic[:]) == "fLaC":
		return validateFLACPictures(r)
	case string(magic[:3]) == "ID3":
		return validateID3(r)
	case magic[0] == 0xff && magic[1]&0xe0 == 0xe0:
		return nil // ID3v1 or explicit no-tags result; no audio properties inferred.
	default:
		return ErrUnsupportedMetadata
	}
}

func syncsafeSize(p []byte) (int64, error) {
	var n int64
	for _, b := range p {
		if b&0x80 != 0 {
			return 0, ErrMalformedMetadata
		}
		n = n<<7 | int64(b)
	}
	return n, nil
}

func validateID3(r *boundedReadSeeker) error {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var header [10]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return ErrMalformedMetadata
	}
	if (header[3] != 3 && header[3] != 4) || header[5] != 0 {
		return ErrUnsupportedMetadata
	}
	size, err := syncsafeSize(header[6:])
	if err != nil || size > MaxMetadataBytes {
		return ErrMalformedMetadata
	}
	fileEnd, err := r.Seek(0, io.SeekEnd)
	if err != nil || size+10 > fileEnd {
		return ErrMalformedMetadata
	}
	end := size + 10
	for pos, frames := int64(10), 0; pos < end; frames++ {
		if frames >= MaxTagEntries {
			return ErrMalformedMetadata
		}
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return err
		}
		var frame [10]byte
		if _, err := io.ReadFull(r, frame[:min(int64(10), end-pos)]); err != nil {
			return ErrMalformedMetadata
		}
		if frame[0] == 0 {
			// Require all remaining padding to be zero, with bounded scratch space.
			if _, err := r.Seek(pos, io.SeekStart); err != nil {
				return err
			}
			var padding [4096]byte
			for pos < end {
				n := min(int64(len(padding)), end-pos)
				if _, err := io.ReadFull(r, padding[:n]); err != nil {
					return err
				}
				for _, b := range padding[:n] {
					if b != 0 {
						return ErrMalformedMetadata
					}
				}
				pos += n
			}
			return nil
		}
		if end-pos < 10 {
			return ErrMalformedMetadata
		}
		for _, b := range frame[:4] {
			if !(b >= 'A' && b <= 'Z') && !(b >= '0' && b <= '9') {
				return ErrMalformedMetadata
			}
		}
		// Compression/encryption/unsynchronisation/grouping are not evaluated.
		if frame[8] != 0 || frame[9] != 0 {
			return ErrUnsupportedMetadata
		}
		n := int64(binary.BigEndian.Uint32(frame[4:8]))
		if header[3] == 4 {
			n, err = syncsafeSize(frame[4:8])
			if err != nil {
				return err
			}
		}
		if n <= 0 || n > end-pos-10 {
			return ErrMalformedMetadata
		}
		limit := MaxTextBytes
		if string(frame[:4]) == "APIC" {
			limit = MaxArtworkBytes
		}
		if n > limit {
			return ErrMalformedMetadata
		}
		pos += 10 + n
	}
	return nil
}

func validateComments(r *boundedReadSeeker, end int64) error {
	readSize := func() (int64, error) {
		pos, err := r.Seek(0, io.SeekCurrent)
		if err != nil || end-pos < 4 {
			return 0, ErrMalformedMetadata
		}
		var b [4]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return 0, ErrMalformedMetadata
		}
		return int64(binary.LittleEndian.Uint32(b[:])), nil
	}
	readString := func(limit int64) (string, error) {
		n, err := readSize()
		if err != nil {
			return "", err
		}
		pos, err := r.Seek(0, io.SeekCurrent)
		if err != nil || n > end-pos || n > limit {
			return "", ErrMalformedMetadata
		}
		b := make([]byte, n)
		if _, err = io.ReadFull(r, b); err != nil {
			return "", err
		}
		return string(b), nil
	}
	if _, err := readString(MaxTextBytes); err != nil {
		return err
	}
	count, err := readSize()
	if err != nil || count > MaxTagEntries {
		return ErrMalformedMetadata
	}
	for i := int64(0); i < count; i++ {
		comment, err := readString(MaxMetadataBytes)
		if err != nil {
			return err
		}
		key, value, ok := strings.Cut(comment, "=")
		if !ok {
			return ErrMalformedMetadata
		}
		if strings.EqualFold(key, "metadata_block_picture") {
			if int64(base64.StdEncoding.DecodedLen(len(value))) > MaxArtworkBytes+MaxTextBytes {
				return ErrMalformedMetadata
			}
			data, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return ErrMalformedMetadata
			}
			picture := &boundedReadSeeker{inner: bytes.NewReader(data)}
			if err := validatePictureLengths(picture, int64(len(data))); err != nil {
				return err
			}
		} else if int64(len(comment)) > MaxTextBytes {
			return ErrMalformedMetadata
		}
	}
	pos, err := r.Seek(0, io.SeekCurrent)
	if err != nil || pos != end {
		return ErrMalformedMetadata
	}
	return nil
}
