package main

import (
	"context"
	"embed"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"resonance/internal/library"
	"resonance/internal/storage"
)

//go:embed web/admin
var adminAssets embed.FS

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

// No proxy header participates in this decision. The listening socket and peer
// must both be loopback, and Host must name that socket, preventing rebinding.
func hostLocalRequest(r *http.Request) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || !loopbackAddress(local.String()) || !loopbackAddress(r.RemoteAddr) {
		return false
	}
	host, port, err := net.SplitHostPort(r.Host)
	_, localPort, _ := net.SplitHostPort(local.String())
	if err != nil || port != localPort || !(host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || parsed.Scheme != scheme || parsed.Host != r.Host || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return false
		}
	}
	return true
}

func newAdminHandler(store *storage.Store, ready func(context.Context) error, coordinator *library.Coordinator, logs io.Writer, listenerAddresses ...string) http.Handler {
	address := ""
	if len(listenerAddresses) > 0 {
		address = listenerAddresses[0]
	}
	return newAdminHandlerWithPicker(store, ready, coordinator, logs, address, newHostFolderPicker())
}
func newAdminHandlerWithPicker(store *storage.Store, ready func(context.Context) error, coordinator *library.Coordinator, logs io.Writer, address string, picker hostFolderPicker) http.Handler {
	a := &app{store: store, ready: ready, coordinator: coordinator, log: slog.New(slog.NewJSONHandler(logs, nil)), listenerAddress: address, folderPicker: picker}
	mux := http.NewServeMux()
	// Shared, embedded visual assets only. The enclosing host-local guard still
	// applies; this cannot open arbitrary host files or expose admin API routes.
	visualAssets, _ := fs.Sub(webAssets, "web/assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(visualAssets))))
	for route, file := range map[string]string{"/": "index.html", "/admin.js": "admin.js", "/admin.css": "admin.css"} {
		mux.HandleFunc("GET "+route+map[bool]string{true: "{$}"}[route == "/"], func(w http.ResponseWriter, r *http.Request) {
			body, err := adminAssets.ReadFile("web/admin/" + file)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(file)))
			_, _ = w.Write(body)
		})
	}
	mux.HandleFunc("GET /api/v1/admin/status", a.adminStatus)
	mux.HandleFunc("GET /api/v1/admin/roots", a.adminRoots)
	mux.HandleFunc("POST /api/v1/admin/folder-picker", a.adminPickFolder)
	mux.HandleFunc("POST /api/v1/admin/roots", a.adminAddRoot)
	mux.HandleFunc("POST /api/v1/admin/roots/{id}/{action}", a.adminRootAction)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", requestID())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if !hostLocalRequest(r) {
			catalogError(w, 403, "host_only")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if r.Method != "POST" || err != nil || media != "application/json" || r.Header.Get("X-Resonance-Admin") != "1" {
				catalogError(w, 403, "host_only")
				return
			}
		}
		budget := 10 * time.Second
		if r.URL.Path == "/api/v1/admin/folder-picker" {
			budget = 2 * time.Minute
		}
		if strings.HasSuffix(r.URL.Path, "/scan") {
			budget = 5 * time.Minute
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *app) adminStatus(w http.ResponseWriter, r *http.Request) {
	state := map[string]any{"process": "running", "catalog": "unavailable"}
	state["folder_picker"] = map[string]bool{"available": a.folderPicker != nil && a.folderPicker.Available()}
	if host, port, err := net.SplitHostPort(a.listenerAddress); err == nil {
		ip := net.ParseIP(host)
		mode := "network"
		if host == "localhost" || ip != nil && ip.IsLoopback() {
			mode = "local"
		}
		listener := map[string]string{"mode": mode, "address": a.listenerAddress}
		if host != "" && (ip == nil || !ip.IsUnspecified()) {
			listener["url"] = "http://" + net.JoinHostPort(host, port) + "/"
		}
		state["listener"] = listener
	}
	if a.ready != nil && a.ready(r.Context()) == nil {
		state["catalog"] = "ready"
	}
	if a.coordinator != nil {
		state["library"] = a.coordinator.Status()
	}
	jsonResponse(w, state)
}

func (a *app) adminPickFolder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256)
	var input *struct{}
	if decodeUserJSON(r, &input, false) != nil || input == nil {
		catalogError(w, 400, "invalid_request")
		return
	}
	if a.folderPicker == nil || !a.folderPicker.Available() {
		catalogError(w, 501, "folder_picker_unavailable")
		return
	}
	choice, err := a.folderPicker.Pick(r.Context())
	if err != nil {
		code, status := "folder_picker_failed", http.StatusServiceUnavailable
		if errors.Is(err, errPickerBusy) {
			code, status = "folder_picker_busy", http.StatusConflict
		}
		if errors.Is(err, context.DeadlineExceeded) || r.Context().Err() != nil {
			code, status = "folder_picker_timeout", http.StatusRequestTimeout
		}
		catalogError(w, status, code)
		return
	}
	if !choice.Cancelled && !validAdminPath(choice.Path) {
		catalogError(w, 422, "folder_picker_failed")
		return
	}
	name := ""
	if !choice.Cancelled {
		name = filepath.Base(choice.Path)
	}
	jsonResponse(w, map[string]any{"path": choice.Path, "name": name, "cancelled": choice.Cancelled})
}

