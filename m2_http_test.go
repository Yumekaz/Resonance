package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func localAdminRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:8081"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:45678"
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8081}))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Resonance-Admin", "1")
	}
	return r
}
func TestM2HostListenerAddressIsTruthful(t *testing.T) {
	for _, tc := range []struct{ address, mode, url string }{{"127.0.0.1:8095", "local", "http://127.0.0.1:8095/"}, {"192.168.1.20:8080", "network", "http://192.168.1.20:8080/"}, {"0.0.0.0:8080", "network", ""}} {
		handler := newAdminHandler(nil, nil, nil, io.Discard, tc.address)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, localAdminRequest("GET", "/api/v1/admin/status", ""))
		var result struct {
			Listener map[string]string `json:"listener"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Listener["mode"] != tc.mode || result.Listener["url"] != tc.url {
			t.Fatalf("listener metadata %s %d %s", tc.address, w.Code, w.Body.String())
		}
	}
}

func TestM2AdminBoundary(t *testing.T) {
	handler := newAdminHandler(nil, nil, nil, io.Discard)
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		want   int
	}{
		{"direct loopback", func(r *http.Request) {}, 200},
		{"LAN peer", func(r *http.Request) { r.RemoteAddr = "192.168.1.9:5000" }, 403},
		{"forged forwarded peer", func(r *http.Request) {
			r.RemoteAddr = "192.168.1.9:5000"
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("Forwarded", "for=127.0.0.1")
		}, 403},
		{"LAN listening socket", func(r *http.Request) {
			*r = *r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.168.1.2"), Port: 8081}))
		}, 403},
		{"no socket evidence", func(r *http.Request) { *r = *r.WithContext(context.Background()) }, 403},
		{"DNS rebinding", func(r *http.Request) { r.Host = "attacker.example:8081" }, 403},
		{"wrong port", func(r *http.Request) { r.Host = "127.0.0.1:8080" }, 403},
		{"cross origin", func(r *http.Request) { r.Header.Set("Origin", "http://attacker.example") }, 403},
		{"listener origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:8080") }, 403},
		{"cross site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"same site different origin", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, 403},
		{"same origin", func(r *http.Request) {
			r.Header.Set("Origin", "http://127.0.0.1:8081")
			r.Header.Set("Sec-Fetch-Site", "same-origin")
		}, 200},
		{"IPv6 loopback", func(r *http.Request) { r.RemoteAddr = "[::1]:5000"; r.Host = "[::1]:8081" }, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := localAdminRequest("GET", "/api/v1/admin/status", "")
			tc.change(r)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("host data cacheable")
			}
		})
	}
	for _, tc := range []struct{ name, method, content, header string }{
		{"simple form", "POST", "application/x-www-form-urlencoded", ""}, {"text", "POST", "text/plain", "1"}, {"missing custom header", "POST", "application/json", ""}, {"preflight", "OPTIONS", "application/json", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := localAdminRequest(tc.method, "/api/v1/admin/roots", "{}")
			r.Header.Set("Content-Type", tc.content)
			r.Header.Set("X-Resonance-Admin", tc.header)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("got %d", w.Code)
			}
		})
	}
}

func TestM2PublicListenerHasNoHostRoutes(t *testing.T) {
	handler := newHandler("missing.wav", "Demo", io.Discard)
	for _, path := range []string{"/api/v1/admin/roots", "/api/v1/admin/status", "/admin/", "/admin/index.html", "/admin.js"} {
		for _, method := range []string{"GET", "POST"} {
			r := localAdminRequest(method, path, "{}")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 404 && w.Code != 405 {
				t.Fatalf("public %s %s: %d", method, path, w.Code)
			}
		}
	}
}

func TestM2AdminBindValidation(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8081", "192.168.1.2:8081", "localhost:8081", ":8081", "[::]:8081", "bad"} {
		if loopbackAddress(address) {
			t.Fatalf("accepted %s", address)
		}
	}
	for _, address := range []string{"127.0.0.1:8081", "[::1]:8081"} {
		if !loopbackAddress(address) {
			t.Fatalf("rejected %s", address)
		}
	}
}

func TestM2SearchInputBeforeDatabase(t *testing.T) {
	handler := newHandler("missing.wav", "Demo", io.Discard)
	for _, query := range []string{"limit=0", "limit=51", "limit=bad", "q=a&q=b", "limit=1&limit=2", "q=" + strings.Repeat("a", 121), "q=%ff", "q=%00"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/search?"+query, nil))
		if w.Code != 400 {
			t.Fatalf("query %q got %d", query, w.Code)
		}
	}
}
