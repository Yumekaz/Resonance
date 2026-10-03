package main

import (
	"net/http"
	"resonance/internal/storage"
)

func (a *app) queueSelect(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.QueueSelectRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	result, err := a.store.SelectQueueItem(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
func (a *app) queueCollection(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	key, err := mutationKey(r)
	if err != nil {
		userError(w, err)
		return
	}
	var req storage.QueueCollectionRequest
	if err = decodeUserJSON(r, &req, true); err != nil {
		userError(w, err)
		return
	}
	result, err := a.store.AddQueueCollection(r.Context(), key, req)
	if err != nil {
		userError(w, err)
		return
	}
	sendMutation(w, result)
}
