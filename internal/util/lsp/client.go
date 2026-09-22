package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// A minimal LSP client: enough of the protocol to answer navigation questions
// (definition, references, hover, document symbols) and nothing else.
// Everything is best-effort — if a server is missing, slow or broken the caller
// gets an error and falls back to its own search.

// ---------------------------------------------------------------- protocol

type lspPosition struct {
	Line      int `json:"line"`      // 0-based
	Character int `json:"character"` // 0-based, in the negotiated encoding
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children"`
	// symbolInformation form, used by servers without hierarchical support.
	Location *lspLocation `json:"location"`
}

// symbolKindName turns a SymbolKind number from the spec into a display name.
func symbolKindName(kind int) string {
	switch kind {
	case 1:
		return "file"
	case 2:
		return "module"
	case 3:
		return "namespace"
	case 4:
		return "package"
	case 5:
		return "class"
	case 6:
		return "method"
	case 7:
		return "property"
	case 8:
		return "field"
	case 9:
		return "ctor"
	case 10:
		return "enum"
	case 11:
		return "interface"
	case 12:
		return "func"
	case 13:
		return "var"
	case 14:
		return "const"
	case 15:
		return "string"
	case 16:
		return "number"
	case 17:
		return "bool"
	case 18:
		return "array"
	case 19:
		return "object"
	case 20:
		return "key"
	case 21:
		return "null"
	case 22:
		return "enum"
	case 23:
		return "struct"
	case 24:
		return "event"
	case 25:
		return "operator"
	case 26:
		return "type"
	default:
		return "sym"
	}
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("lsp error %d: %s", e.Code, e.Message) }

// ---------------------------------------------------------------- client

// lspClient drives one language server subprocess.
type lspClient struct {
	def  serverDef
	root string

	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage
	opened  map[string]int // uri -> document version
	dead    error

	// encoding is how the server counts Character offsets: "utf-8", "utf-16"
	// (the spec default) or "utf-32".
	encoding string
}

func newClient(def serverDef, root string) *lspClient {
	return &lspClient{
		def: def, root: root,
		pending:  map[int64]chan rpcMessage{},
		opened:   map[string]int{},
		encoding: "utf-16",
	}
}

func (c *lspClient) start(ctx context.Context) error {
	// The server must outlive the handshake deadline that started it, so the
	// process is not tied to the caller's context.
	argv := c.def.Cmd
	//nolint:gosec // G204: the built-in registry names the binary
	c.cmd = exec.CommandContext(context.WithoutCancel(ctx), argv[0], argv[1:]...)
	c.cmd.Dir = c.root
	c.cmd.Stderr = io.Discard

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.cmd.Start(); err != nil {
		return err
	}
	c.in, c.out = stdin, bufio.NewReaderSize(stdout, 64<<10)

	go c.readLoop()
	go func() {
		_ = c.cmd.Wait() // the exit is reported through fail, not here
		c.fail(fmt.Errorf("%s exited", c.def.Name))
	}()

	return c.initialize(ctx)
}

func (c *lspClient) fail(err error) {
	c.mu.Lock()
	if c.dead == nil {
		c.dead = err
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *lspClient) alive() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dead
}

// readLoop consumes framed messages and routes them: responses to their waiting
// caller, server-initiated requests to a stub reply (a server that never hears
// back from us can stall), notifications to progress tracking.
func (c *lspClient) readLoop() {
	for {
		msg, err := readFrame(c.out)
		if err != nil {
			c.fail(err)
			return
		}
		switch {
		case msg.Method == "" && len(msg.ID) > 0: // response
			id, err := strconv.ParseInt(strings.Trim(string(msg.ID), `"`), 10, 64)
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch, ok := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ok {
				ch <- msg
				close(ch)
			}
		case len(msg.ID) > 0: // server -> client request; must be answered
			c.reply(msg.ID, msg.Method)
		}
		// Notifications need no reply: $/progress and friends are ignored, and a
		// live process counts as ready either way.
	}
}