type adminRoot struct {
	storage.LibraryRoot
	Path string `json:"path"`
}

func (a *app) adminRoots(w http.ResponseWriter, r *http.Request) {
	if !a.catalogReady(w, r) {
		return
	}
	after := r.URL.Query().Get("after")
	if after != "" && !storage.ValidUUIDString(after) {
		catalogError(w, 400, "invalid_request")
		return
	}
	roots, err := a.store.ListRootsAfter(r.Context(), after, 101)
	if err != nil {
		adminFailure(w, err)
		return
	}
	var next *string
	if len(roots) > 100 {
		roots = roots[:100]
		next = &roots[99].ID
	}
	out := []adminRoot{}
	for _, root := range roots {
		out = append(out, adminRoot{root, root.CanonicalPath})
	}
	jsonResponse(w, map[string]any{"items": out, "next": next})
}

func validAdminPath(path string) bool {
	if len(path) > 4096 || !utf8.ValidString(path) || !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return false
	}
	for _, c := range path {
		if c < 32 {
			return false
		}
	}
	tail := strings.TrimPrefix(path, filepath.VolumeName(path))
	return !strings.Contains(tail, ":")
}

func (a *app) adminAddRoot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if decodeUserJSON(r, &req, false) != nil || !validAdminPath(req.Path) || strings.TrimSpace(req.Name) == "" || len(req.Name) > 128 || !utf8.ValidString(req.Name) {
		catalogError(w, 400, "invalid_request")
		return
	}
	if !a.catalogReady(w, r) {
		return
	}
	path, err := library.CanonicalizeRoot(req.Path)
	if err != nil {
		catalogError(w, 422, "folder_unavailable")
		return
	}
	if !validAdminPath(path) {
		catalogError(w, 400, "invalid_request")
		return
	}
	identity, ok, err := library.CaptureRootIdentity(path)
	if err != nil {
		catalogError(w, 422, "folder_unavailable")
		return
	}
	var root storage.LibraryRoot
	if ok {
		root, err = a.store.AddRootWithIdentity(r.Context(), req.Name, path, identity)
	} else {
		root, err = a.store.AddRoot(r.Context(), req.Name, path)
	}
	if err != nil {
		adminFailure(w, err)
		return
	}
	a.log.Info("host_root_operation", "request_id", w.Header().Get("X-Request-ID"), "action", "add", "root_id", root.ID)
	jsonResponseStatus(w, 201, adminRoot{root, root.CanonicalPath})
}

func (a *app) adminRootAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	if !storage.ValidUUIDString(id) {
		catalogError(w, 400, "invalid_request")
		return
	}
	if action != "verify" && action != "enable" && action != "disable" && action != "scan" {
		catalogError(w, 404, "not_found")
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if decodeUserJSON(r, &req, false) != nil || action == "verify" && !req.Confirm {
		catalogError(w, 400, "invalid_request")
		return
	}
	if !a.catalogReady(w, r) {
		return
	}
	root, err := a.store.GetRoot(r.Context(), id)
	if err != nil {
		adminFailure(w, err)
		return
	}
	switch action {
	case "disable":
		err = a.store.DisableRoot(r.Context(), id)
	case "verify", "enable":
		identity, ok, e := library.CaptureRootIdentity(root.CanonicalPath)
		if e != nil {
			catalogError(w, 422, "folder_unavailable")
			return
		}
		if !ok {
			adminFailure(w, storage.ErrRootIdentityUnavailable)
			return
		}
		if action == "verify" {
			_, err = a.store.VerifyRootIdentity(r.Context(), id, identity)
		} else {
			_, err = a.store.EnableRoot(r.Context(), id, identity)
		}
	case "scan":
		scanner := library.Scanner{Store: a.store, Log: a.log}
		result, e := scanner.Scan(r.Context(), id)
		if e != nil {
			adminFailure(w, e)
			return
		}
		jsonResponse(w, result)
		return
	}
	if err != nil {
		adminFailure(w, err)
		return
	}
	a.log.Info("host_root_operation", "request_id", w.Header().Get("X-Request-ID"), "action", action, "root_id", id)
	jsonResponse(w, map[string]string{"status": "saved"})
}

func adminFailure(w http.ResponseWriter, err error) {
	for _, item := range []struct {
		err    error
		code   string
		status int
	}{
		{storage.ErrRootNotFound, "not_found", 404}, {storage.ErrRootOverlap, "folder_overlap", 409}, {storage.ErrRootDisabled, "folder_disabled", 409},
		{storage.ErrRootUnverified, "verification_required", 409}, {storage.ErrRootQuarantined, "folder_quarantined", 409}, {storage.ErrRootIdentityMismatch, "folder_changed", 409},
		{storage.ErrRootIdentityUnavailable, "identity_unavailable", 422}, {storage.ErrRootUnavailable, "folder_unavailable", 422}, {storage.ErrScanRunning, "scan_running", 409},
	} {
		if errors.Is(err, item.err) {
			catalogError(w, item.status, item.code)
			return
		}
	}
	catalogError(w, 503, "operation_unavailable")
}
