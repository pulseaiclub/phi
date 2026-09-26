package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension/manifest"
	"github.com/pulseaiclub/phi/internal/version"
)

const (
	handshakeTimeout = 5 * time.Second
	rpcTimeout       = 30 * time.Second
	rpcTimeoutMax    = 3600 * time.Second // matches bash tool upper bound
	shutdownWait     = 2 * time.Second
)

// Proc is one extension subprocess speaking PXB over stdin/stdout.
type Proc struct {
	Manifest manifest.Manifest
	Dir      string
	LogPath  string

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	logFile *os.File

	wr *pxb.Writer
	rd *pxb.Reader

	hello     pxb.Hello
	cmds      []pxb.RegisterCommand
	tools     []pxb.RegisterTool
	events    map[uint16]struct{}
	intercept map[uint16]struct{}

	mu       sync.Mutex
	nextID   atomic.Uint32
	pending  map[uint32]chan frameResult
	closed   atomic.Bool
	stopOnce sync.Once

	onNotify      func(pxb.NotifyMsg)
	onHostRequest func(id uint32, hasID bool, req pxb.HostRequest)
}

type frameResult struct {
	frame pxb.Frame
	err   error
}

// StartProc launches manifest.Exec and completes the PXB handshake.
func StartProc(ctx context.Context, m manifest.Manifest, dir, logDir, cwd, sessionID string) (*Proc, error) {
	if m.Exec == "" {
		return nil, errors.New("extension: empty exec")
	}
	execPath := m.Exec
	if !filepath.IsAbs(execPath) {
		execPath = filepath.Join(dir, execPath)
	}
	if st, err := os.Stat(execPath); err != nil {
		return nil, fmt.Errorf("extension %q: exec %s: %w", m.Name, execPath, err)
	} else if st.IsDir() {
		return nil, fmt.Errorf("extension %q: exec %s is a directory", m.Name, execPath)
	}

	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(logDir, "ext-"+m.Name+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, execPath, m.Args...)
	cmd.Dir = dir
	cmd.Stderr = logFile
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("extension %q: start: %w", m.Name, err)
	}

	p := &Proc{
		Manifest:  m,
		Dir:       dir,
		LogPath:   logPath,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		logFile:   logFile,
		wr:        pxb.NewWriter(stdin),
		rd:        pxb.NewReader(stdout),
		events:    make(map[uint16]struct{}),
		intercept: make(map[uint16]struct{}),
		pending:   make(map[uint32]chan frameResult),
	}

	hsCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := p.handshake(hsCtx, cwd, sessionID); err != nil {
		_ = p.Close()
		return nil, err
	}
	go p.readLoop()
	return p, nil
}

// SetNotifyHandler installs the callback for spontaneous Notify frames.
// Install it before the host starts using the process; the read loop reads it.
func (p *Proc) SetNotifyHandler(fn func(pxb.NotifyMsg)) {
	if p == nil {
		return
	}
	p.onNotify = fn
}

// SetHostRequestHandler installs the callback for HostRequest frames.
func (p *Proc) SetHostRequestHandler(fn func(id uint32, hasID bool, req pxb.HostRequest)) {
	if p == nil {
		return
	}
	p.onHostRequest = fn
}

