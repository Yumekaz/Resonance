package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address; use a LAN address only on a trusted network")
	media := flag.String("media", "data/demo.wav", "configured WAV file for demo-track")
	title := flag.String("title", "Demo Track", "display title")
	flag.Parse()

	if strings.Contains(filepath.Base(*media), ":") {
		log.Fatal("alternate data streams are not supported")
	}
	f, err := os.OpenInRoot(filepath.Dir(*media), filepath.Base(*media))
	if err != nil {
		log.Fatalf("configured media is unavailable: %v", err)
	}
	info, err := f.Stat()
	f.Close()
	if err != nil || !info.Mode().IsRegular() {
		log.Fatal("configured media must be a readable regular file")
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(*media, *title, os.Stdout),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	log.Printf("Resonance M0 listening on %s", *addr)
	log.Fatal(server.ListenAndServe())
}
