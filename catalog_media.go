package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"resonance/internal/library"
	"resonance/internal/metadata"
	"resonance/internal/storage"
)

func mediaMIME(format string) string {
	switch format {
	case "mp3":
		return "audio/mpeg"
	case "flac":
		return "audio/flac"
	case "wav":
		return "audio/wav"
	}
	return ""
}

func safeRangeForLog(value string) string {
	if len(value) > 128 {
		return "invalid"
	}
	for _, r := range value {
		if !strings.ContainsRune("bytes=0123456789,- ", r) {
			return "invalid"
		}
	}
	return value
}

func validRelativeMediaPath(relative string) bool {
	if relative == "" || filepath.IsAbs(relative) || !filepath.IsLocal(relative) || strings.Contains(relative, ":") || strings.ContainsRune(relative, 0) {
		return false
	}
	if filepath.ToSlash(filepath.Clean(relative)) != strings.ReplaceAll(relative, "\\", "/") {
		return false
	}
	for _, part := range strings.FieldsFunc(relative, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "." || part == ".." || part == "" {
			return false
		}
	}
	return true
}

func sameEnrolledRootPath(current, enrolled string) bool {
	current, enrolled = filepath.Clean(current), filepath.Clean(enrolled)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(current, enrolled)
	}
	return current == enrolled
}

func openCatalogCandidate(c storage.PlaybackCandidate) (*os.File, error) {
	return openCatalogCandidateWithHook(c, nil)
}

// afterOpen is a fault-injection seam for path replacement tests.
func openCatalogCandidateWithHook(c storage.PlaybackCandidate, afterOpen func()) (*os.File, error) {
	if !validRelativeMediaPath(c.RelativePath) {
		return nil, errors.New("invalid_location")
	}
	if c.ObservedSize == nil || c.ObservedMTimeNS == nil {
		return nil, errors.New("location_unverified")
	}
	canonical, err := library.CanonicalizeRoot(c.RootPath)
	if err != nil || !sameEnrolledRootPath(canonical, c.RootPath) {
		return nil, errors.New("root_changed")
	}
	root, err := os.OpenRoot(c.RootPath)
	if err != nil {
		return nil, errors.New("root_unavailable")
	}
	defer root.Close()
	rootInfo, rootErr := root.Stat(".")
	pathInfo, pathErr := os.Stat(c.RootPath)
	if rootErr != nil || pathErr != nil || !rootInfo.IsDir() || !os.SameFile(rootInfo, pathInfo) {
		return nil, errors.New("root_changed")
	}
	parts := strings.FieldsFunc(c.RelativePath, func(r rune) bool { return r == '/' || r == '\\' })
	for i := range parts {
		part := filepath.Join(parts[:i+1]...)
		info, e := root.Lstat(part)
		if e != nil {
			return nil, errors.New("location_missing")
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil, errors.New("location_link")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, errors.New("invalid_location")
		}
	}
	f, err := root.OpenFile(c.RelativePath, os.O_RDONLY, 0)
	if err != nil {
		return nil, errors.New("location_open_failed")
	}
	if afterOpen != nil {
		afterOpen()
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("location_not_regular")
	}
	if c.ObservedSize != nil && info.Size() != *c.ObservedSize {
		f.Close()
		return nil, errors.New("location_changed")
	}
	if c.ObservedMTimeNS != nil && info.ModTime().UnixNano() != *c.ObservedMTimeNS {
		f.Close()
		return nil, errors.New("location_changed")
	}
	if c.NativeKind != nil && c.NativeScope != nil && len(c.NativeID) > 0 {
		identity, available, e := library.NativeIdentityFromOpenFile(f)
		if e != nil {
			f.Close()
			return nil, errors.New("native_evidence_failed")
		}
		if available && identity.Kind == *c.NativeKind && (identity.Scope != *c.NativeScope || !bytes.Equal(identity.ID, c.NativeID) || len(c.NativeBirth) > 0 && !bytes.Equal(identity.BirthToken, c.NativeBirth)) {
			f.Close()
			return nil, errors.New("location_changed")
		}
	}
	// A stable open handle can outlive replacement of its pathname. Recheck
	// the confined path before returning bytes from that handle.
	pathInfo, err = root.Lstat(c.RelativePath)
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(info, pathInfo) || pathInfo.Size() != info.Size() || pathInfo.ModTime().UnixNano() != info.ModTime().UnixNano() {
		f.Close()
		return nil, errors.New("location_changed")
	}
	return f, nil
}