func (c *lspClient) reply(id json.RawMessage, method string) {
	var result any
	switch method {
	case "workspace/configuration":
		result = []any{map[string]any{}}
	case "workspace/workspaceFolders":
		result = []any{map[string]string{"uri": pathToURI(c.root), "name": filepath.Base(c.root)}}
	default:
		result = nil
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_ = c.write(body) // best effort: a dead server is noticed by the reader
}

func readFrame(r *bufio.Reader) (rpcMessage, error) {
	var length int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return rpcMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, val, ok := strings.Cut(line, ":"); ok &&
			strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(val))
		}
	}
	if length <= 0 || length > 64<<20 {
		return rpcMessage{}, fmt.Errorf("bad content length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return rpcMessage{}, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		return rpcMessage{}, err
	}
	return msg, nil
}

func (c *lspClient) write(body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead != nil {
		return c.dead
	}
	if c.in == nil {
		return errors.New("lsp client stdin closed")
	}
	if _, err := fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := c.in.Write(body)
	return err
}

func (c *lspClient) notify(method string, params any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	return c.write(body)
}

func (c *lspClient) call(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	if c.dead != nil {
		err := c.dead
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	drop := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	// abandon cleans up after a timed-out call on its own goroutine: write holds
	// c.mu while it reaches the server, so waiting for the lock here would wait
	// for exactly the wedged server the deadline was meant to escape.
	abandon := func() {
		go func() {
			drop()
			c.cancel(id)
		}()
	}

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		drop()
		return err
	}

	// The write runs on its own goroutine: a server that stopped draining its
	// stdin must not be able to pin this caller (or c.mu) past its deadline.
	written := make(chan error, 1)
	go func() { written <- c.write(body) }()

	select {
	case <-ctx.Done():
		abandon()
		return ctx.Err()
	case err := <-written:
		if err != nil {
			drop()
			return err
		}
	}

	select {
	case <-ctx.Done():
		abandon()
		return ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return fmt.Errorf("%s: connection lost", c.def.Name)
		}
		if msg.Error != nil {
			return msg.Error
		}
		if out == nil || len(msg.Result) == 0 || string(msg.Result) == "null" {
			return nil
		}
		return json.Unmarshal(msg.Result, out)
	}
}

// cancel tells the server to stop working on a request nobody waits for.
func (c *lspClient) cancel(id int64) {
	_ = c.notify("$/cancelRequest", map[string]any{"id": id})
}

// initializeParams builds the handshake: who we are, which root we work in,
// and the small set of features this client asks for. Servers that need more
// (semantic tokens, workspace symbols, call hierarchy) get no promises from us.
func (c *lspClient) initializeParams() map[string]any {
	return map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToURI(c.root),
		"clientInfo": map[string]string{
			"name": "phi",
		},
		"workspaceFolders": []any{
			map[string]string{"uri": pathToURI(c.root), "name": filepath.Base(c.root)},
		},
		"capabilities": map[string]any{
			"general": map[string]any{
				// Ask for byte offsets so we can skip UTF-16 conversion where
				// the server is willing; we handle either answer.
				"positionEncodings": []string{"utf-8", "utf-16"},
			},
			"workspace": map[string]any{
				"workspaceFolders": true,
				"configuration":    true,
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{"didSave": false, "dynamicRegistration": false},
				"definition":      map[string]any{"linkSupport": true},
				"references":      map[string]any{"dynamicRegistration": false},
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
					"dynamicRegistration":               false,
				},
				// Order matters: servers pick the first format they support,
				// and markdown is what carries the fenced signature block.
				"hover": map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			},
			"window": map[string]any{"workDoneProgress": true},
		},
		"initializationOptions": c.def.InitOptions,
	}
}

func (c *lspClient) initialize(ctx context.Context) error {
	var res struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	if err := c.call(ctx, "initialize", c.initializeParams(), &res); err != nil {
		return err
	}
	if res.Capabilities.PositionEncoding != "" {
		c.encoding = res.Capabilities.PositionEncoding
	}
	return c.notify("initialized", map[string]any{})
}

func (c *lspClient) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.call(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", nil)
	if c.in != nil {
		_ = c.in.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		time.AfterFunc(time.Second, func() { _ = c.cmd.Process.Kill() })
	}
}

