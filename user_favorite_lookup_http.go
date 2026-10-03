package main

import "net/http"

func (a *app) favoritesLookup(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	var req struct {
		TrackIDs []string `json:"track_ids"`
	}
	if err := decodeUserJSON(r, &req, false); err != nil {
		userError(w, err)
		return
	}
	ids, err := a.store.LookupFavorites(r.Context(), req.TrackIDs)
	if err != nil {
		userError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"track_ids": ids})
}
