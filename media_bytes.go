package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type mediaResult struct {
	status         int
	headersStarted bool
	intended       int64
	served         int64
	start          int64
	end            int64
	applyRange     bool
	err            error
}

// serveMediaBytes is the single M0/M1.4 byte-serving implementation. The
// caller has already confined and opened the source before any headers exist.
func serveMediaBytes(ctx context.Context, w http.ResponseWriter, r *http.Request, f *os.File, mime string) mediaResult {
	result := mediaResult{status: http.StatusOK, start: -1, end: -1}
	rangeHeader := strings.Join(r.Header.Values("Range"), ",")
	result.applyRange = rangeHeader != "" && r.Method == http.MethodGet && r.Header.Get("If-Range") == "" && len(r.Header.Values("Range")) == 1 && !strings.Contains(rangeHeader, ",")
	if unit, _, ok := strings.Cut(rangeHeader, "="); ok && !strings.EqualFold(unit, "bytes") {
		result.applyRange = false
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		result.status = http.StatusServiceUnavailable
		result.err = errors.New("invalid media handle")
		http.Error(w, "media unavailable", result.status)
		return result
	}
	size := info.Size()
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	selected := byteRange{0, size - 1}
	if result.applyRange {
		selected, err = parseRange(rangeHeader, size)
		if err != nil {
			if errors.Is(err, errUnsatisfiableRange) {
				result.status = http.StatusRequestedRangeNotSatisfiable
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			} else {
				result.status = http.StatusBadRequest
			}
			http.Error(w, http.StatusText(result.status), result.status)
			return result
		}
		result.status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", selected.start, selected.end, size))
	}
	result.start, result.end = selected.start, selected.end
	result.intended = selected.end - selected.start + 1
	if _, err = f.Seek(selected.start, io.SeekStart); err != nil {
		result.status = http.StatusServiceUnavailable
		result.err = err
		http.Error(w, "media unavailable", result.status)
		return result
	}
	w.Header().Set("Content-Length", fmt.Sprint(result.intended))
	w.WriteHeader(result.status)
	result.headersStarted = true
	if result.intended == 0 || r.Method == http.MethodHead {
		return result
	}
	result.served, result.err = copyMedia(ctx, w, f, result.intended)
	return result
}
