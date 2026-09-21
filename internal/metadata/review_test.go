package metadata

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"os"
	"runtime"
	"testing"
)

func reviewCommentFLAC(comments ...string) []byte {
	var block bytes.Buffer
	binary.Write(&block, binary.LittleEndian, uint32(0))
	binary.Write(&block, binary.LittleEndian, uint32(len(comments)))
	for _, s := range comments {
		binary.Write(&block, binary.LittleEndian, uint32(len(s)))
		block.WriteString(s)
	}
	n := block.Len()
	return append(append([]byte("fLaC"), 0x84, byte(n>>16), byte(n>>8), byte(n)), block.Bytes()...)
}

func TestReviewNestedPictureAllocation(t *testing.T) {
	picture := make([]byte, 32)
	binary.BigEndian.PutUint32(picture[28:], 32<<20) // controlled 32 MiB, not an OOM payload
	content := reviewCommentFLAC("METADATA_BLOCK_PICTURE=" + base64.StdEncoding.EncodeToString(picture))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, reads, err := Read(bytes.NewReader(content))
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("input=%d read=%d allocated=%d error=%v", len(content), reads, allocated, err)
	if err == nil || allocated > 2<<20 {
		t.Fatalf("nested artwork was not safely rejected: allocated=%d error=%v", allocated, err)
	}
}

func TestReviewInvalidDateStaysNull(t *testing.T) {
	result, _, err := Read(bytes.NewReader(reviewCommentFLAC("DATE=not-a-date")))
	if err != nil {
		t.Fatal(err)
	}
	if result.Year != nil {
		t.Fatalf("fabricated year %d", *result.Year)
	}
}

func TestReviewDuplicateFrameWorkBound(t *testing.T) {
	frames := bytes.Repeat(id3Frame("TIT2", []byte{0, 'x'}), 2048)
	size := syncsafe(len(frames))
	content := append([]byte{'I', 'D', '3', 3, 0, 0}, size[:]...)
	content = append(content, frames...)
	if _, _, err := Read(bytes.NewReader(content)); err == nil {
		t.Fatal("excessive duplicate frames accepted without a work limit")
	}
}

func TestReviewContainerLimits(t *testing.T) {
	// An ID3 frame may not escape the enclosing tag into audio bytes.
	size := syncsafe(1)
	content := append([]byte{'I', 'D', '3', 3, 0, 0}, size[:]...)
	content = append(content, id3Frame("TIT2", id3Text("outside tag"))...)
	if _, _, err := Read(bytes.NewReader(content)); err == nil {
		t.Fatal("truncated declared tag accepted")
	}
}

func TestReviewYearAndUnicodeFLAC(t *testing.T) {
	for _, tc := range []struct {
		date string
		year int
	}{{"2024", 2024}, {"2024-02-29", 2024}, {"2024-13-01", 0}, {"2024-99", 0}, {"garbage", 0}, {"", 0}} {
		result, _, err := Read(bytes.NewReader(reviewCommentFLAC("TITLE=夜の歌 ♫", "ARTIST=Björk", "DATE="+tc.date)))
		if err != nil {
			t.Fatal(err)
		}
		if result.Title == nil || *result.Title != "夜の歌 ♫" || result.Artist == nil || *result.Artist != "Björk" {
			t.Fatal("Unicode lost")
		}
		if (tc.year == 0 && result.Year != nil) || (tc.year != 0 && (result.Year == nil || *result.Year != tc.year)) {
			t.Fatalf("date %q: %v", tc.date, result.Year)
		}
	}
	frames := id3Frame("TYER", id3Text("2024"))
	size := syncsafe(len(frames))
	data := append([]byte{'I', 'D', '3', 3, 0, 0}, size[:]...)
	data = append(data, frames...)
	result, _, err := Read(bytes.NewReader(data))
	if err != nil || result.Year == nil || *result.Year != 2024 {
		t.Fatalf("MP3 year not verified: %v %v", result.Year, err)
	}
}

