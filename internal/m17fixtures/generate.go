// Package m17fixtures builds reproducible, legally sourced M1.7 verification
// corpora. It is tooling only; generated files are never application state.
package m17fixtures

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"resonance/internal/metadata"
)

const ScaleTrackCount = 10_000

type Entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Format string `json:"format"`
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
	Role   string `json:"role"`
}

type Summary struct {
	FormatVersion      int               `json:"format_version"`
	Mode               string            `json:"mode"`
	Seed               uint64            `json:"seed,omitempty"`
	TrackCount         int               `json:"track_count"`
	UniqueSHA256       int               `json:"unique_sha256"`
	ArtistCount        int               `json:"artist_count"`
	AlbumCount         int               `json:"album_count"`
	FileCount          int               `json:"file_count"`
	TotalBytes         int64             `json:"total_bytes"`
	AudioSourceDetails string            `json:"audio_source_details"`
	AudioSources       map[string]string `json:"audio_sources_sha256"`
}

type writer struct {
	root     string
	manifest *os.File
	entries  []Entry
	hashes   map[string]struct{}
	total    int64
}

func newWriter(root string) (*writer, error) {
	if err := os.MkdirAll(filepath.Dir(root), 0755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(root, 0755); err != nil {
		return nil, fmt.Errorf("output must be a new directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(root, "manifest.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &writer{root: root, manifest: f, hashes: make(map[string]struct{})}, nil
}

func (w *writer) add(relative string, data []byte, format, title, artist, album, role string) error {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe fixture-relative path")
	}
	path := filepath.Join(w.root, clean)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	entry := Entry{Path: filepath.ToSlash(clean), SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data)), Format: format, Title: title, Artist: artist, Album: album, Role: role}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err = w.manifest.Write(append(encoded, '\n')); err != nil {
		return err
	}
	w.entries = append(w.entries, entry)
	w.hashes[entry.SHA256] = struct{}{}
	w.total += entry.Bytes
	return nil
}

func (w *writer) finish(summary Summary) (Summary, error) {
	if err := w.manifest.Sync(); err != nil {
		_ = w.manifest.Close()
		return summary, err
	}
	if err := w.manifest.Close(); err != nil {
		return summary, err
	}
	summary.FormatVersion = 1
	summary.UniqueSHA256 = len(w.hashes)
	summary.FileCount = len(w.entries)
	summary.TotalBytes = w.total
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(w.root, "summary.json"), append(encoded, '\n'), 0644); err != nil {
		return summary, err
	}
	return summary, nil
}

// GenerateScale creates a small tagged MP3 scale set. The file bytes vary by
// unique ID3 tags and deterministic contiguous windows from the licensed MP3
// source; every path, metadata identity and full-file hash is unique.
func GenerateScale(root string, sourceMP3 []byte, count int, seed uint64) (Summary, error) {
	if count < 1 || count > 100_000 {
		return Summary{}, errors.New("track count must be in 1..100000")
	}
	frames, err := mpegFrameWindows(sourceMP3, 4)
	if err != nil {
		return Summary{}, err
	}
	w, err := newWriter(root)
	if err != nil {
		return Summary{}, err
	}
	defer w.manifest.Close()
	sourceHash := sha256.Sum256(sourceMP3)
	artists, albums := map[string]bool{}, map[string]bool{}
	for i := 0; i < count; i++ {
		artistNumber := i / 100
		albumNumber := (i / 10) % 10
		title := fmt.Sprintf("Scale Track %05d", i)
		artist := fmt.Sprintf("Scale Artist %03d", artistNumber)
		album := fmt.Sprintf("Scale Album %02d", albumNumber)
		frameStart := int((seed + uint64(i)*1_103_515_245 + uint64(i)*uint64(i)*12_345) % uint64(len(frames)))
		content := append(id3v23(title, artist, album, artist, i%10+1), frames[frameStart]...)
		parsed, _, parseErr := metadata.Read(bytes.NewReader(content))
		if parseErr != nil {
			return Summary{}, fmt.Errorf("generated MP3 tags failed validation at track %d: %w", i, parseErr)
		}
		if parsed.Title == nil || *parsed.Title != title || parsed.Artist == nil || *parsed.Artist != artist || parsed.Album == nil || *parsed.Album != album {
			return Summary{}, fmt.Errorf("generated MP3 tags did not match track %d", i)
		}
		relative := filepath.ToSlash(filepath.Join(artist, album, fmt.Sprintf("%s.mp3", title)))
		if err := w.add(relative, content, "mp3", title, artist, album, "scale-track"); err != nil {
			return Summary{}, err
		}
		artists[artist] = true
		albums[artist+"\x00"+album] = true
	}
	return w.finish(Summary{Mode: "scale", Seed: seed, TrackCount: count, ArtistCount: len(artists), AlbumCount: len(albums), AudioSourceDetails: "CC0 testdata/metadata/untagged.mp3; four complete source MPEG frames per fixture", AudioSources: map[string]string{"untagged.mp3": hex.EncodeToString(sourceHash[:])}})
}

