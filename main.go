package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"resonance/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// Return before reporting fatal startup/listener errors so resource defers run.
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "library" {
		return runLibrary(os.Args[2:], os.Stdout, os.Stderr)
	}
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address; use a LAN address only on a trusted network")
	media := flag.String("media", "data/demo.wav", "configured WAV file for demo-track")
	title := flag.String("title", "Demo Track", "display title")
	migrateOnly := flag.Bool("migrate-only", false, "apply pending database migrations and exit")
	flag.Parse()
	databaseURL := os.Getenv("RESONANCE_DATABASE_URL")
	var ready func(context.Context) error
	var catalogStore *storage.Store
	if databaseURL != "" || *migrateOnly {
		if databaseURL == "" {
			return errors.New("RESONANCE_DATABASE_URL is required for migrations")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		store, err := storage.Open(ctx, databaseURL)
		if err != nil {
			return errors.New("invalid database configuration")
		}
		defer store.Close()
		if *migrateOnly {
			if err := store.Migrate(ctx); err != nil {
				return errors.New("database migration failed; inspect PostgreSQL logs and migration state")
			}
			log.Print("database migrations applied")
			return nil
		}
		if err := store.Ready(ctx); err != nil {
			return errors.New("database unavailable or schema incompatible; run migrations and check PostgreSQL")
		}
		ready = store.Ready
		catalogStore = store
	}

	if strings.Contains(filepath.Base(*media), ":") {
		return errors.New("alternate data streams are not supported")
	}
	f, err := os.OpenInRoot(filepath.Dir(*media), filepath.Base(*media))
	if err != nil {
		return errors.New("configured media is unavailable")
	}
	info, err := f.Stat()
	f.Close()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("configured media must be a readable regular file")
	}
	if catalogStore != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, cleanupErr := catalogStore.PruneExpiredReceipts(cleanupCtx, 10)
		cleanupCancel()
		if cleanupErr != nil {
			return errors.New("mutation receipt cleanup failed during startup")
		}
		workerCtx, stopWorker := context.WithCancel(context.Background())
		defer stopWorker()
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-workerCtx.Done():
					return
				case <-ticker.C:
					ctx, cancel := context.WithTimeout(workerCtx, 10*time.Second)
					if _, err := catalogStore.PruneExpiredReceipts(ctx, 10); err != nil {
						log.Print("mutation receipt cleanup unavailable")
					}
					cancel()
				}
			}
		}()
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandlerWithCatalog(*media, *title, os.Stdout, ready, catalogStore),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	log.Printf("Resonance listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("HTTP server stopped or could not listen")
	}
	return nil
}