func (p *Proc) handshake(ctx context.Context, cwd, sessionID string) error {
	f, err := p.readFrame(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return fmt.Errorf("extension %q: hello timeout", p.Manifest.Name)
		}
		return fmt.Errorf("extension %q: read hello: %w", p.Manifest.Name, err)
	}
	if f.Type != pxb.TypeHello {
		return fmt.Errorf("extension %q: first frame type %d want hello", p.Manifest.Name, f.Type)
	}
	hello, err := pxb.DecodeHello(pxb.CloneBody(f))
	if err != nil {
		return fmt.Errorf("extension %q: decode hello: %w", p.Manifest.Name, err)
	}
	if hello.Name != "" && hello.Name != p.Manifest.Name {
		debuglog.Logf("extension: manifest name %q != hello name %q", p.Manifest.Name, hello.Name)
	}
	if hello.Protocol != 0 && hello.Protocol != pxb.ProtocolVersion {
		return fmt.Errorf(
			"extension %q: protocol %d unsupported (want %d)",
			p.Manifest.Name,
			hello.Protocol,
			pxb.ProtocolVersion,
		)
	}
	p.hello = hello

	ack := pxb.EncodeHelloAck(pxb.HelloAck{
		Protocol:     pxb.ProtocolVersion,
		PhiVersion:   version.Version,
		Cwd:          cwd,
		SessionID:    sessionID,
		ExtensionDir: p.Dir,
	})
	if err := p.write(ctx, pxb.TypeHelloAck, 0, 0, ack); err != nil {
		return err
	}

	for {
		frame, err := p.readFrame(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return fmt.Errorf("extension %q: registration timeout", p.Manifest.Name)
			}
			return fmt.Errorf("extension %q: registration: %w", p.Manifest.Name, err)
		}
		body := pxb.CloneBody(frame)
		switch frame.Type {
		case pxb.TypeRegisterCommand:
			rc, err := pxb.DecodeRegisterCommand(body)
			if err != nil {
				return err
			}
			p.cmds = append(p.cmds, rc)
		case pxb.TypeRegisterTool:
			rt, err := pxb.DecodeRegisterTool(body)
			if err != nil {
				return err
			}
			p.tools = append(p.tools, rt)
		case pxb.TypeSubscribe:
			sub, err := pxb.DecodeSubscribe(body)
			if err != nil {
				return err
			}
			for _, e := range sub.Events {
				p.events[e] = struct{}{}
			}
			for _, e := range sub.Intercept {
				p.intercept[e] = struct{}{}
			}
		case pxb.TypeReady:
			return nil
		default:
			return fmt.Errorf("extension %q: unexpected frame %d before ready", p.Manifest.Name, frame.Type)
		}
	}
}

// readFrame reads one frame, aborting when ctx is done.
func (p *Proc) readFrame(ctx context.Context) (pxb.Frame, error) {
	type result struct {
		f   pxb.Frame
		err error
	}
	ch := make(chan result, 1)
	go func() {
		f, err := p.rd.Read()
		ch <- result{f: f, err: err}
	}()
	select {
	case <-ctx.Done():
		return pxb.Frame{}, ctx.Err()
	case r := <-ch:
		return r.f, r.err
	}
}

func (p *Proc) readLoop() {
	for {
		f, err := p.rd.Read()
		if err != nil {
			p.terminate()
			return
		}
		body := pxb.CloneBody(f)
		f.Body = body
		if f.Flags&pxb.FlagHasID != 0 && isResponseType(f.Type) {
			p.mu.Lock()
			ch, ok := p.pending[f.ID]
			if ok {
				delete(p.pending, f.ID)
			}
			p.mu.Unlock()
			if ok {
				ch <- frameResult{frame: f}
				continue
			}
		}
		switch f.Type {
		case pxb.TypeNotify:
			n, err := pxb.DecodeNotify(f.Body)
			if err != nil {
				debuglog.Logf("extension %q: notify decode: %v", p.Manifest.Name, err)
				continue
			}
			if p.onNotify != nil {
				p.onNotify(n)
			}
		case pxb.TypeHostRequest:
			req, err := pxb.DecodeHostRequest(f.Body)
			if err != nil {
				debuglog.Logf("extension %q: host request decode: %v", p.Manifest.Name, err)
				continue
			}
			if p.onHostRequest != nil {
				p.onHostRequest(f.ID, f.Flags&pxb.FlagHasID != 0, req)
			}
		case pxb.TypeShutdownAck:
			return
		default:
			debuglog.Logf("extension %q: unsolicited frame type %d", p.Manifest.Name, f.Type)
		}
	}
}

func (p *Proc) failPending(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, ch := range p.pending {
		ch <- frameResult{err: err}
		delete(p.pending, id)
	}
}

func isResponseType(typ uint16) bool {
	switch typ {
	case pxb.TypeCommandResponse, pxb.TypeToolResult, pxb.TypeInterceptResponse, pxb.TypeToolDetailResult:
		return true
	default:
		return false
	}
}

func (p *Proc) rpc(ctx context.Context, typ uint16, body []byte, want uint16) (pxb.Frame, error) {
	return p.rpcWait(ctx, typ, body, want, 0)
}

