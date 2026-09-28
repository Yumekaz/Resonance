package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"resonance/internal/library"
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
		startupTimeout := 10 * time.Second
		if *migrateOnly {
			// Populated grouping backfills are part of the foreground migration
			// command. The normal server's dependency check remains bounded at
			// ten seconds; the measured 10k upgrade needs a longer command budget.
			startupTimeout = 5 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
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
	signalCtx, signalStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer signalStop()
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	var coordinator *library.Coordinator
	var coordinatorDone chan struct{}
	if catalogStore != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, cleanupErr := catalogStore.PruneExpiredReceipts(cleanupCtx, 10)
		cleanupCancel()
		if cleanupErr != nil {
			return errors.New("mutation receipt cleanup failed during startup")
		}
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
		scanner := &library.Scanner{Store: catalogStore, Log: slog.Default()}
		coordinator, err = library.NewCoordinator(library.CoordinatorOptions{
			Store: catalogStore, Scanner: scanner, Logger: slog.Default(),
		})
		if err != nil {
			return errors.New("library coordinator configuration failed")
		}
		coordinatorDone = make(chan struct{})
		go func() {
			defer close(coordinatorDone)
			_ = coordinator.Run(workerCtx)
		}()
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandlerWithCoordinator(*media, *title, os.Stdout, ready, catalogStore, coordinator),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	log.Printf("Resonance listening on %s", *addr)
	listenErrors := make(chan error, 1)
	go func() { listenErrors <- server.ListenAndServe() }()
	var serverErr error
	select {
	case <-signalCtx.Done():
	case serverErr = <-listenErrors:
		if !errors.Is(serverErr, http.ErrServerClosed) {
			serverErr = errors.New("HTTP server stopped or could not listen")
		} else {
			serverErr = nil
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = server.Close()
	}
	shutdownCancel()
	stopWorkers()
	if coordinatorDone != nil {
		select {
		case <-coordinatorDone:
		case <-time.After(9 * time.Second):
			if serverErr == nil {
				serverErr = errors.New("library coordinator did not stop cleanly")
			}
		}
	}
	if serverErr != nil {
		return serverErr
	}
	if shutdownErr != nil && !errors.Is(shutdownErr, context.DeadlineExceeded) {
		return errors.New("HTTP server shutdown failed")
	}
	return nil
}
