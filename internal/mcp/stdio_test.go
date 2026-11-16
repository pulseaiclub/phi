package mcp

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// streamTransport builds a transport whose child stdout is the given frames, so
// response reading is testable without spawning a server.
func streamTransport(frames ...string) *stdioTransport {
	return &stdioTransport{
		name:   "test",
		stdout: bufio.NewReader(strings.NewReader(strings.Join(frames, "\n") + "\n")),
	}
}

// A server may send its own request (method and id) while a call is in flight.
// That frame is not the answer: returning it makes CallTool report empty output.
func TestReadResponseSkipsServerRequests(t *testing.T) {
	tr := streamTransport(
		`{"jsonrpc":"2.0","id":99,"method":"roots/list"}`,
		`{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}`,
	)

	rpc, err := tr.readResponse(7)
	require.NoError(t, err)
	require.Empty(t, rpc.Method)
	require.JSONEq(t, `{"tools":[]}`, string(rpc.Result))
}

func TestReadResponseSkipsNotifications(t *testing.T) {
	tr := streamTransport(
		`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}`,
		`{"jsonrpc":"2.0","id":3,"result":{"content":[]}}`,
	)

	rpc, err := tr.readResponse(3)
	require.NoError(t, err)
	require.NotNil(t, rpc.ID)
	require.JSONEq(t, `{"content":[]}`, string(rpc.Result))
}

// The late reply to a timed-out call must not be handed to the next call as its
// own result; the mismatch is the only way the caller can learn it desynced.
func TestReadResponseRejectsStaleID(t *testing.T) {
	tr := streamTransport(`{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)

	_, err := tr.readResponse(2)
	require.ErrorContains(t, err, "response id 1 does not match request 2")
}

// JSON-RPC allows the id to be echoed as a string; that still identifies the
// same request.
func TestReadResponseAcceptsNumericStringID(t *testing.T) {
	tr := streamTransport(`{"jsonrpc":"2.0","id":"12","result":{"tools":[]}}`)

	rpc, err := tr.readResponse(12)
	require.NoError(t, err)
	require.JSONEq(t, `{"tools":[]}`, string(rpc.Result))
}

func TestReadResponseSurfacesParseError(t *testing.T) {
	tr := streamTransport(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`)

	rpc, err := tr.readResponse(5)
	require.NoError(t, err)
	require.Equal(t, -32700, rpc.Error.Code)
}

func TestReadResponseReportsClosedStream(t *testing.T) {
	tr := &stdioTransport{name: "test", stdout: bufio.NewReader(strings.NewReader(""))}

	_, err := tr.readResponse(1)
	require.ErrorIs(t, err, io.EOF)
}
