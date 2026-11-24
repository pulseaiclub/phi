package core

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/extension/loader"
)

// testSink adapts optional function fields to the HostSink the host installs.
type testSink struct {
	notify  func(pxb.NotifyMsg)
	hostReq func(id uint32, hasID bool, req pxb.HostRequest)
}

func (s testSink) Notify(n pxb.NotifyMsg) {
	if s.notify != nil {
		s.notify(n)
	}
}

func (s testSink) HostRequest(id uint32, hasID bool, req pxb.HostRequest) {
	if s.hostReq != nil {
		s.hostReq(id, hasID, req)
	}
}

func TestHostFirstCommandConfirmCollision(t *testing.T) {
	input, childInput := io.Pipe()
	childOutput, output := io.Pipe()
	defer input.Close()
	defer childInput.Close()
	defer childOutput.Close()
	defer output.Close()
	p := &Proc{
		stdin:   childInput,
		stdout:  childOutput,
		wr:      pxb.NewWriter(childInput),
		rd:      pxb.NewReader(childOutput),
		pending: make(map[uint32]chan frameResult),
	}
	confirmed := make(chan uint32, 1)
	p.SetHostSink(testSink{hostReq: func(id uint32, hasID bool, req pxb.HostRequest) {
		if hasID && req.Method == "confirm" {
			confirmed <- id
		}
		p.ReplyHost(id, pxb.HostResult{OK: true})
	}})
	go p.readLoop()
	childDone := make(chan error, 1)
	go func() {
		rd, wr := pxb.NewReader(input), pxb.NewWriter(output)
		cmd, err := rd.Read()
		if err != nil {
			childDone <- err
			return
		}
		if cmd.Type != pxb.TypeCommandInvoked || cmd.ID != 1 {
			childDone <- io.ErrUnexpectedEOF
			return
		}
		if err = wr.Write(
			pxb.TypeHostRequest,
			pxb.FlagHasID,
			1,
			pxb.EncodeHostRequest(pxb.HostRequest{Method: "confirm", Arg: "Run?"}),
		); err != nil {
			childDone <- err
			return
		}
		reply, err := rd.Read()
		if err != nil {
			childDone <- err
			return
		}
		if reply.Type != pxb.TypeHostResult {
			childDone <- io.ErrUnexpectedEOF
			return
		}
		confirmation, err := pxb.DecodeHostResult(pxb.CloneBody(reply))
		if err != nil || !confirmation.OK || reply.ID != 1 {
			childDone <- io.ErrUnexpectedEOF
			return
		}
		childDone <- wr.Write(pxb.TypeCommandResponse, pxb.FlagHasID, cmd.ID, pxb.EncodeCommandResponse(pxb.CommandResponse{OK: confirmation.OK}))
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	res, err := p.CallCommand(ctx, "first", "")
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, uint32(1), <-confirmed)
	require.NoError(t, <-childDone)
}

func TestHostProcessHelper(_ *testing.T) {
	mode := os.Getenv("PHI_HOST_TEST_CHILD")
	if mode == "" {
		return
	}
	wr, rd := pxb.NewWriter(os.Stdout), pxb.NewReader(os.Stdin)
	if mode == "eof" {
		_ = os.Stdout.Close()
	} else {
		if mode == "running" {
			_, _ = rd.Read()
		}
		_ = wr.Write(pxb.TypeNotify, 0, 0, pxb.EncodeNotify(pxb.NotifyMsg{Message: "ready"}))
	}
	for {
		time.Sleep(time.Hour)
	}
}

func hostTestProc(t *testing.T, mode string) (*Proc, <-chan struct{}) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHostProcessHelper$")
	cmd.Env = append(os.Environ(), "PHI_HOST_TEST_CHILD="+mode)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	p := &Proc{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		wr:      pxb.NewWriter(stdin),
		rd:      pxb.NewReader(stdout),
		pending: make(map[uint32]chan frameResult),
	}
	ready := make(chan struct{})
	p.SetHostSink(testSink{notify: func(pxb.NotifyMsg) { close(ready) }})
	go p.readLoop()
	t.Cleanup(func() { p.terminate() })
	return p, ready
}