func TestReviewAdditionalID3Variants(t *testing.T) {
	data := taggedMP3(t, "Unicode 夜", "Artist", "Album", false)
	data[3] = 4 // These short frame lengths are also valid syncsafe v2.4 sizes.
	result, _, err := Read(bytes.NewReader(data))
	if err != nil || result.Title == nil || *result.Title != "Unicode 夜" {
		t.Fatalf("ID3v2.4: %#v %v", result, err)
	}
	base, err := os.ReadFile(corpusPath("untagged.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	v1 := make([]byte, 128)
	copy(v1, "TAG")
	copy(v1[3:33], "ID3v1 Title")
	copy(v1[93:97], "2024")
	result, _, err = Read(bytes.NewReader(append(base, v1...)))
	if err != nil || result.Title == nil || *result.Title != "ID3v1 Title" || result.Year == nil || *result.Year != 2024 {
		t.Fatalf("ID3v1: %#v %v", result, err)
	}
}

func TestReviewUnsupportedDispatchAndFlags(t *testing.T) {
	for _, data := range [][]byte{[]byte("OggS0123456789"), []byte("0000ftyp0123456789"), []byte("DSD 0123456789"), []byte("RIFF0000WAVE0123456789")} {
		if _, _, err := Read(bytes.NewReader(data)); err != ErrUnsupportedMetadata {
			t.Fatalf("unreviewed dispatch: %v", err)
		}
	}
	data := taggedMP3(t, "Title", "Artist", "Album", false)
	data[5] = 0x80
	if _, _, err := Read(bytes.NewReader(data)); err != ErrUnsupportedMetadata {
		t.Fatalf("unreviewed flags: %v", err)
	}
}

func reviewArtworkMP3(n int) []byte {
	picture := append([]byte{0}, []byte("image/png\x00\x03\x00")...)
	picture = append(picture, make([]byte, n)...)
	picture[len(picture)-n] = 0x89 // PNG's nonzero first byte, not a text terminator.
	frames := id3Frame("APIC", picture)
	size := syncsafe(len(frames))
	data := append([]byte{'I', 'D', '3', 3, 0, 0}, size[:]...)
	return append(data, frames...)
}

func TestReviewArtworkSizeLimits(t *testing.T) {
	for _, n := range []int{1 << 20, 3 << 20} {
		data := reviewArtworkMP3(n)
		result, reads, err := Read(bytes.NewReader(data))
		if n < 2<<20 {
			if err != nil || result.Artwork == nil || len(result.Artwork.Data) != n {
				t.Fatalf("bounded art: %v", err)
			}
		} else if err == nil || reads > 1024 {
			t.Fatalf("oversize allocated/read: %d %v", reads, err)
		}
	}
}

func FuzzMetadataEnvelope(f *testing.F) {
	f.Add([]byte("fLaC\x84\x00\x00\x08\x00\x00\x00\x00\x00\x00\x00\x00"))
	f.Add([]byte("ID3\x03\x00\x00\x00\x00\x00\x0cTIT2\x00\x00\x00\x02\x00\x00\x00x"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 16384 {
			t.Skip()
		}
		_, read, _ := Read(bytes.NewReader(data))
		if read > MaxReadBytes {
			t.Fatal("read ceiling exceeded")
		}
	})
}

func BenchmarkReviewArtwork(b *testing.B) {
	data := reviewArtworkMP3(1 << 20)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Read(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReviewRejectedDuplicates(b *testing.B) {
	frames := bytes.Repeat(id3Frame("TIT2", []byte{0, 'x'}), 2048)
	size := syncsafe(len(frames))
	data := append([]byte{'I', 'D', '3', 3, 0, 0}, size[:]...)
	data = append(data, frames...)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Read(bytes.NewReader(data)); err == nil {
			b.Fatal("expected limit error")
		}
	}
}
