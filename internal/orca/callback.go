package orca

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// callbackPage is served to the browser after the redirect lands. The user is
// told they can close the tab instead of being left at a blank window while
// the process exits.
const callbackPage = `<!doctype html><meta charset="utf-8"><title>phi</title>
<body style="font:16px system-ui;padding:2rem;background:#0e1218;color:#edf2f7">
<p>Connected. You can close this tab and return to phi.</p></body>`

// CallbackPath is the loopback path the consent screen redirects to.
const CallbackPath = "/cb"

// CallbackListener is a Flow A loopback redirect listener. It is started
// before the browser opens, so the port is known and nothing races, and it is
// closed on every terminal path.
type CallbackListener struct {
	listener net.Listener
	server   *http.Server
	port     int

	mu       sync.Mutex
	code     string
	err      error
	received chan struct{}
	done     bool
}

// ListenForCallback starts a loopback listener bound to 127.0.0.1 on an
// ephemeral port. The returned listener serves the redirect and resolves the
// code for the attempt it was created for.
func ListenForCallback(conn *Connect) (*CallbackListener, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open a loopback callback listener: %w", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, errors.New("loopback listener has an unexpected address type")
	}
	cl := &CallbackListener{
		listener: ln,
		port:     addr.Port,
		received: make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		cl.handle(conn, w, r)
	})
	cl.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = cl.server.Serve(ln) }()
	return cl, nil
}

// CallbackURL is the loopback redirect address to send as callback_url.
func (c *CallbackListener) CallbackURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", c.port, CallbackPath)
}

// Port reports the bound port.
func (c *CallbackListener) Port() int { return c.port }

func (c *CallbackListener) handle(conn *Connect, w http.ResponseWriter, r *http.Request) {
	// The browser gets its page regardless of the outcome; a user staring at a
	// 403 cannot tell whether to retry.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(callbackPage))

	attempt := conn.Status().Attempt
	code, err := conn.CallbackQuery(attempt, r.URL.Query())
	c.resolve(code, err)
}

func (c *CallbackListener) resolve(code string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return
	}
	c.done = true
	c.code = code
	c.err = err
	close(c.received)
}

// Wait blocks until the redirect arrives or ctx ends, then closes the
// listener. A closed listener is what releases the port on every path.
func (c *CallbackListener) Wait(ctx context.Context) (string, error) {
	defer c.Close()
	select {
	case <-c.received:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.code, c.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Close shuts the listener down. It is idempotent.
func (c *CallbackListener) Close() {
	if c.server != nil {
		_ = c.server.Close()
		return
	}
	if c.listener != nil {
		_ = c.listener.Close()
	}
}