func hostAwait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "host lifecycle did not complete")
	}
}

func TestHostBackpressureCancellation(t *testing.T) {
	for _, mode := range []string{"blocked", "running"} {
		t.Run(mode, func(t *testing.T) {
			p, ready := hostTestProc(t, mode)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			var callErr error
			go func() {
				defer close(done)
				args := "{}"
				if mode == "blocked" {
					args = strings.Repeat("x", 4<<20)
				}
				_, callErr = p.CallTool(ctx, "work", []byte(args))
			}()
			hostAwait(t, ready)
			if mode == "blocked" {
				time.Sleep(50 * time.Millisecond)
			}
			cancel()
			hostAwait(t, done)
			require.ErrorIs(t, callErr, context.Canceled)
			require.NotNil(t, p.cmd.ProcessState)
			_, err := p.CallTool(t.Context(), "again", nil)
			require.ErrorContains(t, err, "closed")
		})
	}
}

func TestHostBackpressureTimeout(t *testing.T) {
	for _, mode := range []string{"blocked", "running"} {
		t.Run(mode, func(t *testing.T) {
			p, ready := hostTestProc(t, mode)
			body := pxb.EncodeToolInvoke(pxb.ToolInvoke{Name: "work"})
			if mode == "blocked" {
				hostAwait(t, ready)
				body = []byte(strings.Repeat("x", 4<<20))
			}
			_, err := p.rpcWait(t.Context(), pxb.TypeToolInvoke, body, pxb.TypeToolResult, 200*time.Millisecond)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			hostAwait(t, ready)
			require.NotNil(t, p.cmd.ProcessState)
		})
	}
}

func TestHostBackpressureClose(t *testing.T) {
	p, ready := hostTestProc(t, "blocked")
	hostAwait(t, ready)
	sent := make(chan struct{})
	go func() { p.PushSessionMeta("session", strings.Repeat("x", 4<<20)); close(sent) }()
	time.Sleep(50 * time.Millisecond)
	done := make(chan struct{})
	go func() { _ = p.Close(); close(done) }()
	hostAwait(t, done)
	hostAwait(t, sent)
	require.NotNil(t, p.cmd.ProcessState)
	require.NoError(t, p.Close())
}

func TestHostCanceledBeforeDispatch(t *testing.T) {
	p, ready := hostTestProc(t, "blocked")
	hostAwait(t, ready)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := p.CallTool(ctx, "work", nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, p.nextID.Load())
	require.False(t, p.closed.Load())
}

func TestHostEOFFailsSubsequentRPC(t *testing.T) {
	p, _ := hostTestProc(t, "eof")
	require.Eventually(t, p.closed.Load, 5*time.Second, time.Millisecond)
	p.terminate()
	require.NotNil(t, p.cmd.ProcessState)
	_, err := p.CallTool(t.Context(), "work", nil)
	require.ErrorContains(t, err, "closed")
}

func testProc() *Proc {
	return &Proc{
		Manifest: loader.Manifest{Name: "x"},
		streams:  make(map[uint32]*modelStream),
	}
}

// collectStream reads frames until an error frame or a short deadline.
func collectStream(s *modelStream) []streamFrame {
	var out []streamFrame
	deadline := time.After(time.Second)
	for {
		select {
		case fr := <-s.ch:
			out = append(out, fr)
			if fr.err != nil || fr.ev.Type == "done" || fr.ev.Type == "error" {
				return out
			}
		case <-deadline:
			return out
		}
	}
}

