package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *app) search(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil {
			catalogError(w, 400, "invalid_request")
			return
		}
	}
	if len(q["q"]) > 1 || len(q["limit"]) > 1 || !utf8.ValidString(query) || strings.ContainsRune(query, 0) || len(query) > 512 || utf8.RuneCountInString(query) > 120 || limit < 1 || limit > 50 {
		catalogError(w, 400, "invalid_request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	w.Header().Set("Cache-Control", "no-store")
	if !a.catalogReady(w, r) {
		return
	}
	result, err := a.store.SearchCatalog(ctx, query, limit)
	a.log.Info("catalog_search", "request_id", w.Header().Get("X-Request-ID"), "duration_ms", float64(time.Since(started).Microseconds())/1000, "failed", err != nil, "canceled", ctx.Err() != nil)
	if err != nil {
		catalogQueryError(w, err)
		return
	}
	jsonResponse(w, result)
}