// GenerateJourney builds a separate playable set. The long WAV is realistic
// for seeking and streaming; scale fixtures stay short by construction.
func GenerateJourney(root string, sourceMP3, sourceFLAC []byte) (Summary, error) {
	w, err := newWriter(root)
	if err != nil {
		return Summary{}, err
	}
	defer w.manifest.Close()
	mp3Hash, flacHash := sha256.Sum256(sourceMP3), sha256.Sum256(sourceFLAC)
	addTaggedMP3 := func(relative, title, artist, album, albumArtist, role string, audio []byte) error {
		content := append(id3v23(title, artist, album, albumArtist, 1), audio...)
		parsed, _, e := metadata.Read(bytes.NewReader(content))
		if e != nil {
			return fmt.Errorf("generated journey MP3 failed metadata validation: %w", e)
		}
		if parsed.Title == nil || *parsed.Title != title || parsed.Artist == nil || *parsed.Artist != artist || parsed.Album == nil || *parsed.Album != album {
			return errors.New("generated journey MP3 tags did not match fixture")
		}
		return w.add(relative, content, "mp3", title, artist, album, role)
	}
	if err := addTaggedMP3("library/Browser Artist/Browser Album/Browser Song.mp3", "Browser Song", "Browser Artist", "Browser Album", "Browser Artist", "tagged-playable-mp3", sourceMP3); err != nil {
		return Summary{}, err
	}
	duplicate, err := os.ReadFile(filepath.Join(root, "library", "Browser Artist", "Browser Album", "Browser Song.mp3"))
	if err != nil {
		return Summary{}, err
	}
	if err := w.add("library/Exact Copy/Browser Song Copy.mp3", duplicate, "mp3", "Browser Song", "Browser Artist", "Browser Album", "exact-byte-copy"); err != nil {
		return Summary{}, err
	}
	flac, err := withFLACComments(sourceFLAC, []string{"TITLE=Tagged FLAC", "ARTIST=FLAC Artist", "ALBUM=FLAC Album", "ALBUMARTIST=FLAC Artist", "TRACKNUMBER=1", "DATE=2024"})
	if err != nil {
		return Summary{}, err
	}
	parsedFLAC, _, err := metadata.Read(bytes.NewReader(flac))
	if err != nil {
		return Summary{}, fmt.Errorf("generated journey FLAC failed metadata validation: %w", err)
	}
	if parsedFLAC.Title == nil || *parsedFLAC.Title != "Tagged FLAC" {
		return Summary{}, errors.New("generated journey FLAC title did not match fixture")
	}
	if err := w.add("library/FLAC Artist/FLAC Album/Tagged FLAC.flac", flac, "flac", "Tagged FLAC", "FLAC Artist", "FLAC Album", "tagged-playable-flac"); err != nil {
		return Summary{}, err
	}
	short, err := pcmWAV(2, 523)
	if err != nil {
		return Summary{}, err
	}
	if err := w.add("library/Short.wav", short, "wav", "Short", "", "", "short-natural-end"); err != nil {
		return Summary{}, err
	}
	long, err := pcmWAV(300, 440)
	if err != nil {
		return Summary{}, err
	}
	if err := w.add("library/long.wav", long, "wav", "long", "", "", "long-seek"); err != nil {
		return Summary{}, err
	}
	if err := addTaggedMP3("library/同名艺术家甲/共用专辑/同名歌曲.mp3", "Shared Title", "Same Name Artist A", "Shared Album", "Same Name Artist A", "same-name-unrelated-artist", sourceMP3); err != nil {
		return Summary{}, err
	}
	if err := addTaggedMP3("library/Same Name Artist B/Shared Album/Shared Title.mp3", "Shared Title", "Same Name Artist B", "Shared Album", "Same Name Artist B", "same-name-unrelated-artist", sourceMP3); err != nil {
		return Summary{}, err
	}
	if err := addTaggedMP3("library/Compilation/Album/Guest A.mp3", "Guest A", "Guest Artist A", "Compilation Album", "Various Artists", "compilation-track", sourceMP3); err != nil {
		return Summary{}, err
	}
	if err := addTaggedMP3("library/Compilation/Album/Guest B.mp3", "Guest B", "Guest Artist B", "Compilation Album", "Various Artists", "compilation-track", sourceMP3); err != nil {
		return Summary{}, err
	}
	if err := w.add("library/Missing Tags/untagged.mp3", sourceMP3, "mp3", "", "", "", "missing-tags"); err != nil {
		return Summary{}, err
	}
	// Malformed and unsupported files are isolated from the playable import root.
	if err := w.add("negative-fixtures/unsupported.txt", []byte("not media\n"), "txt", "", "", "", "unsupported"); err != nil {
		return Summary{}, err
	}
	if err := w.add("negative-fixtures/malformed.mp3", []byte("ID3\x03\x00\x00\x7f\x7f\x7f\x7f"), "mp3", "", "", "", "malformed-metadata"); err != nil {
		return Summary{}, err
	}
	if err := w.add("negative-fixtures/oversized-artwork.mp3", oversizedArtworkMP3(), "mp3", "", "", "", "oversized-artwork-metadata"); err != nil {
		return Summary{}, err
	}
	if err := w.add("negative-fixtures/oversized.wav", make([]byte, 44), "wav", "", "", "", "malformed-audio-envelope"); err != nil {
		return Summary{}, err
	}
	return w.finish(Summary{Mode: "journey", TrackCount: 9, ArtistCount: 6, AlbumCount: 5, AudioSourceDetails: "CC0 testdata/metadata/untagged.mp3 and untagged.flac; generated mono 16-bit 44100 Hz PCM WAV", AudioSources: map[string]string{"untagged.mp3": hex.EncodeToString(mp3Hash[:]), "untagged.flac": hex.EncodeToString(flacHash[:])}})
}

