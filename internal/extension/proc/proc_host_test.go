package proc

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
)

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
	p.onHostRequest = func(id uint32, hasID bool, req pxb.HostRequest) {
		if hasID && req.Method == "confirm" {
			confirmed <- id
		}
		p.ReplyHost(id, pxb.HostResult{OK: true})
	}
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
	p.onNotify = func(pxb.NotifyMsg) { close(ready) }
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
