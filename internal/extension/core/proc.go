package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/extension/loader"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/debuglog"
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
	Manifest loader.Manifest
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
	models    []pxb.RegisterModel
	events    map[uint16]struct{}
	intercept map[uint16]struct{}

	mu       sync.Mutex
	nextID   atomic.Uint32
	pending  map[uint32]chan frameResult
	streams  map[uint32]*modelStream
	closed   atomic.Bool
	stopOnce sync.Once

	// sink is read by readLoop (spontaneous frames) and by RPC callers
	// (CommandResponse notify) while the host may re-wire it — a session switch
	// or a hot swap rebinds it on a live process, so it needs a real barrier.
	sink atomic.Pointer[HostSink]
}

// HostSink receives frames the extension sends on its own initiative instead of
// as an RPC reply: notifications and host requests (confirm, send_user_message).
// Without a sink those frames are dropped.
type HostSink interface {
	Notify(pxb.NotifyMsg)
	HostRequest(id uint32, hasID bool, req pxb.HostRequest)
}

// SetHostSink installs the sink for spontaneous frames: notifications and host
// requests (confirm, send_user_message). Without a sink those frames are dropped.
func (p *Proc) SetHostSink(sink HostSink) {
	if p == nil {
		return
	}
	p.sink.Store(&sink)
}

// hostSink returns the installed sink, if any.
func (p *Proc) hostSink() (HostSink, bool) {
	if p == nil {
		return nil, false
	}
	sink := p.sink.Load()
	if sink == nil || *sink == nil {
		return nil, false
	}
	return *sink, true
}

type frameResult struct {
	frame pxb.Frame
	err   error
}

// streamFrame is one ModelStreamEvent delivery or a stream-fatal error.
type streamFrame struct {
	ev  pxb.ModelStreamEvent
	err error
}

// modelStream is one in-flight ModelInvoke stream. The frame channel is never
// closed — termination is signaled via done — so a sender that fetched the
// stream before an abandon can never send on (or close) a closed channel.
type modelStream struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	ch     chan streamFrame
}

func newModelStream() *modelStream {
	return &modelStream{
		done: make(chan struct{}),
		ch:   make(chan streamFrame, streamBuffer),
	}
}

// push delivers a frame unless the stream was abandoned; it never blocks on a
// closed target. After done is signaled, queued-but-unread frames are dropped
// for late senders and drained by the consumer.
func (s *modelStream) push(fr streamFrame) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	select {
	case s.ch <- fr:
	case <-s.done:
	}
}

// close ends the stream once. It never blocks on a slow consumer.
func (s *modelStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.done)
}