func oversizedArtworkMP3() []byte {
	payload := append([]byte{0}, []byte("image/jpeg")...)
	payload = append(payload, 0, 0, 0) // MIME terminator, front-cover type, empty description.
	payload = append(payload, make([]byte, int(metadata.MaxArtworkBytes)+1)...)
	frame := make([]byte, 10, 10+len(payload))
	copy(frame, "APIC")
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	frame = append(frame, payload...)
	return append([]byte{'I', 'D', '3', 3, 0, 0, byte(len(frame) >> 21 & 127), byte(len(frame) >> 14 & 127), byte(len(frame) >> 7 & 127), byte(len(frame) & 127)}, frame...)
}

func id3v23(title, artist, album, albumArtist string, track int) []byte {
	textFrame := func(name, value string) []byte {
		payload := []byte{1, 0xff, 0xfe}
		for _, unit := range utf16.Encode([]rune(value)) {
			var pair [2]byte
			binary.LittleEndian.PutUint16(pair[:], unit)
			payload = append(payload, pair[:]...)
		}
		frame := make([]byte, 10, 10+len(payload))
		copy(frame, name)
		binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
		return append(frame, payload...)
	}
	frames := append(textFrame("TIT2", title), textFrame("TPE1", artist)...)
	frames = append(frames, textFrame("TALB", album)...)
	frames = append(frames, textFrame("TPE2", albumArtist)...)
	frames = append(frames, textFrame("TRCK", fmt.Sprint(track))...)
	frames = append(frames, textFrame("TYER", "2024")...)
	return append([]byte{'I', 'D', '3', 3, 0, 0, byte(len(frames) >> 21 & 127), byte(len(frames) >> 14 & 127), byte(len(frames) >> 7 & 127), byte(len(frames) & 127)}, frames...)
}

type mpegFrame struct{ start, end int }

