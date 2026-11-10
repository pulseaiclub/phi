package phi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

func TestOversizeResponseFallbacks(t *testing.T) {
	large := make([]byte, pxb.MaxPayload+1)
	for _, typ := range []uint16{pxb.TypeCommandResponse, pxb.TypeToolDetailResult, pxb.TypeInterceptResponse} {
		var out bytes.Buffer
		e := New("test", "1")
		e.wr = pxb.NewWriter(&out)
		e.write(typ, pxb.FlagHasID, 99, large)
		require.NoError(t, e.writeErr)
		f, err := pxb.ReadFrame(&out)
		require.NoError(t, err)
		assert.Equal(t, typ, f.Type)
		assert.Equal(t, uint32(99), f.ID)
		switch typ {
		case pxb.TypeCommandResponse:
			r, err := pxb.DecodeCommandResponse(f.Body)
			require.NoError(t, err)
			assert.False(t, r.OK)
			assert.Equal(t, oversizeResponse, r.Error)
		case pxb.TypeToolDetailResult:
			r, err := pxb.DecodeToolDetailResult(f.Body)
			require.NoError(t, err)
			assert.Equal(t, oversizeResponse, r.Detail)
		case pxb.TypeInterceptResponse:
			r, err := pxb.DecodeInterceptResp(f.Body)
			require.NoError(t, err)
			assert.True(t, r.Block)
			assert.True(t, r.Cancel)
			assert.True(t, r.Stop)
			assert.Equal(t, oversizeResponse, r.Reason)
		}
	}
}

func TestConfirmByteLimit(t *testing.T) {
	e := New("test", "1")
	e.RegisterCommand("confirm", ext.Command{Handler: func(string, *ext.Context) error {
		e.Confirm("", "")
		assert.Fail(t, "handler resumed after queue overflow")
		return nil
	}})
	var in, out bytes.Buffer
	handshake(t, &in)
	inputFrame(t, &in, pxb.TypeCommandInvoked, 10, pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: "confirm"}))
	body := make([]byte, maxDeferredBytes/2+1)
	inputFrame(t, &in, pxb.TypeToolInvoke, 11, body)
	inputFrame(t, &in, pxb.TypeToolInvoke, 12, body)
	require.ErrorContains(t, e.run(&in, &out), "queue is full")
	assert.Empty(t, e.deferred)
}

func inputFrame(t *testing.T, b *bytes.Buffer, typ uint16, id uint32, body []byte) {
	t.Helper()
	require.NoError(t, pxb.WriteFrame(b, typ, pxb.FlagHasID, id, body))
}

func handshake(t *testing.T, b *bytes.Buffer) {
	inputFrame(t, b, pxb.TypeHelloAck, 0, pxb.EncodeHelloAck(pxb.HelloAck{}))
}

func outputFrames(t *testing.T, b *bytes.Buffer) []pxb.Frame {
	t.Helper()
	var frames []pxb.Frame
	for b.Len() > 0 {
		f, err := pxb.ReadFrame(b)
		require.NoError(t, err)
		frames = append(frames, f)
	}
	return frames
}

