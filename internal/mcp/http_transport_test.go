package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestHTTP404WithSessionRestarts(t *testing.T) {
	var mu sync.Mutex
	initCount := 0
	listCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := readHTTPMethod(t, r)
		switch method {
		case "initialize":
			mu.Lock()
			initCount++
			count := initCount
			mu.Unlock()
			w.Header().Set(headerSessionID, fmt.Sprintf("sid-%d", count))
			writeHTTPTestJSONRPC(w, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			mu.Lock()
			listCount++
			mu.Unlock()
			if r.Header.Get(headerSessionID) == "sid-1" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeHTTPTestJSONRPC(w, map[string]any{
				"tools": []map[string]string{{"name": "echo"}},
			})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	tr, err := newHTTPTransport("demo", ServerConfig{Transport: "http", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession("demo", tr)
	if _, err := s.ListTools(t.Context()); !errors.Is(err, errTransportBroken) {
		t.Fatalf("err = %v, want transport error", err)
	}

	tools, err := s.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want echo after restart", tools)
	}
	mu.Lock()
	gotInit, gotList := initCount, listCount
	mu.Unlock()
	if gotInit != 2 || gotList != 2 {
		t.Fatalf("initialize/list calls = %d/%d, want 2/2", gotInit, gotList)
	}
}

func TestHTTP404WithoutSessionIsOrdinaryError(t *testing.T) {
	var mu sync.Mutex
	initCount := 0
	listCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := readHTTPMethod(t, r)
		switch method {
		case "initialize":
			mu.Lock()
			initCount++
			mu.Unlock()
			writeHTTPTestJSONRPC(w, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			mu.Lock()
			listCount++
			mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	tr, err := newHTTPTransport("demo", ServerConfig{Transport: "http", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession("demo", tr)
	for range 2 {
		if _, err := s.ListTools(t.Context()); err == nil || errors.Is(err, errTransportBroken) {
			t.Fatalf("err = %v, want ordinary HTTP error", err)
		}
	}
	mu.Lock()
	gotInit, gotList := initCount, listCount
	mu.Unlock()
	if gotInit != 1 || gotList != 2 {
		t.Fatalf("initialize/list calls = %d/%d, want 1/2", gotInit, gotList)
	}
}

func TestHTTPNotify404Restarts(t *testing.T) {
	var mu sync.Mutex
	initCount := 0
	notifyCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := readHTTPMethod(t, r)
		switch method {
		case "initialize":
			mu.Lock()
			initCount++
			count := initCount
			mu.Unlock()
			w.Header().Set(headerSessionID, fmt.Sprintf("sid-%d", count))
			writeHTTPTestJSONRPC(w, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{},
			})
		case "notifications/initialized":
			mu.Lock()
			notifyCount++
			count := notifyCount
			mu.Unlock()
			if count == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPTestJSONRPC(w, map[string]any{
				"tools": []map[string]string{{"name": "echo"}},
			})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	tr, err := newHTTPTransport("demo", ServerConfig{Transport: "http", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession("demo", tr)
	if _, err := s.ListTools(t.Context()); !errors.Is(err, errTransportBroken) {
		t.Fatalf("err = %v, want transport error", err)
	}
	if _, err := s.ListTools(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotInit, gotNotify := initCount, notifyCount
	mu.Unlock()
	if gotInit != 2 || gotNotify != 2 {
		t.Fatalf("initialize/notify calls = %d/%d, want 2/2", gotInit, gotNotify)
	}
}

func TestHTTPNotifyNetworkErrorIsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := readHTTPMethod(t, r)
		if method == "initialize" {
			writeHTTPTestJSONRPC(w, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{},
			})
			return
		}
		if method == "notifications/initialized" {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("server does not support hijacking")
				return
			}
			conn, _, err := hijacker.Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		http.Error(w, "unexpected", http.StatusBadRequest)
	}))
	defer srv.Close()

	tr, err := newHTTPTransport("demo", ServerConfig{Transport: "http", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession("demo", tr)
	if _, err := s.ListTools(t.Context()); !errors.Is(err, errTransportBroken) {
		t.Fatalf("err = %v, want transport error", err)
	}
}

func readHTTPMethod(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	return request.Method
}

func writeHTTPTestJSONRPC(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result":  result,
	})
}