// ensureOpen tells the server about a file. Most servers refuse to answer
// questions about a document they were never handed.
func (c *lspClient) ensureOpen(abs, rel string) error {
	uri := pathToURI(abs)
	c.mu.Lock()
	_, already := c.opened[uri]
	c.mu.Unlock()
	if already {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	if err := c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": c.def.languageID(rel),
			"version":    1,
			"text":       string(data),
		},
	}); err != nil {
		return err
	}
	c.mu.Lock()
	c.opened[uri] = 1
	c.mu.Unlock()
	return nil
}

// closeDoc notifies the server that the file was closed, allowing the server to
// free ASTs and file memory.
func (c *lspClient) closeDoc(abs string) {
	uri := pathToURI(abs)
	c.mu.Lock()
	_, already := c.opened[uri]
	if !already {
		c.mu.Unlock()
		return
	}
	delete(c.opened, uri)
	c.mu.Unlock()
	_ = c.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{
			"uri": uri,
		},
	})
}

// ---------------------------------------------------------------- positions

// toLSP converts a 1-based line and 0-based byte column into the offsets the
// server expects. The spec counts UTF-16 code units by default, which is not
// what Go gives us.
func (c *lspClient) toLSP(lineText string, line, byteCol int) lspPosition {
	if byteCol > len(lineText) {
		byteCol = len(lineText)
	}
	prefix := lineText[:byteCol]
	var ch int
	switch c.encoding {
	case "utf-8":
		ch = byteCol
	case "utf-32":
		ch = utf8.RuneCountInString(prefix)
	default:
		ch = len(utf16.Encode([]rune(prefix)))
	}
	return lspPosition{Line: line - 1, Character: ch}
}

// fromLSP converts a server position back into a 1-based line and 0-based byte
// column against the lines we have on disk.
func (c *lspClient) fromLSP(lines []string, p lspPosition) (int, int) {
	line := p.Line + 1
	if p.Line < 0 || p.Line >= len(lines) {
		return line, 0
	}
	text := lines[p.Line]
	switch c.encoding {
	case "utf-8":
		if p.Character > len(text) {
			return line, len(text)
		}
		return line, p.Character
	case "utf-32":
		r := []rune(text)
		if p.Character > len(r) {
			return line, len(text)
		}
		return line, len(string(r[:p.Character]))
	default:
		units, bytes := 0, 0
		for _, r := range text {
			if units >= p.Character {
				break
			}
			units += len(utf16.Encode([]rune{r}))
			bytes += utf8.RuneLen(r)
		}
		return line, bytes
	}
}

// ---------------------------------------------------------------- uris

// pathToURI renders an absolute path as a file: URI. Windows drive paths are
// normalised even when we are not running on Windows: a server on another host,
// or a test, may hand us one, and "file:C:/x" is not a URI other tools parse.
func pathToURI(p string) string {
	p = filepath.ToSlash(p)
	switch {
	case isDrivePath(strings.ReplaceAll(p, `\`, "/")):
		p = "/" + strings.ReplaceAll(p, `\`, "/")
	case runtime.GOOS == "windows" && !strings.HasPrefix(p, "/"):
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	return u.String()
}

// uriToPath turns a file: URI back into a path. The leading slash of
// "file:///C:/x" belongs to the URI, not the path, so it goes away again.
func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" && u.Scheme != "" {
		return "", fmt.Errorf("not a file uri: %s", uri)
	}
	p := u.Path
	if p == "" {
		// The "file:C:/x" form parses into Opaque rather than Path.
		p = u.Opaque
	}
	if runtime.GOOS == "windows" || isDrivePath(strings.TrimPrefix(p, "/")) {
		p = strings.TrimPrefix(p, "/")
	}
	return filepath.FromSlash(p), nil
}

// isDrivePath reports whether p starts with a Windows drive such as "C:/".
func isDrivePath(p string) bool {
	if len(p) < 3 || p[1] != ':' || p[2] != '/' {
		return false
	}
	c := p[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
