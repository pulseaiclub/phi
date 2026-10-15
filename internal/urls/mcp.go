package urls

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MCPCall is a tool endpoint and its required JSON object arguments.
type MCPCall struct {
	Target MCPURL
	Args   map[string]any
}

// ParseMCPCall treats everything after the first whitespace as JSON, never shell
// syntax. UseNumber preserves integer precision, including nested arguments.
func ParseMCPCall(command string) (MCPCall, error) {
	var call MCPCall
	split := strings.IndexFunc(command, unicode.IsSpace)
	if split < 0 {
		return call, errors.New("expected mcp://<server>/tools/<tool> followed by a JSON object")
	}
	target, err := ParseMCPURL(command[:split])
	if err != nil {
		return call, err
	}
	if target.Kind != MCPTool {
		return call, errors.New("MCP call requires a tool endpoint")
	}
	decoder := json.NewDecoder(strings.NewReader(command[split:]))
	decoder.UseNumber()
	if err := decoder.Decode(&call.Args); err != nil {
		return call, fmt.Errorf("MCP arguments must be a JSON object: %w", err)
	}
	if call.Args == nil {
		return call, errors.New("MCP arguments must be a JSON object, not null")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return call, errors.New("unexpected content after MCP argument object")
	}
	call.Target = target
	return call, nil
}

// MCPURLKind identifies one level of the MCP browsing hierarchy.
type MCPURLKind uint8

const (
	MCPRoot   MCPURLKind = iota + 1 // mcp://
	MCPServer                       // mcp://<server>/
	MCPTools                        // mcp://<server>/tools/
	MCPTool                         // mcp://<server>/tools/<tool>
)

// MCPURL contains decoded, case-sensitive identifiers. Cursor is opaque;
// an empty cursor and a zero limit mean unspecified. Limits are capped at 1000.
type MCPURL struct {
	Kind   MCPURLKind
	Server string
	Tool   string
	Cursor string
	Limit  int
}

// ParseMCPURL splits the raw hierarchy before decoding identifiers exactly once.
// Authority is an opaque server identifier, never userinfo or a host/port pair.
func ParseMCPURL(input string) (MCPURL, error) {
	var target MCPURL
	u, err := ParseInternalURL(input)
	if err != nil {
		return target, err
	}
	if u.Protocol != "mcp:" {
		return target, fmt.Errorf("expected mcp:// URL, got %q", input)
	}
	if u.Hash != "" {
		return target, errors.New("MCP URLs do not allow fragments")
	}
	target.Server, _ = url.PathUnescape(u.RawHost)
	parts := strings.Split(u.RawPathname, "/")
	switch {
	case u.RawHost == "" && u.RawPathname == "":
		target.Kind = MCPRoot
	case u.RawHost != "" && u.RawPathname == "/":
		target.Kind = MCPServer
	case u.RawHost != "" && u.RawPathname == "/tools/":
		target.Kind = MCPTools
	case u.RawHost != "" && len(parts) == 3 && parts[1] == "tools":
		target.Kind = MCPTool
		target.Tool, _ = url.PathUnescape(parts[2])
	default:
		return target, errors.New(
			"invalid MCP hierarchy: expected mcp://, mcp://<server>/, or mcp://<server>/tools/[<tool>]",
		)
	}
	if target.Kind == MCPTool && u.Search != "" {
		return target, errors.New("MCP tool endpoints do not allow queries")
	}
	if err := target.parseQuery(u.Search); err != nil {
		return target, err
	}
	return target, target.validate()
}

func (u *MCPURL) parseQuery(search string) error {
	query, err := url.ParseQuery(strings.TrimPrefix(search, "?"))
	if err != nil {
		return fmt.Errorf("invalid MCP query: %w", err)
	}
	for key, values := range query {
		if len(values) != 1 {
			return fmt.Errorf("duplicate MCP query parameter %q", key)
		}
		switch key {
		case "cursor":
			u.Cursor = values[0]
		case "limit":
			value := values[0]
			if value == "" || strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
				return errors.New("MCP limit must be an integer from 1 to 1000")
			}
			u.Limit, err = strconv.Atoi(value)
			if err != nil || u.Limit < 1 {
				return errors.New("MCP limit must be an integer from 1 to 1000")
			}
		default:
			return fmt.Errorf("unknown MCP query parameter %q", key)
		}
	}
	return nil
}

func (u MCPURL) validate() error {
	if u.Kind < MCPRoot || u.Kind > MCPTool {
		return fmt.Errorf("invalid MCP URL kind %d", u.Kind)
	}
	needsServer := u.Kind != MCPRoot
	needsTool := u.Kind == MCPTool
	if needsServer != (u.Server != "") || needsTool != (u.Tool != "") {
		return fmt.Errorf("MCP server and tool identifiers do not match URL kind %d", u.Kind)
	}
	for _, name := range []string{u.Server, u.Tool} {
		if !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
			return errors.New("MCP identifiers must be valid UTF-8 without control characters")
		}
	}
	if u.Limit < 0 || u.Limit > 1000 {
		return errors.New("MCP limit must be an integer from 1 to 1000, or zero when unspecified")
	}
	if u.Kind == MCPTool && (u.Cursor != "" || u.Limit != 0) {
		return errors.New("MCP tool endpoints do not allow queries")
	}
	return nil
}

// Build returns a canonical URL using per-component escaping without path cleaning.
func (u MCPURL) Build() (InternalURL, error) {
	if err := u.validate(); err != nil {
		return InternalURL{}, err
	}
	href := "mcp://"
	if u.Kind != MCPRoot {
		href += url.PathEscape(u.Server) + "/"
	}
	if u.Kind == MCPTools || u.Kind == MCPTool {
		href += "tools/"
	}
	if u.Kind == MCPTool {
		href += url.PathEscape(u.Tool)
	}
	query := url.Values{}
	if u.Cursor != "" {
		query.Set("cursor", u.Cursor)
	}
	if u.Limit != 0 {
		query.Set("limit", strconv.Itoa(u.Limit))
	}
	if len(query) != 0 {
		href += "?" + query.Encode()
	}
	return ParseInternalURL(href)
}
