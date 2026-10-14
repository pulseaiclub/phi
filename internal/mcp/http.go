package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/util"
)

const (
	httpNotifyTimeout = 5 * time.Second
	httpMaxBody       = 8 << 20 // 8 MiB
	headerSessionID   = "Mcp-Session-Id"
)

// httpTransport speaks JSON-RPC over HTTP POST (plain JSON or SSE body).
type httpTransport struct {
	name      string
	url       string
	headers   map[string]string
	client    *http.Client
	id        atomic.Int64
	sessionID string
}

func newHTTPTransport(name string, cfg ServerConfig) (*httpTransport, error) {
	url := strings.TrimSpace(cfg.URL)
	if url == "" {
		return nil, fmt.Errorf("server %q: http transport requires url", name)
	}
	headers := make(map[string]string, len(cfg.Headers))
	maps.Copy(headers, cfg.Headers)
	return &httpTransport{
		name:    name,
		url:     url,
		headers: headers,
		client: &http.Client{
			Timeout: defaultTimeout,
			Transport: &http.Transport{
				Proxy: nil, // prefer direct; MCP endpoints are often local
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
	}, nil
}

func (t *httpTransport) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id := nextID(&t.id)
	payload, err := marshalRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	req, err := t.newRequest(ctx, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp http %s: %w", method, err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get(headerSessionID); sid != "" {
		t.sessionID = sid
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody))
	if err != nil {
		return nil, fmt.Errorf("mcp http %s read: %w", method, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf(
			"mcp http %s: HTTP %d: %s",
			method,
			resp.StatusCode,
			util.TruncateBytes(string(body), 300, "…"),
		)
	}

	rpc, err := parseHTTPOrSSEBody(body)
	if err != nil {
		return nil, err
	}
	return resultOrError(method, rpc)
}

func (t *httpTransport) notify(ctx context.Context, method string, params map[string]any) error {
	payload, err := marshalNotification(method, params)
	if err != nil {
		return err
	}
	nctx, cancel := context.WithTimeout(ctx, httpNotifyTimeout)
	defer cancel()
	req, err := t.newRequest(nctx, payload)
	if err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil // fire-and-forget
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return nil
}

func (t *httpTransport) close() error {
	t.sessionID = ""
	if t.client != nil {
		t.client.CloseIdleConnections()
	}
	return nil
}

func (t *httpTransport) newRequest(ctx context.Context, payload []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if t.sessionID != "" {
		req.Header.Set(headerSessionID, t.sessionID)
	}
	return req, nil
}