// rpcWait is rpc with an optional per-call wait override (0 = host default).
// When override > 0, the wait is clamp(override) capped by any ctx deadline.
// When override == 0, legacy behavior: ctx deadline replaces the default if set.
func (p *Proc) rpcWait(
	ctx context.Context,
	typ uint16,
	body []byte,
	want uint16,
	override time.Duration,
) (pxb.Frame, error) {
	if p == nil || p.closed.Load() {
		return pxb.Frame{}, errors.New("extension: process closed")
	}
	if err := ctx.Err(); err != nil {
		return pxb.Frame{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, rpcWaitDuration(ctx, override))
	defer cancel()
	stop := p.watch(waitCtx)
	defer stop()
	id := p.nextID.Add(1)
	ch := make(chan frameResult, 1)
	p.mu.Lock()
	if p.closed.Load() {
		p.mu.Unlock()
		return pxb.Frame{}, errors.New("extension: process closed")
	}
	p.pending[id] = ch
	p.mu.Unlock()

	if err := p.wr.Write(typ, pxb.FlagHasID, id, body); err != nil {
		p.terminate()
		if waitCtx.Err() != nil {
			return pxb.Frame{}, waitCtx.Err()
		}
		return pxb.Frame{}, err
	}

	select {
	case <-waitCtx.Done():
		p.terminate()
		return pxb.Frame{}, waitCtx.Err()
	case res := <-ch:
		if res.err != nil {
			if waitCtx.Err() != nil {
				return pxb.Frame{}, waitCtx.Err()
			}
			return pxb.Frame{}, res.err
		}
		if want != 0 && res.frame.Type != want {
			return pxb.Frame{}, fmt.Errorf(
				"extension %q: rpc reply type %d want %d",
				p.Manifest.Name,
				res.frame.Type,
				want,
			)
		}
		return res.frame, nil
	}
}

func rpcWaitDuration(ctx context.Context, override time.Duration) time.Duration {
	wait := rpcTimeout
	if override > 0 {
		wait = min(override, rpcTimeoutMax)
	}
	if d, ok := ctx.Deadline(); ok {
		rem := time.Until(d)
		if override > 0 {
			wait = min(wait, rem)
		} else {
			wait = rem
		}
	}
	if wait <= 0 {
		return time.Millisecond
	}
	return wait
}

func (p *Proc) toolRPCTimeout(name string) time.Duration {
	for _, t := range p.tools {
		if t.Name == name && t.TimeoutSec > 0 {
			return time.Duration(t.TimeoutSec) * time.Second
		}
	}
	return 0
}

// CallTool invokes a registered tool.
func (p *Proc) CallTool(ctx context.Context, name string, args json.RawMessage) (ext.ToolResult, error) {
	body := pxb.EncodeToolInvoke(pxb.ToolInvoke{Name: name, Args: args})
	f, err := p.rpcWait(ctx, pxb.TypeToolInvoke, body, pxb.TypeToolResult, p.toolRPCTimeout(name))
	if err != nil {
		return ext.ToolResult{}, err
	}
	tr, err := pxb.DecodeToolResult(f.Body)
	if err != nil {
		return ext.ToolResult{}, err
	}
	if tr.IsError {
		if tr.Error == "" {
			tr.Error = tr.Content
		}
		res := ext.ToolResult{Content: tr.Content, Detail: tr.Detail, Output: tr.Output, Expanded: tr.Expanded}
		return res, errors.New(tr.Error)
	}
	return ext.ToolResult{Content: tr.Content, Detail: tr.Detail, Output: tr.Output, Expanded: tr.Expanded}, nil
}

// CallToolDetail asks the extension for a one-line TUI detail for raw args.
// Uses the default RPC budget (not the tool's TimeoutSec); detail must be cheap.
func (p *Proc) CallToolDetail(ctx context.Context, name string, args json.RawMessage) (string, error) {
	body := pxb.EncodeToolInvoke(pxb.ToolInvoke{Name: name, Args: args})
	f, err := p.rpcWait(ctx, pxb.TypeToolDetailInvoke, body, pxb.TypeToolDetailResult, 0)
	if err != nil {
		return "", err
	}
	tr, err := pxb.DecodeToolDetailResult(f.Body)
	if err != nil {
		return "", err
	}
	return tr.Detail, nil
}

// CallCommand runs a slash command.
func (p *Proc) CallCommand(ctx context.Context, name, args string) (pxb.CommandResponse, error) {
	body := pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: name, Args: args})
	f, err := p.rpc(ctx, pxb.TypeCommandInvoked, body, pxb.TypeCommandResponse)
	if err != nil {
		return pxb.CommandResponse{}, err
	}
	resp, err := pxb.DecodeCommandResponse(f.Body)
	if err != nil {
		return pxb.CommandResponse{}, err
	}
	if resp.Notify != "" && p.onNotify != nil {
		p.onNotify(pxb.NotifyMsg{Level: "info", Message: resp.Notify})
	}
	return resp, nil
}

// Intercept runs a request/response intercept for subscribed events.
func (p *Proc) Intercept(ctx context.Context, req pxb.InterceptReq) (pxb.InterceptResp, error) {
	if _, ok := p.intercept[req.Event]; !ok {
		return pxb.InterceptResp{}, nil
	}
	body := pxb.EncodeInterceptReq(req)
	f, err := p.rpc(ctx, pxb.TypeIntercept, body, pxb.TypeInterceptResponse)
	if err != nil {
		return pxb.InterceptResp{}, err
	}
	return pxb.DecodeInterceptResp(f.Body)
}

