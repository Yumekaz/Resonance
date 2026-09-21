package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"resonance/internal/library"
	"resonance/internal/storage"
)

func runLibrary(args []string, out, logOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: resonance library <add|list|scan|disable>")
	}
	databaseURL := os.Getenv("RESONANCE_DATABASE_URL")
	if databaseURL == "" {
		return errors.New("RESONANCE_DATABASE_URL is required for library commands")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, databaseURL)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	defer store.Close()
	if err := store.Ready(ctx); err != nil {
		return errors.New("database unavailable or schema incompatible")
	}
	emit := func(value any) error { return json.NewEncoder(out).Encode(value) }
	switch args[0] {
	case "add":
		flags := flag.NewFlagSet("library add", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		path := flags.String("path", "", "host directory to enroll")
		name := flags.String("name", "", "display name")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return errors.New("usage: resonance library add -path <directory> -name <name>")
		}
		canonical, err := library.CanonicalizeRoot(*path)
		if err != nil {
			return err
		}
		root, err := store.AddRoot(ctx, *name, canonical)
		if err != nil {
			if errors.Is(err, storage.ErrRootOverlap) {
				return err
			}
			return errors.New("root enrollment failed")
		}
		return emit(root)
	case "list":
		if len(args) != 1 {
			return errors.New("usage: resonance library list")
		}
		roots, err := store.ListRoots(ctx)
		if err != nil {
			return errors.New("library list failed")
		}
		return emit(roots)
	case "disable":
		if len(args) != 2 {
			return errors.New("usage: resonance library disable <root-id>")
		}
		if err := store.DisableRoot(ctx, args[1]); err != nil {
			if errors.Is(err, storage.ErrRootNotFound) {
				return err
			}
			return errors.New("root disable failed")
		}
		return emit(map[string]string{"id": args[1], "status": "disabled"})
	case "scan":
		if len(args) != 2 {
			return errors.New("usage: resonance library scan <root-id>")
		}
		scanCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		scanner := &library.Scanner{Store: store, Log: slog.New(slog.NewJSONHandler(logOut, nil))}
		result, err := scanner.Scan(scanCtx, args[1])
		if result.RunID != "" {
			_ = emit(result)
		}
		if err != nil {
			if errors.Is(err, storage.ErrScanRunning) || errors.Is(err, storage.ErrRootDisabled) || errors.Is(err, storage.ErrRootNotFound) {
				return err
			}
			if strings.Contains(err.Error(), "database") {
				return errors.New("database failure during scan")
			}
			if errors.Is(err, context.Canceled) {
				return errors.New("scan canceled")
			}
			return errors.New("scan failed; inspect structured scan status")
		}
		return nil
	default:
		return errors.New("usage: resonance library <add|list|scan|disable>")
	}
}