func (a *app) resolveCatalogMedia(w http.ResponseWriter, r *http.Request, requireArtwork bool) (*os.File, *storage.PlaybackCandidate, int, string) {
	if !a.catalogReady(w, r) {
		return nil, nil, http.StatusServiceUnavailable, "catalog_unavailable"
	}
	id := r.PathValue("id")
	if !requireID(w, id, "track") {
		return nil, nil, http.StatusBadRequest, "invalid_request"
	}
	candidates, err := a.store.PlaybackCandidates(r.Context(), id)
	if err != nil {
		catalogQueryError(w, err)
		if storage.IsCatalogNotFound(err) {
			return nil, nil, http.StatusNotFound, "not_found"
		}
		if errors.Is(err, storage.ErrCatalogInvalid) {
			return nil, nil, http.StatusInternalServerError, "catalog_invalid"
		}
		return nil, nil, http.StatusServiceUnavailable, "catalog_unavailable"
	}
	for _, candidate := range candidates {
		if requireArtwork && (len(candidate.ArtworkSHA256) != sha256.Size || candidate.ArtworkMIME == nil) {
			continue
		}
		if mediaMIME(candidate.Format) == "" {
			continue
		}
		f, e := openCatalogCandidate(candidate)
		if e != nil {
			a.log.Warn("catalog_candidate_failed", "request_id", w.Header().Get("X-Request-ID"), "track_id", id, "location_id", candidate.LocationID, "code", e.Error())
			continue
		}
		still, e := a.store.CandidateStillAvailable(r.Context(), candidate)
		if e != nil {
			f.Close()
			catalogError(w, 503, "catalog_unavailable")
			return nil, nil, http.StatusServiceUnavailable, "catalog_unavailable"
		}
		if !still {
			f.Close()
			continue
		}
		return f, &candidate, 0, ""
	}
	if requireArtwork {
		catalogError(w, 404, "not_found")
		return nil, nil, http.StatusNotFound, "not_found"
	} else {
		catalogError(w, 503, "track_unavailable")
		return nil, nil, http.StatusServiceUnavailable, "track_unavailable"
	}
}

func (a *app) catalogStream(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	f, c, rejectedStatus, rejectedCode := a.resolveCatalogMedia(w, r, false)
	if f == nil {
		attrs := []any{"request_id", w.Header().Get("X-Request-ID"), "source", "catalog", "status", rejectedStatus, "error", rejectedCode,
			"range", r.Header.Get("Range") != "", "range_header", safeRangeForLog(r.Header.Get("Range")), "bytes_intended", int64(0), "bytes_served", int64(0),
			"duration_ms", float64(time.Since(started).Microseconds()) / 1000, "canceled", r.Context().Err() != nil}
		if id := r.PathValue("id"); storage.ValidCatalogID(id, "track") {
			attrs = append(attrs, "track_id", id)
		}
		a.log.Info("media_request", attrs...)
		return
	}
	defer f.Close()
	result := serveMediaBytes(r.Context(), w, r, f, mediaMIME(c.Format))
	attrs := []any{"request_id", w.Header().Get("X-Request-ID"), "source", "catalog", "track_id", c.TrackID, "media_object_id", c.MediaObjectID, "location_id", c.LocationID, "range", r.Header.Get("Range") != "", "range_header", safeRangeForLog(r.Header.Get("Range")), "range_applied", result.applyRange && result.status == 206, "status", result.status, "selected_start", result.start, "selected_end", result.end, "bytes_intended", result.intended, "bytes_served", result.served, "duration_ms", float64(time.Since(started).Microseconds()) / 1000, "canceled", r.Context().Err() != nil}
	if result.err != nil {
		code := "stream_failed"
		if r.Context().Err() != nil {
			code = "client_canceled"
		}
		attrs = append(attrs, "error", code)
	}
	a.log.Info("media_request", attrs...)
	if result.headersStarted && result.err != nil && r.Context().Err() == nil {
		panic(http.ErrAbortHandler)
	}
}

func verifiedArtwork(f *os.File, c storage.PlaybackCandidate) ([]byte, string, error) {
	if len(c.ArtworkSHA256) != sha256.Size || c.ArtworkMIME == nil {
		return nil, "", errors.New("artwork_missing")
	}
	if *c.ArtworkMIME != "image/png" && *c.ArtworkMIME != "image/jpeg" {
		return nil, "", errors.New("artwork_mime")
	}
	result, _, err := metadata.Read(f)
	if err != nil || result.Artwork == nil {
		return nil, "", errors.New("artwork_invalid")
	}
	art := result.Artwork
	if len(art.Data) == 0 || len(art.Data) > int(metadata.MaxArtworkBytes) || art.MIME != *c.ArtworkMIME {
		return nil, "", errors.New("artwork_invalid")
	}
	actual := sha256.Sum256(art.Data)
	if !bytes.Equal(actual[:], c.ArtworkSHA256) {
		return nil, "", errors.New("artwork_hash")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(art.Data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 {
		return nil, "", errors.New("artwork_invalid")
	}
	if format == "png" && art.MIME == "image/png" || format == "jpeg" && art.MIME == "image/jpeg" {
		return art.Data, art.MIME, nil
	}
	return nil, "", errors.New("artwork_mime")
}

func (a *app) catalogArtwork(w http.ResponseWriter, r *http.Request) {
	f, c, _, _ := a.resolveCatalogMedia(w, r, true)
	if f == nil {
		return
	}
	defer f.Close()
	art, mime, err := verifiedArtwork(f, *c)
	if err != nil {
		catalogError(w, 404, "not_found")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(art)))
	w.WriteHeader(200)
	_, _ = w.Write(art)
}
