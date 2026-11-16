package mcp

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type nopWriteCloser struct {
	*bytes.Buffer
}

func (nopWriteCloser) Close() error { return nil }

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

	rpc, err := readResponse(tr.stdout, tr.stdin, 7)
	require.NoError(t, err)
	require.Empty(t, rpc.Method)
	require.JSONEq(t, `{"tools":[]}`, string(rpc.Result))
}

func TestReadResponseSkipsNotifications(t *testing.T) {
	tr := streamTransport(
		`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}`,
		`{"jsonrpc":"2.0","id":3,"result":{"content":[]}}`,
	)

	rpc, err := readResponse(tr.stdout, tr.stdin, 3)
	require.NoError(t, err)
	require.NotNil(t, rpc.ID)
	require.JSONEq(t, `{"content":[]}`, string(rpc.Result))
}

// The late reply to a timed-out call must not be handed to the next call as its
// own result; the mismatch is the only way the caller can learn it desynced.
func TestReadResponseRejectsStaleID(t *testing.T) {
	tr := streamTransport(`{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)

	_, err := readResponse(tr.stdout, tr.stdin, 2)
	require.ErrorContains(t, err, "response id 1 does not match request 2")
}

// JSON-RPC allows the id to be echoed as a string; that still identifies the
// same request.
func TestReadResponseAcceptsNumericStringID(t *testing.T) {
	tr := streamTransport(`{"jsonrpc":"2.0","id":"12","result":{"tools":[]}}`)

	rpc, err := readResponse(tr.stdout, tr.stdin, 12)
	require.NoError(t, err)
	require.JSONEq(t, `{"tools":[]}`, string(rpc.Result))
}

func TestReadResponseSurfacesParseError(t *testing.T) {
	tr := streamTransport(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`)

	rpc, err := readResponse(tr.stdout, tr.stdin, 5)
	require.NoError(t, err)
	require.Equal(t, -32700, rpc.Error.Code)
}

func TestReadResponseReportsClosedStream(t *testing.T) {
	tr := &stdioTransport{name: "test", stdout: bufio.NewReader(strings.NewReader(""))}

	_, err := readResponse(tr.stdout, tr.stdin, 1)
	require.ErrorIs(t, err, io.EOF)
}

func TestReadResponseRepliesToServerPingAndUnsupportedRequests(t *testing.T) {
	var sent bytes.Buffer
	tr := &stdioTransport{
		name:  "test",
		stdin: nopWriteCloser{&sent},
		stdout: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`{"jsonrpc":"2.0","id":9007199254740993,"method":"ping"}`,
			`{"jsonrpc":"2.0","id":99,"method":"roots/list"}`,
			`{"jsonrpc":"2.0","method":"notifications/progress"}`,
			`{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}`,
		}, "\n") + "\n")),
	}

	rpc, err := readResponse(tr.stdout, tr.stdin, 7)
	require.NoError(t, err)
	require.JSONEq(t, `{"tools":[]}`, string(rpc.Result))

	lines := strings.Split(strings.TrimSpace(sent.String()), "\n")
	require.Len(t, lines, 2)
	require.Equal(t, `{"id":9007199254740993,"jsonrpc":"2.0","result":{}}`, lines[0])
	require.JSONEq(t, `{"jsonrpc":"2.0","id":99,"error":{"code":-32601,"message":"method not found"}}`, lines[1])
}

func TestSessionResetsAfterResponseIDMismatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_STATE_HOME", tmp)

	marker := filepath.Join(tmp, "restarted")
	scriptPath := filepath.Join(tmp, "server.sh")
	script := `#!/bin/sh
first=1
if [ -f "` + marker + `" ]; then
  first=0
else
  touch "` + marker + `"
fi
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}\n' "$id"
      ;;
    *'"method":"tools/call"'*)
      if [ "$first" = "1" ]; then
        printf '{"jsonrpc":"2.0","id":999,"result":{"content":[]}}\n'
        printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"stale"}]}}\n' "$id"
      else
        printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"ok"}]}}\n' "$id"
      fi
      ;;
  esac
done
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))

	tr, err := newStdioTransport("test", ServerConfig{Command: []string{scriptPath}})
	require.NoError(t, err)
	sess := newSession("test", tr)
	defer func() { _ = sess.Close() }()

	_, err = sess.CallTool(context.Background(), "echo", nil)
	require.ErrorIs(t, err, errResponseIDMismatch)

	out, err := sess.CallTool(context.Background(), "echo", nil)
	require.NoError(t, err)
	require.Equal(t, "ok", out)
}
