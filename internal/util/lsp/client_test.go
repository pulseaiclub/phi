package lsp

import (
	"bufio"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func frame(body string) string {
	return "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
}

// Position encodings are the classic source of off-by-N bugs in LSP clients:
// Go counts bytes, JavaScript counts UTF-16 units, and the protocol defaults to
// UTF-16 while some servers negotiate UTF-8.
func TestPositionEncodings(t *testing.T) {
	// "héllo → wörld" mixes 1-, 2- and 3-byte runes.
	line := "héllo → wörld"
	cases := []struct {
		enc     string
		word    string
		wantCol int
	}{
		{"utf-8", "wörld", strings.Index(line, "wörld")}, // bytes
		{"utf-16", "wörld", 8},                           // h é l l o ␠ → ␠
		{"utf-32", "wörld", 8},                           // same here: no surrogates
	}
	for _, c := range cases {
		t.Run(c.enc, func(t *testing.T) {
			cl := &lspClient{encoding: c.enc}
			byteCol := strings.Index(line, c.word)

			got := cl.toLSP(line, 1, byteCol)
			assert.Equal(t, c.wantCol, got.Character)
			assert.Equal(t, 0, got.Line) // LSP is 0-based

			// Round-tripping must land back on the same byte.
			gotLine, gotByte := cl.fromLSP([]string{line}, got)
			assert.Equal(t, 1, gotLine)
			assert.Equal(t, byteCol, gotByte)
		})
	}
}

func TestFromLSPPastEndOfLine(t *testing.T) {
	c := &lspClient{encoding: "utf-16"}

	line, col := c.fromLSP([]string{"abc"}, lspPosition{Line: 0, Character: 99})
	assert.Equal(t, 1, line)
	assert.Equal(t, 3, col)

	// A line number no file has still yields a usable 1-based line.
	line, col = c.fromLSP([]string{"abc"}, lspPosition{Line: 7, Character: 2})
	assert.Equal(t, 8, line)
	assert.Equal(t, 0, col)
}

func TestUTF16ToByte(t *testing.T) {
	cases := []struct {
		line string
		u16  int
		want int
	}{
		{"abc", 0, 0},
		{"abc", 2, 2},
		{"abc", 99, 3},  // past the end clamps
		{"héllo", 2, 3}, // é is two bytes
		{"→x", 1, 3},    // → is three bytes, one UTF-16 unit
		{"🎉x", 2, 4},    // emoji is a surrogate pair: two units, four bytes
		{"🎉x", 3, 5},
		{"", 5, 0},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, utf16ToByte(c.line, c.u16), "utf16ToByte(%q, %d)", c.line, c.u16)
	}
}

func TestURIRoundTrip(t *testing.T) {
	cases := []string{
		"/home/user/project/main.go",
		"/home/user/my project/a b.go", // spaces must be escaped
		"/tmp/weird#name$x.go",
		"/tmp/über/文件.go",
	}
	for _, p := range cases {
		uri := pathToURI(p)
		assert.True(t, strings.HasPrefix(uri, "file://"), "pathToURI(%q) = %q", p, uri)
		assert.NotContains(t, uri, " ")

		got, err := uriToPath(uri)
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean(p), filepath.Clean(got))
	}
}

// A Windows path has to survive the trip even when the client runs elsewhere: a
// server, a test or a pasted path may hand us one.
func TestURIRoundTripWindowsPath(t *testing.T) {
	p := `C:\Users\me\my project\a b.go`
	uri := pathToURI(p)
	assert.Equal(t, "file:///C:/Users/me/my%20project/a%20b.go", uri)

	got, err := uriToPath(uri)
	require.NoError(t, err)
	// FromSlash only turns separators into backslashes on Windows.
	assert.Equal(t, filepath.FromSlash("C:/Users/me/my project/a b.go"), got)
}

func TestURIToPathRejectsOtherSchemes(t *testing.T) {
	_, err := uriToPath("https://example.com/x.go")
	assert.Error(t, err)

	// The opaque "file:C:/x" form some clients send still resolves.
	got, err := uriToPath("file:C:/x/y.go")
	require.NoError(t, err)
	assert.Equal(t, filepath.FromSlash("C:/x/y.go"), got)
}

