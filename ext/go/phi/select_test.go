package phi

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

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
