package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
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
	hadSession := t.sessionID != ""
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
		if isContextError(err) {
			return nil, fmt.Errorf("mcp http %s: %w", method, err)
		}
		return nil, t.brokenError(method, err)
	}
	defer resp.Body.Close()

	if method == "initialize" && resp.StatusCode < http.StatusBadRequest {
		if sid := resp.Header.Get(headerSessionID); sid != "" {
			t.sessionID = sid
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody))
	if err != nil {
		if isContextError(err) {
			return nil, fmt.Errorf("mcp http %s read: %w", method, err)
		}
		return nil, t.brokenError(method, err)
	}
	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusNotFound && hadSession {
			return nil, t.brokenError(method, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 300)))
		}
		return nil, fmt.Errorf("mcp http %s: HTTP %d: %s", method, resp.StatusCode, truncate(string(body), 300))
	}

	rpc, err := parseHTTPOrSSEBody(body)
	if err != nil {
		return nil, err
	}
	return resultOrError(method, rpc)
}

func (t *httpTransport) notify(ctx context.Context, method string, params map[string]any) error {
	hadSession := t.sessionID != ""
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
		if isContextError(err) {
			return fmt.Errorf("mcp http notify %s: %w", method, err)
		}
		return t.brokenError("notify "+method, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusNotFound && hadSession {
			return t.brokenError("notify "+method, fmt.Errorf("HTTP %d", resp.StatusCode))
		}
		return fmt.Errorf("mcp http notify %s: HTTP %d", method, resp.StatusCode)
	}
	return nil
}

func (t *httpTransport) brokenError(method string, err error) error {
	t.resetSession()
	return fmt.Errorf("mcp http %s: %w: %w", method, errTransportBroken, err)
}

func (t *httpTransport) resetSession() {
	t.sessionID = ""
	if t.client != nil {
		t.client.CloseIdleConnections()
	}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (t *httpTransport) close() error {
	t.resetSession()
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