// Servers frame every message with a Content-Length header, and not all of them
// use \r\n. The reader must survive both, and a pipe that hands over one byte
// at a time.
func TestReadFrameSplitReader(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`
	stream := frame(body) + "Content-Length: " + strconv.Itoa(len(body)) + "\n\n" + body

	r := bufio.NewReader(iotest.OneByteReader(strings.NewReader(stream)))

	for i := range 2 {
		msg, err := readFrame(r)
		require.NoError(t, err, "frame %d", i)
		assert.Equal(t, `1`, string(msg.ID))
		assert.JSONEq(t, `{"ok":true}`, string(msg.Result))
		assert.Empty(t, msg.Method)
	}

	_, err := readFrame(r)
	assert.Error(t, err, "the stream is exhausted")
}

func TestReadFrameRejectsBadHeaders(t *testing.T) {
	cases := map[string]string{
		"zero length":  "Content-Length: 0\r\n\r\n",
		"no length":    "\r\n{}",
		"missing body": "Content-Length: 40\r\n\r\n{}",
		"absurd":       "Content-Length: 999999999999\r\n\r\n",
	}
	for name, stream := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readFrame(bufio.NewReader(strings.NewReader(stream)))
			assert.Error(t, err)
		})
	}
}

func TestReadFrameTruncatedBody(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":2,"method":"x","params":{"a":"ünï"}}`
	stream := frame(body)[:len(frame(body))-10]
	_, err := readFrame(bufio.NewReader(strings.NewReader(stream)))
	assert.Error(t, err)
}

// closeDoc forgets the URI, so a later ensureOpen re-sends the file, and a
// repeated close is harmless.
func TestCloseDocForgetsURI(t *testing.T) {
	c := newClient(serverDef{Name: "test", Cmd: []string{"echo"}}, t.TempDir())
	c.opened[pathToURI("/test.go")] = 1

	c.closeDoc("/test.go")
	c.mu.Lock()
	_, exists := c.opened[pathToURI("/test.go")]
	c.mu.Unlock()
	assert.False(t, exists)

	c.closeDoc("/test.go") // no panic, nothing re-added
	c.mu.Lock()
	assert.Empty(t, c.opened)
	c.mu.Unlock()
}

// The handshake names the client, points the server at the root and asks only
// for the features this package uses.
func TestInitializeParams(t *testing.T) {
	root := t.TempDir()
	c := newClient(serverDef{Name: "x", DefaultLang: "go", InitOptions: map[string]any{"a": 1}}, root)

	p := c.initializeParams()
	assert.Equal(t, pathToURI(root), p["rootUri"])
	assert.Equal(t, map[string]any{"a": 1}, p["initializationOptions"])

	info, ok := p["clientInfo"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "phi", info["name"])

	folders, ok := p["workspaceFolders"].([]any)
	require.True(t, ok)
	require.Len(t, folders, 1)
	assert.Equal(t, pathToURI(root), folders[0].(map[string]string)["uri"])

	caps, ok := p["capabilities"].(map[string]any)
	require.True(t, ok)
	td, ok := caps["textDocument"].(map[string]any)
	require.True(t, ok)
	for _, want := range []string{"definition", "references", "hover", "documentSymbol", "synchronization"} {
		assert.Contains(t, td, want)
	}
	assert.NotContains(t, td, "callHierarchy") // dropped with the call-hierarchy port
}

func TestLanguageIDMapping(t *testing.T) {
	var ts serverDef
	for _, d := range serverDefs() {
		if d.Name == "typescript" {
			ts = d
		}
	}
	require.Equal(t, "typescript", ts.Name, "the registry no longer lists typescript")

	cases := map[string]string{
		"a.ts":  "typescript",
		"a.tsx": "typescriptreact",
		"a.jsx": "javascriptreact",
		"a.js":  "javascript",
		"a.mjs": "javascript",
	}
	for file, want := range cases {
		assert.Equal(t, want, ts.languageID(file), "languageID(%q)", file)
	}
}