func TestConfirmReplaysRequestsSerially(t *testing.T) {
	e := New("test", "1")
	var calls []string
	e.RegisterCommand("confirm", ext.Command{Handler: func(_ string, _ *ext.Context) error {
		calls = append(calls, "confirm-start")
		require.True(t, e.Confirm("title", "message"))
		calls = append(calls, "confirm-end")
		return nil
	}})
	e.RegisterCommand(
		"queued",
		ext.Command{Handler: func(args string, _ *ext.Context) error { calls = append(calls, args); return nil }},
	)
	e.RegisterTool(
		ext.Tool{Name: "tool", Execute: func(_ context.Context, args json.RawMessage) (ext.ToolResult, error) {
			calls = append(calls, string(args))
			return ext.ToolResult{}, nil
		}, DetailFromArgs: func(args json.RawMessage) string { calls = append(calls, string(args)); return "detail" }},
	)
	e.OnToolCall(func(ext.ToolCallEvent) *ext.ToolCallResult { calls = append(calls, "intercept"); return nil })
	e.SubscribeEvent(ext.EventTurnStart, func(pxb.EventNotify) {
		calls = append(calls, "event")
	})
	var in, out bytes.Buffer
	handshake(t, &in)
	inputFrame(t, &in, pxb.TypeCommandInvoked, 10, pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: "confirm"}))
	inputFrame(
		t,
		&in,
		pxb.TypeToolInvoke,
		11,
		pxb.EncodeToolInvoke(pxb.ToolInvoke{Name: "tool", Args: json.RawMessage(`"tool"`)}),
	)
	inputFrame(
		t,
		&in,
		pxb.TypeToolDetailInvoke,
		12,
		pxb.EncodeToolInvoke(pxb.ToolInvoke{Name: "tool", Args: json.RawMessage(`"detail"`)}),
	)
	inputFrame(
		t,
		&in,
		pxb.TypeCommandInvoked,
		13,
		pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: "queued", Args: "queued"}),
	)
	inputFrame(t, &in, pxb.TypeIntercept, 14, pxb.EncodeInterceptReq(pxb.InterceptReq{Event: pxb.EvToolCall}))
	inputFrame(t, &in, pxb.TypeEvent, 0, pxb.EncodeEventNotify(pxb.EventNotify{Event: pxb.EvTurnStart}))
	inputFrame(t, &in, pxb.TypeHostResult, 1, pxb.EncodeHostResult(pxb.HostResult{OK: true}))
	inputFrame(t, &in, pxb.TypeShutdown, 0, nil)
	require.NoError(t, e.run(&in, &out))
	assert.Equal(
		t,
		[]string{"confirm-start", "confirm-end", `"tool"`, `"detail"`, "queued", "intercept", "event"},
		calls,
	)
	var ids []uint32
	for _, f := range outputFrames(t, &out) {
		if f.ID >= 10 {
			ids = append(ids, f.ID)
		}
	}
	assert.Equal(t, []uint32{10, 11, 12, 13, 14}, ids)
}

func TestConfirmShutdownAndFailureUnwindHandler(t *testing.T) {
	for _, mode := range []string{"shutdown", "eof", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			e := New("test", "1")
			resumed := false
			e.RegisterCommand("confirm", ext.Command{Handler: func(_ string, _ *ext.Context) error {
				e.Confirm("", " ")
				resumed = true
				e.Notify("info", "late")
				return nil
			}})
			var in, out bytes.Buffer
			handshake(t, &in)
			inputFrame(
				t,
				&in,
				pxb.TypeCommandInvoked,
				10,
				pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: "confirm"}),
			)
			if mode == "shutdown" {
				inputFrame(t, &in, pxb.TypeShutdown, 0, nil)
			}
			if mode == "overflow" {
				for range maxDeferredFrames + 1 {
					inputFrame(t, &in, pxb.TypeToolInvoke, 11, pxb.EncodeToolInvoke(pxb.ToolInvoke{Name: "tool"}))
				}
			}
			err := e.run(&in, &out)
			switch mode {
			case "shutdown":
				require.NoError(t, err)
			case "eof":
				require.ErrorIs(t, err, io.EOF)
			case "overflow":
				require.ErrorContains(t, err, "queue is full")
			}
			assert.False(t, resumed)
			frames := outputFrames(t, &out)
			for _, f := range frames {
				assert.NotEqual(t, pxb.TypeCommandResponse, f.Type)
				assert.NotEqual(t, pxb.TypeNotify, f.Type)
			}
			if mode == "shutdown" {
				assert.Equal(t, pxb.TypeShutdownAck, frames[len(frames)-1].Type)
			}
		})
	}
}

func TestOversizeToolResponseKeepsIDAndConnection(t *testing.T) {
	e := New("test", "1")
	e.RegisterTool(ext.Tool{Name: "large", Execute: func(context.Context, json.RawMessage) (ext.ToolResult, error) {
		return ext.ToolResult{Content: strings.Repeat("x", pxb.MaxPayload)}, nil
	}})
	var in, out bytes.Buffer
	handshake(t, &in)
	inputFrame(t, &in, pxb.TypeToolInvoke, 42, pxb.EncodeToolInvoke(pxb.ToolInvoke{Name: "large"}))
	inputFrame(t, &in, pxb.TypeShutdown, 0, nil)
	require.NoError(t, e.run(&in, &out))
	frames := outputFrames(t, &out)
	f := frames[len(frames)-2]
	assert.Equal(t, uint32(42), f.ID)
	assert.Equal(t, pxb.FlagHasID, f.Flags)
	r, err := pxb.DecodeToolResult(f.Body)
	require.NoError(t, err)
	assert.True(t, r.IsError)
	assert.Contains(t, r.Error, "exceeds PXB maximum")
	assert.Equal(t, pxb.TypeShutdownAck, frames[len(frames)-1].Type)
}

