package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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

func TestReadResponseRejectsTrailingData(t *testing.T) {
	for _, suffix := range []string{" trailing-junk", ` {"jsonrpc":"2.0","id":7,"result":{}}`} {
		tr := streamTransport(`{"jsonrpc":"2.0","id":7,"result":{}}` + suffix)

		_, err := readResponse(tr.stdout, tr.stdin, 7)
		require.ErrorContains(t, err, "parse response")
	}
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

	// The ping id must come back byte for byte: decoding it as a float64 would
	// round it to 9007199254740992.
	var ping struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &ping))
	require.Equal(t, "9007199254740993", string(ping.ID))
	require.JSONEq(t, `{}`, string(ping.Result))
	require.JSONEq(t, `{"jsonrpc":"2.0","id":99,"error":{"code":-32601,"message":"method not found"}}`, lines[1])
}

// The stdio tests below run their fake server as this test binary, not as a
// shell script: Windows has no `#!/bin/sh`, so a script fixture passed on three
// runners and failed on the fourth.
const (
	helperModeEnv   = "PHI_MCP_STDIO_HELPER"
	helperMarkerEnv = "PHI_MCP_STDIO_HELPER_MARKER"

	helperStaleID    = "stale-id"
	helperLateReply  = "late-reply"
	helperHoldStdout = "hold-stdout"
	helperDescendant = "descendant"
)

// TestStdioHelperProcess is not a test: it is the fake MCP server the stdio
// transport spawns, so the same fixture runs on Linux, macOS, and Windows.
func TestStdioHelperProcess(t *testing.T) {
	switch os.Getenv(helperModeEnv) {
	case "":
		return
	case helperDescendant:
		// A descendant that keeps the inherited stdout open after its parent dies
		// is what makes a naive Close wait forever.
		time.Sleep(30 * time.Second)
		return
	case helperStaleID:
		serveStaleID(os.Getenv(helperMarkerEnv))
	case helperLateReply:
		serveLateReply(os.Getenv(helperMarkerEnv))
	case helperHoldStdout:
		serveHoldingStdout(t, os.Getenv(helperMarkerEnv))
	}
	// The transport kills this process; returning would print PASS on the pipe
	// the parent is still parsing.
	for {
		time.Sleep(time.Hour)
	}
}

// stdioHelper builds a server config that reruns this test binary in the given
// helper mode.
func stdioHelper(mode, marker string) ServerConfig {
	return ServerConfig{
		Command: []string{os.Args[0], "-test.run=^TestStdioHelperProcess$"},
		Env: map[string]string{
			helperModeEnv:   mode,
			helperMarkerEnv: marker,
		},
	}
}

// forEachRequest calls fn with the id and method of each JSON-RPC request on
// stdin. Notifications carry no id and are skipped, as a server would.
func forEachRequest(fn func(id, method string)) {
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(scan.Bytes(), &req); err != nil || len(req.ID) == 0 {
			continue
		}
		fn(string(req.ID), req.Method)
	}
}

func writeStdioFrame(frame string) {
	_, _ = fmt.Fprintln(os.Stdout, frame)
}

func writeInitializeResult(id string) {
	writeStdioFrame(`{"jsonrpc":"2.0","id":` + id + `,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}`)
}

// serveStaleID answers the first tools/call with a reply carrying another id
// and then the real reply, so the transport must reject the first frame. The
// marker records that the first run happened, so the restarted server answers
// "ok".
func serveStaleID(marker string) {
	restarted := false
	if _, err := os.Stat(marker); err == nil {
		restarted = true
	} else {
		_ = os.WriteFile(marker, []byte("1"), 0o600)
	}
	forEachRequest(func(id, method string) {
		switch method {
		case "initialize":
			writeInitializeResult(id)
		case "tools/call":
			if restarted {
				writeStdioFrame(`{"jsonrpc":"2.0","id":` + id + `,"result":{"content":[{"type":"text","text":"ok"}]}}`)
				return
			}
			writeStdioFrame(`{"jsonrpc":"2.0","id":999,"result":{"content":[]}}`)
			writeStdioFrame(`{"jsonrpc":"2.0","id":` + id + `,"result":{"content":[{"type":"text","text":"stale"}]}}`)
		}
	})
}

// serveLateReply answers the first tools/call after a short delay. A retry
// before that delay must use a fresh transport rather than share its reader.
func serveLateReply(marker string) {
	forEachRequest(func(id, method string) {
		switch method {
		case "initialize":
			writeInitializeResult(id)
		case "tools/call":
			if _, err := os.Stat(marker); err == nil {
				writeStdioFrame(`{"jsonrpc":"2.0","id":` + id + `,"result":{"content":[{"type":"text","text":"ok"}]}}`)
				return
			}
			_ = os.WriteFile(marker, []byte("1"), 0o600)
			time.Sleep(250 * time.Millisecond)
			writeStdioFrame(`{"jsonrpc":"2.0","id":` + id + `,"result":{"content":[{"type":"text","text":"late"}]}}`)
		}
	})
}

// serveHoldingStdout starts a descendant that inherits the transport's stdout
// pipe, then records the call and never answers it, so only Close can end it.
func serveHoldingStdout(t *testing.T, marker string) {
	//nolint:gosec // the argv is this test binary, not user input
	descendant := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStdioHelperProcess$")
	descendant.Env = append(os.Environ(), helperModeEnv+"="+helperDescendant)
	descendant.Stdout = os.Stdout
	if err := descendant.Start(); err != nil {
		return
	}
	forEachRequest(func(id, method string) {
		switch method {
		case "initialize":
			writeInitializeResult(id)
		case "tools/call":
			_ = os.WriteFile(marker, []byte("called"), 0o600)
		}
	})
}