func mpegFrameWindows(data []byte, size int) ([][]byte, error) {
	var frames []mpegFrame
	for offset := 0; offset+4 <= len(data); {
		header := binary.BigEndian.Uint32(data[offset : offset+4])
		if header>>21 != 0x7ff {
			break
		}
		version := (header >> 19) & 3
		layer := (header >> 17) & 3
		bitrateIndex := (header >> 12) & 15
		rateIndex := (header >> 10) & 3
		if version == 1 || layer != 1 || bitrateIndex == 0 || bitrateIndex == 15 || rateIndex == 3 {
			break
		}
		mpeg1Bitrates := [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
		mpeg2Bitrates := [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
		bitrate := mpeg2Bitrates[bitrateIndex]
		if version == 3 {
			bitrate = mpeg1Bitrates[bitrateIndex]
		}
		sampleRates := [...]int{44100, 48000, 32000}
		sampleRate := sampleRates[rateIndex]
		if version == 2 {
			sampleRate /= 2
		}
		if version == 0 {
			sampleRate /= 4
		}
		padding := int((header >> 9) & 1)
		coefficient := 72
		if version == 3 {
			coefficient = 144
		}
		length := coefficient*bitrate*1000/sampleRate + padding
		if length < 4 || offset+length > len(data) {
			break
		}
		frames = append(frames, mpegFrame{start: offset, end: offset + length})
		offset += length
	}
	if len(frames) < size {
		return nil, errors.New("licensed MP3 does not contain enough complete MPEG Layer III frames")
	}
	windows := make([][]byte, len(frames)-size+1)
	for i := range windows {
		start, end := frames[i].start, frames[i+size-1].end
		windows[i] = append([]byte(nil), data[start:end]...)
	}
	return windows, nil
}

func withFLACComments(base []byte, comments []string) ([]byte, error) {
	if len(base) < 8 || string(base[:4]) != "fLaC" {
		return nil, errors.New("invalid source FLAC")
	}
	position := 4
	for {
		if position+4 > len(base) {
			return nil, errors.New("truncated source FLAC metadata")
		}
		header := base[position]
		length := int(base[position+1])<<16 | int(base[position+2])<<8 | int(base[position+3])
		if position+4+length > len(base) {
			return nil, errors.New("invalid source FLAC metadata length")
		}
		last := header&0x80 != 0
		if last {
			base = append([]byte(nil), base...)
			base[position] &^= 0x80
		}
		position += 4 + length
		if last {
			break
		}
	}
	var block []byte
	put := func(value uint32) {
		var bytes [4]byte
		binary.LittleEndian.PutUint32(bytes[:], value)
		block = append(block, bytes[:]...)
	}
	vendor := []byte("Project Resonance M1.7 generated fixture")
	put(uint32(len(vendor)))
	block = append(block, vendor...)
	put(uint32(len(comments)))
	for _, comment := range comments {
		put(uint32(len(comment)))
		block = append(block, comment...)
	}
	if len(block) > 0xffffff {
		return nil, errors.New("FLAC comments exceed block size")
	}
	header := []byte{0x84, byte(len(block) >> 16), byte(len(block) >> 8), byte(len(block))}
	result := append(append(append([]byte{}, base[:position]...), header...), block...)
	return append(result, base[position:]...), nil
}

func pcmWAV(seconds, frequency int) ([]byte, error) {
	if seconds < 1 || seconds > 3600 || frequency < 20 || frequency > 20_000 {
		return nil, errors.New("invalid PCM WAV parameters")
	}
	const sampleRate = 44100
	dataSize := seconds * sampleRate * 2
	if dataSize > math.MaxInt32-36 {
		return nil, errors.New("PCM WAV exceeds RIFF size limit")
	}
	data := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+dataSize))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], sampleRate)
	binary.LittleEndian.PutUint32(header[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(dataSize))
	_, _ = data.Write(header)
	buffer := make([]byte, 8192)
	for sample := 0; sample < seconds*sampleRate; {
		count := min(len(buffer)/2, seconds*sampleRate-sample)
		for j := 0; j < count; j++ {
			value := int16(6000 * math.Sin(2*math.Pi*float64(frequency)*float64(sample+j)/sampleRate))
			binary.LittleEndian.PutUint16(buffer[j*2:], uint16(value))
		}
		if _, err := data.Write(buffer[:count*2]); err != nil {
			return nil, err
		}
		sample += count
	}
	return data.Bytes(), nil
}