type failingWriter struct {
	target uint16
	err    error
}

func (w failingWriter) Write(b []byte) (int, error) {
	if len(b) >= pxb.HeaderSize {
		h, err := pxb.DecodeHeader(b)
		if err == nil && h.Type == w.target {
			return 0, w.err
		}
	}
	return len(b), nil
}

func TestRunPropagatesWriteFailures(t *testing.T) {
	for _, typ := range []uint16{pxb.TypeCommandResponse, pxb.TypeToolResult, pxb.TypeToolDetailResult, pxb.TypeInterceptResponse, pxb.TypeShutdownAck, pxb.TypeHostRequest, pxb.TypeNotify} {
		t.Run(string(rune(typ)), func(t *testing.T) {
			e := New("test", "1")
			e.RegisterCommand("confirm", ext.Command{Handler: func(_ string, _ *ext.Context) error {
				if typ == pxb.TypeNotify {
					e.Notify("info", "msg")
				} else {
					e.Confirm("", "")
				}
				return nil
			}})
			var in bytes.Buffer
			handshake(t, &in)
			switch typ {
			case pxb.TypeCommandResponse:
				inputFrame(
					t,
					&in,
					pxb.TypeCommandInvoked,
					5,
					pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: "missing"}),
				)
			case pxb.TypeToolResult:
				inputFrame(t, &in, pxb.TypeToolInvoke, 5, pxb.EncodeToolInvoke(pxb.ToolInvoke{}))
			case pxb.TypeToolDetailResult:
				inputFrame(t, &in, pxb.TypeToolDetailInvoke, 5, pxb.EncodeToolInvoke(pxb.ToolInvoke{}))
			case pxb.TypeInterceptResponse:
				inputFrame(t, &in, pxb.TypeIntercept, 5, pxb.EncodeInterceptReq(pxb.InterceptReq{}))
			case pxb.TypeShutdownAck:
				inputFrame(t, &in, pxb.TypeShutdown, 0, nil)
			default:
				inputFrame(
					t,
					&in,
					pxb.TypeCommandInvoked,
					5,
					pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: "confirm"}),
				)
			}
			want := errors.New("broken transport")
			require.ErrorIs(t, e.run(&in, failingWriter{target: typ, err: want}), want)
		})
	}
}

func TestSelectCancellationAndInvalidResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  pxb.HostResult
		wantErr bool
	}{
		{"cancel", pxb.HostResult{}, false},
		{"range", pxb.HostResult{OK: true, Body: "3"}, true},
		{"null", pxb.HostResult{OK: true, Body: "null"}, true},
		{"error", pxb.HostResult{Error: "host unavailable"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New("test", "1")
			var in, out bytes.Buffer
			inputFrame(t, &in, pxb.TypeHostResult, 1, pxb.EncodeHostResult(tc.result))
			e.rd, e.wr = pxb.NewReader(&in), pxb.NewWriter(&out)
			reply, err := e.Select(ext.SelectRequest{Items: []ext.SelectItem{{Label: "one"}}})
			assert.False(t, reply.OK)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSelectInsideCommand(t *testing.T) {
	e := New("test", "1")
	e.RegisterCommand("pick", ext.Command{Handler: func(_ string, _ *ext.Context) error {
		reply, err := e.Select(ext.SelectRequest{Items: []ext.SelectItem{{Label: "first"}, {Label: "second"}}})
		require.NoError(t, err)
		assert.Equal(t, ext.SelectReply{Index: 1, OK: true}, reply)
		return nil
	}})
	var in, out bytes.Buffer
	handshake(t, &in)
	inputFrame(t, &in, pxb.TypeCommandInvoked, 10, pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: "pick"}))
	inputFrame(t, &in, pxb.TypeHostResult, 1, pxb.EncodeHostResult(pxb.HostResult{OK: true, Body: "1"}))
	inputFrame(t, &in, pxb.TypeShutdown, 0, nil)
	require.NoError(t, e.run(&in, &out))
	var methods []string
	for _, frame := range outputFrames(t, &out) {
		if frame.Type == pxb.TypeHostRequest {
			req, err := pxb.DecodeHostRequest(frame.Body)
			require.NoError(t, err)
			methods = append(methods, req.Method)
		}
	}
	assert.Equal(t, []string{"select"}, methods)
}