// A malformed ModelStreamEvent must end the stream with an error frame and
// leave no registration behind: late frames for the same id are dropped
// quietly instead of panicking the read loop.
func TestDeliverStreamCorruptFrameEndsStream(t *testing.T) {
	p := testProc()
	s := newModelStream()
	p.streams[1] = s

	p.deliverStream(pxb.Frame{
		Header: pxb.Header{Flags: pxb.FlagHasID, ID: 1},
		Body:   []byte{0xff, 0x00, 0x00},
	})
	frames := collectStream(s)
	require.Len(t, frames, 1)
	require.Error(t, frames[0].err)
	assert.Contains(t, frames[0].err.Error(), "model stream event")

	_, stillRegistered := p.streams[1]
	assert.False(t, stillRegistered, "failed stream must be deregistered")

	valid := pxb.EncodeModelStreamEvent(pxb.ModelStreamEvent{Type: "delta", Content: "late"})
	assert.NotPanics(t, func() {
		p.deliverStream(pxb.Frame{Header: pxb.Header{Flags: pxb.FlagHasID, ID: 1}, Body: valid})
	})
}

// Abandoning a stream whose buffer is full must release the blocked sender
// via done instead of closing a channel under it.
func TestModelStreamAbandonWithFullBuffer(t *testing.T) {
	s := newModelStream()
	for range streamBuffer {
		s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "delta", Content: "x"}})
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		close(started)
		s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "delta", Content: "overflow"}})
		close(finished)
	}()
	<-started // the push is parked on the full buffer (or about to park)

	done := make(chan struct{})
	go func() {
		s.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close blocked behind a full buffer")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("parked push never released after close")
	}

	// Post-close pushes are dropped, never panic.
	assert.NotPanics(t, func() {
		s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "delta", Content: "late"}})
		s.close()
	})
}

// done and the terminal frame travel on separate channels, so a consumer that
// sees done first must still find the terminal frame in the buffer.
func TestModelStreamDoneFrameSurvivesClose(t *testing.T) {
	s := newModelStream()
	s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "done"}})
	s.close()

	frames := collectStream(s)
	require.Len(t, frames, 1)
	assert.Equal(t, "done", frames[0].ev.Type)
}

// failStreams must not deadlock when a consumer abandoned the stream and the
// buffer is full: detach under the lock, then close without pushing.
func TestFailStreamsDetachesBeforeClose(t *testing.T) {
	p := testProc()
	s := newModelStream()
	p.streams[7] = s
	for range streamBuffer {
		s.push(streamFrame{ev: pxb.ModelStreamEvent{Type: "delta"}})
	}

	done := make(chan struct{})
	go func() {
		p.failStreams()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failStreams deadlocked on a full buffer")
	}
	assert.Empty(t, p.streams)
}

func TestRPCWaitDurationDefault(t *testing.T) {
	d := rpcWaitDuration(t.Context(), 0)
	assert.Equal(t, rpcTimeout, d)
}

func TestRPCWaitDurationOverride(t *testing.T) {
	d := rpcWaitDuration(t.Context(), 120*time.Second)
	assert.Equal(t, 120*time.Second, d)
}

func TestRPCWaitDurationClampsMax(t *testing.T) {
	d := rpcWaitDuration(t.Context(), 10_000*time.Second)
	assert.Equal(t, rpcTimeoutMax, d)
}

func TestRPCWaitDurationOverrideCappedByCtx(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	d := rpcWaitDuration(ctx, 120*time.Second)
	assert.Less(t, d, 45*time.Second)
	assert.Greater(t, d, 30*time.Second)
}

func TestRPCWaitDurationLegacyCtxReplacesDefault(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	d := rpcWaitDuration(ctx, 0)
	assert.Greater(t, d, 80*time.Second)
	assert.LessOrEqual(t, d, 90*time.Second)
}

func TestToolRPCTimeoutLookup(t *testing.T) {
	p := &Proc{tools: []pxb.RegisterTool{
		{Name: "fetch", TimeoutSec: 180},
		{Name: "ping"},
	}}
	assert.Equal(t, 180*time.Second, p.toolRPCTimeout("fetch"))
	assert.Equal(t, time.Duration(0), p.toolRPCTimeout("ping"))
	assert.Equal(t, time.Duration(0), p.toolRPCTimeout("missing"))
}