// StartProc launches manifest.Exec and completes the PXB handshake.
func StartProc(ctx context.Context, m loader.Manifest, dir, logDir, cwd, sessionID string) (*Proc, error) {
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
		streams:   make(map[uint32]*modelStream),
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
	// Extensions advertising CapLargeFrames accept ModelInvoke frames past the
	// 16 MiB default — full conversation histories get big. Older extensions
	// keep the default cap and fail exactly as before.
	if hello.Caps&pxb.CapLargeFrames != 0 {
		p.wr.SetMaxPayload(pxb.MaxStreamPayload)
	}

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
		case pxb.TypeRegisterModel:
			rm, err := pxb.DecodeRegisterModel(body)
			if err != nil {
				return err
			}
			if rm.Name == "" {
				return fmt.Errorf("extension %q: register model with empty name", p.Manifest.Name)
			}
			if rm.Conn != pxb.ConnDirect && rm.Conn != pxb.ConnExt {
				return fmt.Errorf(
					"extension %q: model %q: unknown conn %q (want %q or %q)",
					p.Manifest.Name, rm.Name, rm.Conn, pxb.ConnDirect, pxb.ConnExt,
				)
			}
			p.models = append(p.models, rm)
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
		case pxb.TypeModelStream:
			p.deliverStream(f)
		case pxb.TypeNotify:
			n, err := pxb.DecodeNotify(f.Body)
			if err != nil {
				debuglog.Logf("extension %q: notify decode: %v", p.Manifest.Name, err)
				continue
			}
			if sink, ok := p.hostSink(); ok {
				sink.Notify(n)
			}
		case pxb.TypeHostRequest:
			req, err := pxb.DecodeHostRequest(f.Body)
			if err != nil {
				debuglog.Logf("extension %q: host request decode: %v", p.Manifest.Name, err)
				continue
			}
			if sink, ok := p.hostSink(); ok {
				sink.HostRequest(f.ID, f.Flags&pxb.FlagHasID != 0, req)
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

// failStreams ends every in-flight model stream on process death. Streams are
// detached from the map under the lock; close happens outside it, so a slow
// or stuck consumer cannot wedge the teardown. Consumers report the failure
// themselves from drain: done with no terminal frame + a closed proc.
func (p *Proc) failStreams() {
	p.mu.Lock()
	detached := make([]*modelStream, 0, len(p.streams))
	for id, s := range p.streams {
		detached = append(detached, s)
		delete(p.streams, id)
	}
	p.mu.Unlock()
	for _, s := range detached {
		s.close()
	}
}

// deliverStream routes one ModelStreamEvent to its waiting consumer. The
// invoke frame ID is the correlation key; done/error end the stream.
func (p *Proc) deliverStream(f pxb.Frame) {
	if f.Flags&pxb.FlagHasID == 0 {
		debuglog.Logf("extension %q: model stream frame without id", p.Manifest.Name)
		return
	}
	p.mu.Lock()
	s, ok := p.streams[f.ID]
	if !ok {
		p.mu.Unlock()
		// Abandoned stream (cancel or consumer stop): drop quietly — the
		// extension is allowed to keep producing until it sees the cancel.
		return
	}
	ev, err := pxb.DecodeModelStreamEvent(f.Body)
	if err != nil || ev.Type == "done" || ev.Type == "error" {
		delete(p.streams, f.ID)
	}
	p.mu.Unlock()
	if err != nil {
		s.push(streamFrame{err: fmt.Errorf("extension %q: model stream event: %w", p.Manifest.Name, err)})
		s.close()
		return
	}
	s.push(streamFrame{ev: ev})
	if ev.Type == "done" || ev.Type == "error" {
		s.close()
	}
}

// deregStream removes a stream registration (idempotent).
func (p *Proc) deregStream(id uint32) {
	p.mu.Lock()
	s, ok := p.streams[id]
	if ok {
		delete(p.streams, id)
	}
	p.mu.Unlock()
	if ok {
		s.close()
	}
}

// cancelModel abandons a stream and asks the extension to stop. Cancel is
// best-effort: an extension that never reads it runs to completion and its
// late frames are dropped, so cancellation must never kill the process.
func (p *Proc) cancelModel(id uint32) {
	p.deregStream(id)
	if err := p.write(context.Background(), pxb.TypeModelCancel, pxb.FlagHasID, id, nil); err != nil {
		debuglog.Logf("extension %q: model cancel: %v", p.Manifest.Name, err)
	}
}

// StreamModel invokes an ext-served model (Conn "ext") and yields the
// ModelStreamEvent frames as they arrive, correlated by the invoke frame ID.
// Unlike RPC waits this never uses watch(): canceling the context abandons
// the stream and sends a best-effort ModelCancel instead of terminating the
// extension, which may be mid-flight for an unrelated request.
func (p *Proc) StreamModel(
	ctx context.Context,
	req pxb.ModelInvoke,
) iter.Seq2[pxb.ModelStreamEvent, error] {
	return func(yield func(pxb.ModelStreamEvent, error) bool) {
		if p == nil || p.closed.Load() {
			yield(pxb.ModelStreamEvent{}, errors.New("extension: process closed"))
			return
		}
		id := p.nextID.Add(1)
		s := newModelStream()
		p.mu.Lock()
		if p.closed.Load() {
			p.mu.Unlock()
			yield(pxb.ModelStreamEvent{}, errors.New("extension: process closed"))
			return
		}
		p.streams[id] = s
		p.mu.Unlock()

		// The invoke write is ctx-aware: a wedged extension must not turn an
		// Esc into an unkillable turn. The writer serializes concurrent writes,
		// so this races other RPC frames safely.
		body := pxb.EncodeModelInvoke(req)
		writeDone := make(chan error, 1)
		go func() { writeDone <- p.wr.Write(pxb.TypeModelInvoke, pxb.FlagHasID, id, body) }()
		select {
		case <-ctx.Done():
			p.cancelModel(id)
			return
		case err := <-writeDone:
			if err != nil {
				p.deregStream(id)
				p.terminate()
				yield(pxb.ModelStreamEvent{}, err)
				return
			}
		}

		for {
			select {
			case <-ctx.Done():
				p.cancelModel(id)
				return
			case fr := <-s.ch:
				if fr.err != nil {
					yield(pxb.ModelStreamEvent{}, fr.err)
					return
				}
				if !yield(fr.ev, nil) {
					p.cancelModel(id)
					return
				}
			case <-s.done:
				// The stream was closed (terminal frame, abandon, or process
				// death). Drain whatever already landed in the buffer — done
				// and the last frame race through separate channels.
				for {
					select {
					case fr := <-s.ch:
						if fr.err != nil {
							yield(pxb.ModelStreamEvent{}, fr.err)
							return
						}
						if !yield(fr.ev, nil) {
							p.cancelModel(id)
							return
						}
					default:
						if p.closed.Load() {
							yield(pxb.ModelStreamEvent{}, errors.New("extension: process closed"))
						}
						return
					}
				}
			}
		}
	}
}

// streamBuffer decouples the frame pump from the consumer so a slow turn
// render cannot stall every RPC on this extension.
const streamBuffer = 64

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
	if resp.Notify != "" {
		if sink, ok := p.hostSink(); ok {
			sink.Notify(pxb.NotifyMsg{Level: "info", Message: resp.Notify})
		}
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
		p.failStreams()
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

// Closed reports whether the subprocess is gone — shut down by the host or
// exited on its own (readLoop error, crash). A dead process keeps its
// registration data but can no longer serve RPCs, so callers use this to avoid
// advertising a healthy extension that is actually gone.
func (p *Proc) Closed() bool {
	return p == nil || p.closed.Load()
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

// Models returns models registered during handshake (copy).
func (p *Proc) Models() []pxb.RegisterModel {
	if p == nil {
		return nil
	}
	out := make([]pxb.RegisterModel, len(p.models))
	copy(out, p.models)
	return out
}

// HasCommand reports whether the extension registered a command with this name.
func (p *Proc) HasCommand(name string) bool {
	if p == nil {
		return false
	}
	for _, c := range p.cmds {
		if c.Name == name {
			return true
		}
	}
	return false
}

// BuildAPI installs shim handlers onto api from this process's registrations.
func (p *Proc) BuildAPI(api *ext.API) {
	if p == nil || api == nil {
		return
	}
	for _, t := range p.tools {
		name := t.Name
		var params map[string]any
		if len(t.SchemaJSON) > 0 {
			_ = json.Unmarshal(t.SchemaJSON, &params)
		}
		var detailFn func(json.RawMessage) string
		if t.HasDetail {
			detailFn = func(args json.RawMessage) string {
				d, err := p.CallToolDetail(context.Background(), name, args)
				if err != nil {
					return ""
				}
				return d
			}
		}
		api.RegisterTool(ext.Tool{
			Name:           name,
			Description:    t.Description,
			Parameters:     params,
			DetailFromArgs: detailFn,
			Readable:       t.Readable,
			Execute: func(ctx context.Context, args json.RawMessage) (ext.ToolResult, error) {
				return p.CallTool(ctx, name, args)
			},
		})
	}
	for _, c := range p.cmds {
		name := c.Name
		api.RegisterCommand(name, ext.Command{
			Description: c.Description,
			NeedsArgs:   c.NeedsArgs,
			Handler: func(args string, _ *ext.Context) error {
				resp, err := p.CallCommand(context.Background(), name, args)
				if err != nil {
					return err
				}
				if !resp.OK {
					if resp.Error != "" {
						return errors.New(resp.Error)
					}
					return fmt.Errorf("extension command %q failed", name)
				}
				return nil
			},
		})
	}
	p.registerInterceptShims(api)
	p.registerEventShims(api)
}

// registerInterceptShims forwards subscribed events through the child's intercept hook.
func (p *Proc) registerInterceptShims(api *ext.API) {
	on := func(code uint16, event string, handler any) {
		if p.WantsIntercept(code) {
			api.On(event, handler)
		}
	}
	on(pxb.EvToolCall, ext.EventToolCall, func(ev ext.ToolCallEvent, _ *ext.Context) *ext.ToolCallResult {
		resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
			Event: pxb.EvToolCall, ToolName: ev.ToolName, ToolCallID: ev.ToolCallID, Input: ev.Input,
		})
		if err != nil {
			debuglog.Logf("extension %q: tool_call intercept: %v", p.Manifest.Name, err)
			return nil
		}
		return &ext.ToolCallResult{Block: resp.Block, Reason: resp.Reason, Input: resp.Input, Context: resp.Context}
	})
	on(pxb.EvToolResult, ext.EventToolResult, func(ev ext.ToolResultEvent, _ *ext.Context) *ext.ToolResultResult {
		resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
			Event: pxb.EvToolResult, ToolName: ev.ToolName, ToolCallID: ev.ToolCallID,
			Input: ev.Input, Content: ev.Content, IsError: ev.IsError, ErrText: ev.Err,
		})
		if err != nil {
			debuglog.Logf("extension %q: tool_result intercept: %v", p.Manifest.Name, err)
			return nil
		}
		return &ext.ToolResultResult{
			Content: resp.Content,
			Context: resp.Context,
			Stop:    resp.Stop,
			Reason:  resp.Reason,
		}
	})
	on(pxb.EvSessionBeforeSwitch, ext.EventSessionBeforeSwitch,
		func(ev ext.SessionBeforeSwitchEvent, _ *ext.Context) *ext.SessionBeforeSwitchResult {
			resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
				Event: pxb.EvSessionBeforeSwitch, Reason: ev.Reason, TargetID: ev.TargetSessionID,
			})
			if err != nil {
				return nil
			}
			return &ext.SessionBeforeSwitchResult{Cancel: resp.Cancel, Reason: resp.Reason, Toast: resp.Toast}
		})
	on(pxb.EvBeforeAgentStart, ext.EventBeforeAgentStart,
		func(ev ext.BeforeAgentStartEvent, _ *ext.Context) *ext.BeforeAgentStartResult {
			resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
				Event: pxb.EvBeforeAgentStart, Prompt: ev.Prompt,
			})
			if err != nil {
				return nil
			}
			return &ext.BeforeAgentStartResult{
				Prompt:             resp.Prompt,
				SystemPromptAppend: resp.SystemPromptAppend,
			}
		})
	on(pxb.EvUserInput, ext.EventUserInput, func(ev ext.UserInputEvent, _ *ext.Context) *ext.UserInputResult {
		resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
			Event: pxb.EvUserInput, Prompt: ev.Text,
		})
		if err != nil {
			return nil
		}
		return &ext.UserInputResult{Handled: resp.Handled, Text: resp.Prompt, Reason: resp.Reason}
	})
	on(
		pxb.EvTurnStopping,
		ext.EventTurnStopping,
		func(ev ext.TurnStoppingEvent, _ *ext.Context) *ext.TurnStoppingResult {
			resp, err := p.Intercept(context.Background(), pxb.InterceptReq{
				Event: pxb.EvTurnStopping, TurnIndex: uint32(ev.TurnIndex), //nolint:gosec // G115
			})
			if err != nil {
				return nil
			}
			return &ext.TurnStoppingResult{Continue: resp.Continue, Message: resp.Prompt, Reason: resp.Reason}
		},
	)
}