// Emit sends a fire-and-forget event when subscribed.
func (p *Proc) Emit(ev pxb.EventNotify) {
	if p == nil || p.closed.Load() {
		return
	}
	if _, ok := p.events[ev.Event]; !ok {
		return
	}
	body := pxb.EncodeEventNotify(ev)
	if err := p.write(context.Background(), pxb.TypeEvent, 0, 0, body); err != nil {
		debuglog.Logf("extension %q: emit: %v", p.Manifest.Name, err)
	}
}

// PushSessionMeta sends cwd/session identity to the child.
func (p *Proc) PushSessionMeta(sessionID, cwd string) {
	if p == nil || p.closed.Load() {
		return
	}
	body := pxb.EncodeSessionMeta(pxb.SessionMeta{SessionID: sessionID, Cwd: cwd})
	if err := p.write(context.Background(), pxb.TypeSessionMeta, 0, 0, body); err != nil {
		debuglog.Logf("extension %q: session meta: %v", p.Manifest.Name, err)
	}
}

// ReplyHost sends a HostResult for a prior HostRequest.
func (p *Proc) ReplyHost(id uint32, res pxb.HostResult) {
	if p == nil || p.closed.Load() {
		return
	}
	if err := p.write(
		context.Background(),
		pxb.TypeHostResult,
		pxb.FlagHasID,
		id,
		pxb.EncodeHostResult(res),
	); err != nil {
		debuglog.Logf("extension %q: host result: %v", p.Manifest.Name, err)
	}
}

// PushEvent sends a lifecycle event regardless of Subscribe (pane actions, …).
func (p *Proc) PushEvent(ev pxb.EventNotify) {
	if p == nil || p.closed.Load() {
		return
	}
	body := pxb.EncodeEventNotify(ev)
	if err := p.write(context.Background(), pxb.TypeEvent, 0, 0, body); err != nil {
		debuglog.Logf("extension %q: push event: %v", p.Manifest.Name, err)
	}
}

// WantsIntercept reports subscription.
func (p *Proc) WantsIntercept(code uint16) bool {
	_, ok := p.intercept[code]
	return ok
}

// watch covers synchronous writes as well as response waits. Cancellation kills
// and reaps the entire extension: older Go/Rust peers cannot cancel an in-flight
// operation. All concurrent calls fail and the extension must be restarted.
func (p *Proc) watch(ctx context.Context) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		p.terminate()
		close(done)
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}

func (p *Proc) write(ctx context.Context, typ, flags uint16, id uint32, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.closed.Load() {
		return errors.New("extension: process closed")
	}
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	stop := p.watch(ctx)
	defer stop()
	if err := p.wr.Write(typ, flags, id, body); err != nil {
		p.terminate()
		return err
	}
	return ctx.Err()
}

// kill closes pipes independently of the writer lock, releasing blocked sends.
func (p *Proc) kill() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
}

func (p *Proc) terminate() {
	p.closed.Store(true)
	p.kill()
	p.finish(false)
}

func (p *Proc) finish(graceful bool) {
	p.stopOnce.Do(func() {
		p.closed.Store(true)
		p.failPending(errors.New("extension: process closed"))
		if graceful {
			// Start the kill budget before writing: the child may no longer read.
			timer := time.AfterFunc(shutdownWait, p.kill)
			defer timer.Stop()
			_ = p.wr.Write(pxb.TypeShutdown, 0, 0, nil)
		}
		if p.cmd != nil {
			_ = p.cmd.Wait()
		}
		p.kill()
		if p.logFile != nil {
			_ = p.logFile.Close()
		}
	})
}

// Close asks the child to shut down within a bounded budget and reaps it.
func (p *Proc) Close() error {
	if p != nil {
		p.finish(true)
	}
	return nil
}

// Tools returns tools registered during handshake (copy).
func (p *Proc) Tools() []pxb.RegisterTool {
	if p == nil {
		return nil
	}
	out := make([]pxb.RegisterTool, len(p.tools))
	copy(out, p.tools)
	return out
}

// Commands returns slash commands registered during handshake (copy).
func (p *Proc) Commands() []pxb.RegisterCommand {
	if p == nil {
		return nil
	}
	out := make([]pxb.RegisterCommand, len(p.cmds))
	copy(out, p.cmds)
	return out
}
