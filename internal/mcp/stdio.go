package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/util"
)

const defaultTimeout = 60 * time.Second

// stdioTransport speaks newline-delimited JSON-RPC over a subprocess.
type stdioTransport struct {
	name    string
	cfg     ServerConfig
	timeout time.Duration
	id      atomic.Int64

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *os.File
}

func newStdioTransport(name string, cfg ServerConfig) (*stdioTransport, error) {
	if _, err := cfg.CmdLine(); err != nil {
		return nil, fmt.Errorf("server %q: %w", name, err)
	}
	return &stdioTransport{
		name:    name,
		cfg:     cfg,
		timeout: defaultTimeout,
	}, nil
}

func (t *stdioTransport) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if err := t.ensureStarted(); err != nil {
		return nil, err
	}
	id := nextID(&t.id)
	payload, err := marshalRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')

	deadline := t.requestDeadline(ctx)
	type outcome struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		if _, werr := t.stdin.Write(payload); werr != nil {
			ch <- outcome{err: fmt.Errorf("write %s: %w", method, werr)}
			return
		}
		rpc, rerr := t.readResponse()
		if rerr != nil {
			ch <- outcome{err: fmt.Errorf("read %s: %w", method, rerr)}
			return
		}
		raw, err := resultOrError(method, rpc)
		ch <- outcome{raw: raw, err: err}
	}()

	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("mcp %s: timeout after %s", method, deadline)
	case out := <-ch:
		return out.raw, out.err
	}
}

func (t *stdioTransport) notify(_ context.Context, method string, params map[string]any) error {
	if err := t.ensureStarted(); err != nil {
		return err
	}
	payload, err := marshalNotification(method, params)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	_, err = t.stdin.Write(payload)
	return err
}

func (t *stdioTransport) close() error {
	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
	var err error
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		err = t.cmd.Wait()
		t.cmd = nil
	}
	if t.stderr != nil {
		_ = t.stderr.Close()
		t.stderr = nil
	}
	t.stdout = nil
	return err
}

func (t *stdioTransport) ensureStarted() error {
	if t.cmd != nil {
		return nil
	}
	argv, err := t.cfg.CmdLine()
	if err != nil {
		return fmt.Errorf("server %q: %w", t.name, err)
	}
	logDir, err := LogDir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(logDir, sanitizeName(t.name)+".log")
	stderr, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open mcp log: %w", err)
	}

	//nolint:gosec,noctx // G204: MCP server argv is user config; lifetime owned by Close/Kill
	cmd := exec.Command(argv[0], argv[1:]...)
	env := os.Environ()
	for k, v := range t.cfg.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	cmd.Stderr = stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = stderr.Close()
		return err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stderr.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stderr.Close()
		return fmt.Errorf("spawn %q: %w", t.name, err)
	}
	t.cmd = cmd
	t.stdin = stdin
	t.stdout = bufio.NewReader(stdoutPipe)
	t.stderr = stderr
	return nil
}

func (t *stdioTransport) readResponse() (jsonRPCResponse, error) {
	for {
		line, err := t.stdout.ReadBytes('\n')
		if err != nil {
			return jsonRPCResponse{}, err
		}
		var rpc jsonRPCResponse
		if err := json.Unmarshal(line, &rpc); err != nil {
			return jsonRPCResponse{}, fmt.Errorf(
				"parse response: %w; raw=%q",
				err,
				util.TruncateBytes(string(line), 200, "…"),
			)
		}
		// Skip server notifications (method set, no id).
		if rpc.Method != "" && rpc.ID == nil {
			continue
		}
		return rpc, nil
	}
}

func (t *stdioTransport) requestDeadline(ctx context.Context) time.Duration {
	deadline := t.timeout
	if deadline <= 0 {
		deadline = defaultTimeout
	}
	if d, ok := ctx.Deadline(); ok {
		if left := time.Until(d); left > 0 && left < deadline {
			return left
		}
	}
	return deadline
}