// registerEventShims forwards fire-and-forget lifecycle events to the child.
func (p *Proc) registerEventShims(api *ext.API) {
	on := func(code uint16, event string, handler any) {
		if _, ok := p.events[code]; ok {
			api.On(event, handler)
		}
	}
	on(pxb.EvSessionStart, ext.EventSessionStart, func(ev ext.SessionStartEvent, _ *ext.Context) {
		p.Emit(pxb.EventNotify{Event: pxb.EvSessionStart, Reason: ev.Reason, PreviousSessionID: ev.PreviousSessionID})
	})
	on(pxb.EvSessionShutdown, ext.EventSessionShutdown, func(ev ext.SessionShutdownEvent, _ *ext.Context) {
		p.Emit(pxb.EventNotify{
			Event:           pxb.EvSessionShutdown,
			Reason:          ev.Reason,
			TargetSessionID: ev.TargetSessionID,
		})
	})
	on(pxb.EvSessionCompact, ext.EventSessionCompact, func(ev ext.SessionCompactEvent, _ *ext.Context) {
		p.Emit(pxb.EventNotify{Event: pxb.EvSessionCompact, Reason: ev.Reason})
	})
	on(pxb.EvAgentStart, ext.EventAgentStart, func(ext.AgentStartEvent, *ext.Context) {
		p.Emit(pxb.EventNotify{Event: pxb.EvAgentStart})
	})
	on(pxb.EvAgentEnd, ext.EventAgentEnd, func(ext.AgentEndEvent, *ext.Context) {
		p.Emit(pxb.EventNotify{Event: pxb.EvAgentEnd})
	})
	on(pxb.EvTurnStart, ext.EventTurnStart, func(ev ext.TurnStartEvent, _ *ext.Context) {
		//nolint:gosec // G115: turn index is a small session counter
		p.Emit(pxb.EventNotify{Event: pxb.EvTurnStart, TurnIndex: uint32(ev.TurnIndex)})
	})
	on(pxb.EvTurnEnd, ext.EventTurnEnd, func(ev ext.TurnEndEvent, _ *ext.Context) {
		//nolint:gosec // G115: turn index is a small session counter
		p.Emit(pxb.EventNotify{Event: pxb.EvTurnEnd, TurnIndex: uint32(ev.TurnIndex)})
	})
	on(pxb.EvToolExecStart, ext.EventToolExecutionStart, func(ev ext.ToolExecutionStartEvent, _ *ext.Context) {
		p.Emit(pxb.EventNotify{
			Event:      pxb.EvToolExecStart,
			ToolName:   ev.ToolName,
			ToolCallID: ev.ToolCallID,
			Input:      ev.Args,
		})
	})
	on(pxb.EvToolExecEnd, ext.EventToolExecutionEnd, func(ev ext.ToolExecutionEndEvent, _ *ext.Context) {
		p.Emit(pxb.EventNotify{
			Event:      pxb.EvToolExecEnd,
			ToolName:   ev.ToolName,
			ToolCallID: ev.ToolCallID,
			IsError:    ev.IsError,
		})
	})
}
