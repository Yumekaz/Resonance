package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"resonance/internal/m17fixtures"
)

func main() {
	mode := flag.String("mode", "", "scale or journey")
	out := flag.String("out", "", "new output directory (existing directories are rejected)")
	count := flag.Int("tracks", m17fixtures.ScaleTrackCount, "scale fixture count")
	seed := flag.Uint64("seed", 20260928, "deterministic MP3 frame-window seed")
	mp3Path := flag.String("mp3", filepath.Join("testdata", "metadata", "untagged.mp3"), "CC0 source MP3 fixture")
	flacPath := flag.String("flac", filepath.Join("testdata", "metadata", "untagged.flac"), "CC0 source FLAC fixture")
	flag.Parse()
	if *out == "" {
		if attempt := os.Getenv("RESONANCE_M17_ATTEMPT_DIR"); attempt != "" {
			*out = filepath.Join(attempt, "corpus")
		} else {
			fail("-out is required outside an M1.7 attempt")
		}
	}
	var summary m17fixtures.Summary
	var err error
	switch *mode {
	case "scale":
		var data []byte
		data, err = os.ReadFile(*mp3Path)
		if err == nil {
			summary, err = m17fixtures.GenerateScale(*out, data, *count, *seed)
		}
	case "journey":
		var mp3, flac []byte
		mp3, err = os.ReadFile(*mp3Path)
		if err == nil {
			flac, err = os.ReadFile(*flacPath)
		}
		if err == nil {
			summary, err = m17fixtures.GenerateJourney(*out, mp3, flac)
		}
	default:
		fail("-mode must be scale or journey")
	}
	if err != nil {
		fail(err.Error())
	}
	if attempt := os.Getenv("RESONANCE_M17_ATTEMPT_DIR"); attempt != "" {
		if err := copyExclusive(filepath.Join(*out, "manifest.jsonl"), filepath.Join(attempt, "fixture-manifest.jsonl")); err != nil {
			fail(err.Error())
		}
		if err := copyExclusive(filepath.Join(*out, "summary.json"), filepath.Join(attempt, "fixture-summary.json")); err != nil {
			fail(err.Error())
		}
	}
	fmt.Printf("generated %s corpus: %d files, %d unique hashes, %d Artists, %d Albums, %d bytes\n", summary.Mode, summary.FileCount, summary.UniqueSHA256, summary.ArtistCount, summary.AlbumCount, summary.TotalBytes)
}

func copyExclusive(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
