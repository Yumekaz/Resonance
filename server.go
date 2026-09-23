package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"resonance/internal/storage"
)

//go:embed web/index.html web/app.js web/style.css web/library.html web/library.js web/library.css
var webAssets embed.FS

type track struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	StreamURL string `json:"stream_url"`
	root      string
	filename  string
}

type app struct {
	tracks map[string]track
	log    *slog.Logger
	ready  func(context.Context) error
	store  *storage.Store
}

func newHandler(mediaPath, title string, logOutput io.Writer) http.Handler {
	return newHandlerWithReadiness(mediaPath, title, logOutput, nil)
}

func newHandlerWithReadiness(mediaPath, title string, logOutput io.Writer, ready func(context.Context) error) http.Handler {
	return newHandlerWithCatalog(mediaPath, title, logOutput, ready, nil)
}

func newHandlerWithCatalog(mediaPath, title string, logOutput io.Writer, ready func(context.Context) error, store *storage.Store) http.Handler {
	a := &app{
		tracks: map[string]track{"demo-track": {ID: "demo-track", Title: title, StreamURL: "/media/demo-track", root: filepath.Dir(mediaPath), filename: filepath.Base(mediaPath)}},
		log:    slog.New(slog.NewJSONHandler(logOutput, nil)),
		ready:  ready,
		store:  store,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("GET /ready", a.readiness)
	mux.HandleFunc("GET /api/v1/demo-track", a.metadata)
	mux.HandleFunc("GET /media/{id}", a.media)
	mux.HandleFunc("GET /api/v1/tracks", a.tracksList)
	mux.HandleFunc("GET /api/v1/tracks/{id}", a.trackDetail)
	mux.HandleFunc("GET /api/v1/artists", a.artistsList)
	mux.HandleFunc("GET /api/v1/artists/{id}", a.artistDetail)
	mux.HandleFunc("GET /api/v1/albums", a.albumsList)
	mux.HandleFunc("GET /api/v1/albums/{id}", a.albumDetail)
	mux.HandleFunc("GET /api/v1/artists/{id}/albums", a.artistAlbums)
	mux.HandleFunc("GET /api/v1/artists/{id}/tracks", a.groupedTracks)
	mux.HandleFunc("GET /api/v1/albums/{id}/tracks", a.groupedTracks)
	mux.HandleFunc("GET /api/v1/tracks/{id}/stream", a.catalogStream)
	mux.HandleFunc("HEAD /api/v1/tracks/{id}/stream", a.catalogStream)
	mux.HandleFunc("GET /api/v1/tracks/{id}/artwork", a.catalogArtwork)
	assets, _ := fs.Sub(webAssets, "web")
	if store != nil {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			body, _ := webAssets.ReadFile("web/library.html")
			_, _ = w.Write(body)
		})
		mux.HandleFunc("GET /demo", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			body, _ := webAssets.ReadFile("web/index.html")
			_, _ = w.Write(body)
		})
	}
	mux.Handle("GET /", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", requestID())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (a *app) readiness(w http.ResponseWriter, r *http.Request) {
	if a.ready == nil {
		jsonResponseStatus(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "catalog": "unconfigured"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.ready(ctx); err != nil {
		jsonResponseStatus(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "catalog": "unavailable"})
		return
	}
	jsonResponse(w, map[string]string{"status": "ready", "catalog": "ready"})
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"status": "ok", "version": "dev"})
}

func (a *app) metadata(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, a.tracks["demo-track"])
}

func jsonResponse(w http.ResponseWriter, value any) {
	jsonResponseStatus(w, http.StatusOK, value)
}

func jsonResponseStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (a *app) media(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	id := r.PathValue("id")
	rangeHeader := strings.Join(r.Header.Values("Range"), ",")
	usedRange := rangeHeader != ""
	applyRange := usedRange && r.Method == http.MethodGet && r.Header.Get("If-Range") == "" && len(r.Header.Values("Range")) == 1 && !strings.Contains(rangeHeader, ",")
	if unit, _, ok := strings.Cut(rangeHeader, "="); ok && !strings.EqualFold(unit, "bytes") {
		applyRange = false
	}
	requestID := w.Header().Get("X-Request-ID")
	status, intended, served := http.StatusOK, int64(0), int64(0)
	requestedStart, requestedEnd := int64(-1), int64(-1)
	var streamErr error
	defer func() {
		contextErr := r.Context().Err()
		canceled := contextErr != nil
		attrs := []any{"request_id", requestID, "source", "demo", "media_id", id, "range", usedRange, "range_applied", applyRange && status == http.StatusPartialContent, "range_header", safeRangeForLog(rangeHeader), "selected_start", requestedStart, "selected_end", requestedEnd, "status", status, "bytes_intended", intended, "bytes_served", served, "duration_ms", float64(time.Since(started).Microseconds()) / 1000, "canceled", canceled}
		if streamErr != nil {
			errorClass := "stream_failed"
			if contextErr != nil {
				errorClass = "client_canceled"
			}
			attrs = append(attrs, "error", errorClass)
		}
		a.log.Info("media_request", attrs...)
	}()

	entry, ok := a.tracks[id]
	if !ok || strings.Contains(id, "/") {
		status = http.StatusNotFound
		http.Error(w, "media not found", status)
		return
	}
	if r.Context().Err() != nil {
		streamErr = r.Context().Err()
		return
	}
	// The operator enrolls the configured file's parent directory. OpenInRoot
	// prevents a replaced filename/symlink from escaping that directory.
	var f *os.File
	var err error
	if strings.Contains(entry.filename, ":") {
		err = errors.New("alternate data stream")
	} else {
		f, err = os.OpenInRoot(entry.root, entry.filename)
	}
	if err != nil {
		status = http.StatusServiceUnavailable
		streamErr = err
		http.Error(w, "media unavailable", status)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		status = http.StatusServiceUnavailable
		streamErr = errors.New("media stat failed or is not regular")
		http.Error(w, "media unavailable", status)
		return
	}
	result := serveMediaBytes(r.Context(), w, r, f, "audio/wav")
	status, intended, served = result.status, result.intended, result.served
	requestedStart, requestedEnd = result.start, result.end
	streamErr = result.err
	if result.headersStarted && streamErr != nil && r.Context().Err() == nil {
		panic(http.ErrAbortHandler)
	}
}

func copyMedia(ctx context.Context, w http.ResponseWriter, f io.Reader, size int64) (int64, error) {
	buffer := make([]byte, 32*1024)
	var written int64
	controller := http.NewResponseController(w)
	defer controller.SetWriteDeadline(time.Time{})
	for written < size {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := f.Read(buffer[:min(int64(len(buffer)), size-written)])
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return written, err
			}
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			count, err := w.Write(buffer[:n])
			written += int64(count)
			if err != nil {
				return written, err
			}
			if count != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil && written < size {
			return written, readErr
		}
		if n == 0 && readErr == nil {
			return written, io.ErrNoProgress
		}
	}
	return written, nil
}
