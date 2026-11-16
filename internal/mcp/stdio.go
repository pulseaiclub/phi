package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/util"
)

const defaultTimeout = 60 * time.Second

// errResponseIDMismatch marks a response whose ID differs from the pending request.
var errResponseIDMismatch = errors.New("response id mismatch")

// stdioTransport speaks newline-delimited JSON-RPC over a subprocess.
type stdioTransport struct {
	name    string
	cfg     ServerConfig
	timeout time.Duration
	id      atomic.Int64

	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	stderr   *os.File
	readerWG sync.WaitGroup
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

	stdout := t.stdout
	stdin := t.stdin

	deadline := t.requestDeadline(ctx)
	type outcome struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan outcome, 1)
	t.readerWG.Add(1)
	go func() {
		defer t.readerWG.Done()
		if _, werr := stdin.Write(payload); werr != nil {
			ch <- outcome{err: fmt.Errorf("write %s: %w", method, werr)}
			return
		}
		rpc, rerr := readResponse(stdout, stdin, id)
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
		t.readerWG.Wait()
		err = t.cmd.Wait()
		t.cmd = nil
	} else {
		t.readerWG.Wait()
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

// readResponse reads frames until the JSON-RPC response carrying id.
// Server-initiated requests (carrying both method and id) are answered so a
// peer waiting on e.g. ping does not stall; notifications are skipped. A
// response carrying another id means the stream is out of step (e.g. a late
// reply to a timed-out call), which is reported instead of being taken as this
// call's.
func readResponse(stdout *bufio.Reader, stdin io.Writer, id int64) (jsonRPCResponse, error) {
	for {
		line, err := stdout.ReadBytes('\n')
		if err != nil {
			return jsonRPCResponse{}, err
		}
		var rpc jsonRPCResponse
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&rpc); err != nil {
			return jsonRPCResponse{}, fmt.Errorf(
				"parse response: %w; raw=%q",
				err,
				util.TruncateBytes(string(line), 200, "…"),
			)
		}
		if rpc.Method != "" {
			if rpc.ID != nil {
				_ = replyServerRequest(stdin, rpc.ID, rpc.Method)
			}
			continue
		}
		got, ok := responseID(rpc.ID)
		if !ok {
			// A parse-error response carries a null id; surface it rather than
			// reading on until EOF hides the reason.
			if rpc.ID == nil && rpc.Error != nil {
				return rpc, nil
			}
			continue
		}
		if got != id {
			return jsonRPCResponse{}, fmt.Errorf(
				"%w: response id %d does not match request %d",
				errResponseIDMismatch,
				got,
				id,
			)
		}
		return rpc, nil
	}
}

func replyServerRequest(stdin io.Writer, id any, method string) error {
	if stdin == nil {
		return nil
	}
	var msg map[string]any
	if method == "ping" {
		msg = map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  map[string]any{},
		}
	} else {
		msg = map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found",
			},
		}
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	_, err = stdin.Write(payload)
	return err
}

// responseID decodes a JSON-RPC id. Requests use integers, but a gateway that
// echoes them as numeric strings still identifies the right call.
func responseID(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	default:
		return 0, false
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
