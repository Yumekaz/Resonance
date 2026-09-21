package metadata

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unicode/utf16"
)

func corpusPath(name string) string { return filepath.Join("..", "..", "testdata", "metadata", name) }

func readFixture(t *testing.T, name string) Result {
	t.Helper()
	f, err := os.Open(corpusPath(name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result, _, err := Read(f)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCorpus(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"tagged.mp3", taggedMP3(t, "Test Title", "Test Artist", "Test Album", false)},
		{"tagged.flac", taggedFLAC(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, _, err := Read(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			if result.Title == nil || *result.Title != "Test Title" || result.Artist == nil || *result.Artist != "Test Artist" || result.Album == nil || *result.Album != "Test Album" {
				t.Fatalf("tags: %#v", result)
			}
			if result.AlbumArtist == nil || *result.AlbumArtist != "Album Credit" || result.Genre == nil || *result.Genre != "Jazz" || result.TrackNumber == nil || *result.TrackNumber != 2 || result.DiscNumber == nil || *result.DiscNumber != 1 {
				t.Fatalf("extended tags: %#v", result)
			}
			if result.DurationSec != nil || result.SampleRate != nil || result.Channels != nil || result.Bitrate != nil {
				t.Fatal("audio properties must remain absent")
			}
		})
	}
	t.Run("untagged.mp3", func(t *testing.T) {
		f, err := os.Open(corpusPath("untagged.mp3"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, _, err := Read(f); err == nil {
			t.Fatal("expected no-tags error")
		}
	})
	t.Run("untagged.flac", func(t *testing.T) {
		result := readFixture(t, "untagged.flac")
		if result.Title != nil || result.Artist != nil || result.Album != nil || result.Year != nil || result.Artwork != nil {
			t.Fatalf("invented metadata: %#v", result)
		}
	})
}

func id3Text(value string) []byte {
	buf := []byte{1, 0xff, 0xfe}
	for _, unit := range utf16.Encode([]rune(value)) {
		var pair [2]byte
		binary.LittleEndian.PutUint16(pair[:], unit)
		buf = append(buf, pair[:]...)
	}
	return buf
}

func id3Frame(name string, payload []byte) []byte {
	frame := make([]byte, 10, 10+len(payload))
	copy(frame, name)
	binary.BigEndian.PutUint32(frame[4:], uint32(len(payload)))
	return append(frame, payload...)
}

func syncsafe(n int) [4]byte {
	return [4]byte{byte(n >> 21 & 127), byte(n >> 14 & 127), byte(n >> 7 & 127), byte(n & 127)}
}

func taggedMP3(t testing.TB, title, artist, album string, art bool) []byte {
	t.Helper()
	base, err := os.ReadFile(corpusPath("untagged.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	frames := append(id3Frame("TIT2", id3Text(title)), id3Frame("TPE1", id3Text(artist))...)
	frames = append(frames, id3Frame("TALB", id3Text(album))...)
	frames = append(frames, id3Frame("TPE2", id3Text("Album Credit"))...)
	frames = append(frames, id3Frame("TRCK", id3Text("2/9"))...)
	frames = append(frames, id3Frame("TPOS", id3Text("1/2"))...)
	frames = append(frames, id3Frame("TCON", id3Text("Jazz"))...)
	if art {
		png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/dXcAAAAASUVORK5CYII=")
		if err != nil {
			t.Fatal(err)
		}
		picture := append([]byte{0}, []byte("image/png")...)
		picture = append(picture, 0, 3, 0)
		picture = append(picture, png...)
		frames = append(frames, id3Frame("APIC", picture)...)
	}
	header := []byte{'I', 'D', '3', 3, 0, 0}
	size := syncsafe(len(frames))
	header = append(header, size[:]...)
	return append(append(header, frames...), base...)
}

func unicodeArtworkMP3(t testing.TB) []byte {
	return taggedMP3(t, "夜の歌 ♫", "Björk", "Unicode Album", true)
}

func taggedFLAC(t testing.TB) []byte {
	t.Helper()
	base, err := os.ReadFile(corpusPath("untagged.flac"))
	if err != nil {
		t.Fatal(err)
	}
	position := 4
	for {
		last := base[position]&0x80 != 0
		length := int(base[position+1])<<16 | int(base[position+2])<<8 | int(base[position+3])
		if last {
			base[position] &^= 0x80
		}
		position += 4 + length
		if last {
			break
		}
	}
	var block []byte
	put := func(n uint32) { var b [4]byte; binary.LittleEndian.PutUint32(b[:], n); block = append(block, b[:]...) }
	put(4)
	block = append(block, "test"...)
	comments := []string{"TITLE=Test Title", "ARTIST=Test Artist", "ALBUM=Test Album", "ALBUMARTIST=Album Credit", "TRACKNUMBER=2", "DISCNUMBER=1", "GENRE=Jazz", "DATE=2024"}
	put(uint32(len(comments)))
	for _, comment := range comments {
		put(uint32(len(comment)))
		block = append(block, comment...)
	}
	header := []byte{0x84, byte(len(block) >> 16), byte(len(block) >> 8), byte(len(block))}
	result := append(append(append([]byte{}, base[:position]...), header...), block...)
	return append(result, base[position:]...)
}

func TestUnicodeAndArtwork(t *testing.T) {
	result, _, err := Read(bytes.NewReader(unicodeArtworkMP3(t)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Title == nil || *result.Title != "夜の歌 ♫" || result.Artist == nil || *result.Artist != "Björk" {
		t.Fatalf("unicode tags: %#v", result)
	}
	if result.Artwork == nil || result.Artwork.MIME != "image/png" || len(result.Artwork.Data) < 40 {
		t.Fatalf("artwork: %#v", result.Artwork)
	}
}

func TestFLACArtwork(t *testing.T) {
	base := taggedFLAC(t)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/dXcAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	position := 4
	for {
		last := base[position]&0x80 != 0
		length := int(base[position+1])<<16 | int(base[position+2])<<8 | int(base[position+3])
		if last {
			base[position] &^= 0x80
		}
		position += 4 + length
		if last {
			break
		}
	}
	block := make([]byte, 0)
	put := func(n uint32) { var b [4]byte; binary.BigEndian.PutUint32(b[:], n); block = append(block, b[:]...) }
	put(3)
	put(uint32(len("image/png")))
	block = append(block, "image/png"...)
	put(0)
	put(1)
	put(1)
	put(32)
	put(0)
	put(uint32(len(png)))
	block = append(block, png...)
	header := []byte{0x86, byte(len(block) >> 16), byte(len(block) >> 8), byte(len(block))}
	withPicture := append(append(append([]byte{}, base[:position]...), header...), block...)
	withPicture = append(withPicture, base[position:]...)
	result, _, err := Read(bytes.NewReader(withPicture))
	if err != nil {
		t.Fatal(err)
	}
	if result.Artwork == nil || result.Artwork.MIME != "image/png" || len(result.Artwork.Data) != len(png) {
		t.Fatalf("FLAC artwork: %#v", result.Artwork)
	}
}

func TestUnsupportedWAVAndMalformed(t *testing.T) {
	wav := make([]byte, 44+200)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 44100)
	binary.LittleEndian.PutUint32(wav[28:], 88200)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 200)
	if _, _, err := Read(bytes.NewReader(wav)); err == nil {
		t.Fatal("WAV tags were unexpectedly supported")
	}
	for _, content := range [][]byte{nil, []byte("garbage"), []byte("ID3\x03\x00\x00\x7f\x7f\x7f\x7f")} {
		if _, _, err := Read(bytes.NewReader(content)); err == nil {
			t.Fatalf("malformed accepted: %q", content)
		}
	}
}

func TestMalformedFLACPictureLengthIsRejectedBeforeAllocation(t *testing.T) {
	block := make([]byte, 32)
	binary.BigEndian.PutUint32(block[28:], 0x7fffffff)
	content := append([]byte("fLaC"), []byte{0x86, 0, 0, 32}...)
	content = append(content, block...)
	if _, _, err := Read(bytes.NewReader(content)); err != ErrMalformedMetadata {
		t.Fatalf("expected safe rejection, got %v", err)
	}
}

func TestLargeFileReadBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.mp3")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(unicodeArtworkMP3(t)); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result, readBytes, err := Read(f)
	if err != nil {
		t.Fatal(err)
	}
	if result.Title == nil || readBytes >= 1<<20 {
		t.Fatalf("large file read %d bytes", readBytes)
	}
	t.Logf("64 MiB sparse MP3: parser read %d bytes", readBytes)
}

func BenchmarkMetadataSparseMP3(b *testing.B) {
	path := filepath.Join(b.TempDir(), "large.mp3")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	base := taggedMP3(b, "Test Title", "Test Artist", "Test Album", false)
	if _, err := f.Write(base); err != nil {
		b.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f.Seek(0, 0); err != nil {
			b.Fatal(err)
		}
		_, readBytes, err := Read(f)
		if err != nil || readBytes > 1<<20 {
			b.Fatalf("read=%d error=%v", readBytes, err)
		}
	}
	b.StopTimer()
	runtime.KeepAlive(f)
}
