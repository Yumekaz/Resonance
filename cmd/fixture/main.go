package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

func main() {
	out := flag.String("out", "data/demo.wav", "output WAV file")
	seconds := flag.Int("seconds", 30, "duration in seconds")
	flag.Parse()
	if *seconds < 1 || *seconds > 3600 {
		fmt.Fprintln(os.Stderr, "seconds must be 1..3600")
		os.Exit(2)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0755); err != nil {
		panic(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	const sampleRate = 44100
	dataSize := uint32(*seconds * sampleRate * 2)
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], 36+dataSize)
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], sampleRate)
	binary.LittleEndian.PutUint32(header[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], dataSize)
	if _, err := f.Write(header); err != nil {
		panic(err)
	}
	buffer := make([]byte, 8192)
	for i := 0; i < *seconds*sampleRate; {
		n := len(buffer) / 2
		if remaining := *seconds*sampleRate - i; remaining < n {
			n = remaining
		}
		for j := 0; j < n; j++ {
			value := int16(6000 * math.Sin(2*math.Pi*440*float64(i+j)/sampleRate))
			binary.LittleEndian.PutUint16(buffer[j*2:], uint16(value))
		}
		if _, err := f.Write(buffer[:n*2]); err != nil {
			panic(err)
		}
		i += n
	}
	fmt.Printf("generated %s (%d bytes, PCM WAV)\n", *out, dataSize+44)
}
