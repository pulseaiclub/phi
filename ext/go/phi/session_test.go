package phi

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

func TestSessionHostCallsInsideCommand(t *testing.T) {
	e := New("test", "1")
	e.RegisterCommand("rewind", ext.Command{Handler: func(_ string, _ *ext.Context) error {
		choice, err := e.Select(
			ext.SelectRequest{Title: "turn", Items: []ext.SelectItem{{Label: "first"}, {Label: "second"}}},
		)
		require.NoError(t, err)
		assert.Equal(t, ext.SelectReply{Index: 1, OK: true}, choice)
		turns, err := e.SessionTurns()
		require.NoError(t, err)
		require.Len(t, turns, 1)
		assert.Equal(t, "user-1", turns[0].EntryID)
		return e.SessionRewind(ext.RewindRequest{SessionID: "s", EntryID: turns[0].EntryID})
	}})
	var in, out bytes.Buffer
	handshake(t, &in)
	inputFrame(t, &in, pxb.TypeCommandInvoked, 10, pxb.EncodeCommandInvoked(pxb.CommandInvoked{Name: "rewind"}))
	for i, body := range []string{"1", `[{"entry_id":"user-1","text":"hello"}]`, "null"} {
		inputFrame(t, &in, pxb.TypeHostResult, uint32(i+1), pxb.EncodeHostResult(pxb.HostResult{OK: true, Body: body}))
	}
	inputFrame(t, &in, pxb.TypeShutdown, 0, nil)
	require.NoError(t, e.run(&in, &out))
	var methods []string
	for _, f := range outputFrames(t, &out) {
		if f.Type == pxb.TypeHostRequest {
			req, err := pxb.DecodeHostRequest(f.Body)
			require.NoError(t, err)
			methods = append(methods, req.Method)
		}
		if f.Type == pxb.TypeCommandResponse {
			res, err := pxb.DecodeCommandResponse(f.Body)
			require.NoError(t, err)
			assert.True(t, res.OK)
		}
	}
	assert.Equal(t, []string{"select", "session_turns", "session_rewind"}, methods)
}

func TestSelectCancellationAndInvalidResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  pxb.HostResult
		wantErr bool
	}{
		{"cancel", pxb.HostResult{}, false},
		{"range", pxb.HostResult{OK: true, Body: "3"}, true},
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
