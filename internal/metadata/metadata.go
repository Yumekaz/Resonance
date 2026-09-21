// Package metadata evaluates bounded tag extraction for the M1 catalog.
// It does not decode audio or infer absent fields from filenames.
package metadata

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"time"

	"github.com/dhowden/tag"
)

const MaxReadBytes int64 = 8 << 20

var ErrReadBudget = errors.New("metadata read budget exceeded")
var ErrMalformedMetadata = errors.New("malformed or oversized metadata block")
var ErrUnsupportedMetadata = errors.New("unsupported metadata format or encoding")

type Artwork struct {
	MIME string
	Data []byte
}

type Result struct {
	Format      string
	Title       *string
	Artist      *string
	Album       *string
	Year        *int
	Artwork     *Artwork
	DurationSec *float64
	SampleRate  *int
	Channels    *int
	Bitrate     *int
}

type boundedReadSeeker struct {
	inner io.ReadSeeker
	read  int64
}

func (b *boundedReadSeeker) Read(p []byte) (int, error) {
	if b.read >= MaxReadBytes {
		return 0, ErrReadBudget
	}
	if int64(len(p)) > MaxReadBytes-b.read {
		p = p[:int(MaxReadBytes-b.read)]
	}
	n, err := b.inner.Read(p)
	b.read += int64(n)
	return n, err
}

func (b *boundedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return b.inner.Seek(offset, whence)
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// Read extracts tag fields with a cumulative read ceiling. Unsupported and
// malformed formats return an error; no guessed title or audio property is set.
func Read(source io.ReadSeeker) (Result, int64, error) {
	reader := &boundedReadSeeker{inner: source}
	if err := validateEnvelope(reader); err != nil {
		return Result{}, reader.read, err
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return Result{}, reader.read, err
	}
	m, err := tag.ReadFrom(reader)
	if err != nil {
		return Result{}, reader.read, err
	}
	result := Result{
		Format: string(m.FileType()),
		Title:  nullableString(m.Title()),
		Artist: nullableString(m.Artist()),
		Album:  nullableString(m.Album()),
	}
	if year := metadataYear(m); year > 0 && year <= 9999 {
		result.Year = &year
	}
	if picture := m.Picture(); picture != nil {
		result.Artwork = &Artwork{MIME: picture.MIMEType, Data: picture.Data}
	}
	return result, reader.read, nil
}

func metadataYear(m tag.Metadata) int {
	// The dependency returns time.Time{}.Year() == 1 for malformed Vorbis dates.
	// Do not turn a failed date parse into an invented catalog value.
	if m.FileType() == tag.FLAC {
		raw := m.Raw()
		date, _ := raw["date"].(string)
		if date == "" {
			date, _ = raw["year"].(string)
		}
		for _, layout := range []string{"2006", "2006-01", "2006-01-02"} {
			if parsed, err := time.Parse(layout, date); err == nil {
				return parsed.Year()
			}
		}
		return 0
	}
	// The supported ID3 variants return zero for absent/unparseable year tags.
	return m.Year()
}

// dhowden/tag reads a declared FLAC picture data length into a newly allocated
// slice without checking the enclosing block length. Validate that one length
// before entering the library; this inspects headers, not encoded audio.
func validateFLACPictures(r *boundedReadSeeker) error {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return err
	}
	if !bytes.Equal(magic[:], []byte("fLaC")) {
		return nil
	}
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := r.Seek(4, io.SeekStart); err != nil {
		return err
	}
	for blocks := 0; blocks < MaxTagEntries; blocks++ {
		var header [4]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return ErrMalformedMetadata
		}
		length := int64(header[1])<<16 | int64(header[2])<<8 | int64(header[3])
		start, err := r.Seek(0, io.SeekCurrent)
		if err != nil || start+length > end || start+length > MaxMetadataBytes+4 {
			return ErrMalformedMetadata
		}
		blockEnd := start + length
		if header[0]&0x7f == 6 {
			if length > MaxReadBytes {
				return ErrMalformedMetadata
			}
			if err := validatePictureLengths(r, blockEnd); err != nil {
				return err
			}
		}
		if header[0]&0x7f == 4 {
			if err := validateComments(r, blockEnd); err != nil {
				return err
			}
		}
		if _, err := r.Seek(blockEnd, io.SeekStart); err != nil {
			return err
		}
		if header[0]&0x80 != 0 {
			return nil
		}
	}
	return ErrMalformedMetadata
}

func validatePictureLengths(r *boundedReadSeeker, blockEnd int64) error {
	readUint32 := func() (uint32, error) {
		position, err := r.Seek(0, io.SeekCurrent)
		if err != nil || blockEnd-position < 4 {
			return 0, ErrMalformedMetadata
		}
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, ErrMalformedMetadata
		}
		return binary.BigEndian.Uint32(b[:]), nil
	}
	if pictureType, err := readUint32(); err != nil || pictureType > 20 {
		return ErrMalformedMetadata
	} // picture type
	for i := 0; i < 2; i++ { // MIME and description strings
		size, err := readUint32()
		if err != nil {
			return err
		}
		position, err := r.Seek(0, io.SeekCurrent)
		if err != nil || int64(size) > blockEnd-position || int64(size) > MaxTextBytes {
			return ErrMalformedMetadata
		}
		if _, err := r.Seek(int64(size), io.SeekCurrent); err != nil {
			return err
		}
	}
	for i := 0; i < 4; i++ { // width, height, depth, colors
		if _, err := readUint32(); err != nil {
			return err
		}
	}
	dataSize, err := readUint32()
	if err != nil {
		return err
	}
	position, err := r.Seek(0, io.SeekCurrent)
	if err != nil || int64(dataSize) != blockEnd-position || int64(dataSize) > MaxArtworkBytes {
		return ErrMalformedMetadata
	}
	return nil
}
