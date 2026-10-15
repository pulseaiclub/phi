package tooldef

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/urls"
)

// MCPTarget is a parsed mcp:// URL.
type MCPTarget struct {
	Kind   urls.MCPURLKind
	Server string
	Tool   string
}

// ParseMCPTarget parses path as an mcp:// URL. Returns (nil, nil) when path is
// not an MCP URL so callers fall through to file handling; a malformed MCP URL
// is an error, never a silent fallback.
func ParseMCPTarget(path string) (*MCPTarget, error) {
	trimmed := strings.TrimSpace(path)
	if !strings.HasPrefix(trimmed, "mcp://") {
		return nil, nil
	}
	u, err := urls.ParseMCPURL(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid MCP URL: %w", err)
	}
	return &MCPTarget{Kind: u.Kind, Server: u.Server, Tool: u.Tool}, nil
}

// MCPList resolves list targets: mcp:// names servers, mcp://<server>/tools/
// names tools. Displayed URLs are clickable next-hop targets.
func MCPList(ctx context.Context, pool *mcp.Pool, target *MCPTarget) (string, error) {
	if err := requirePool(pool); err != nil {
		return "", err
	}
	switch target.Kind {
	case urls.MCPRoot:
		names := pool.ServerNames()
		if len(names) == 0 {
			return "(no MCP servers configured)", nil
		}
		var b strings.Builder
		b.WriteString("MCP servers:\n")
		for _, name := range names {
			fmt.Fprintf(&b, "  mcp://%s/tools/\n", name)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case urls.MCPServer:
		return fmt.Sprintf("mcp://%s/tools/", target.Server), nil
	case urls.MCPTools:
		tools, err := pool.ListTools(ctx, target.Server)
		if err != nil {
			return "", fmt.Errorf("failed to list tools on server %q: %w", target.Server, err)
		}
		if len(tools) == 0 {
			return fmt.Sprintf("(no tools on server %q)", target.Server), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Tools on %q:\n", target.Server)
		for _, t := range tools {
			fmt.Fprintf(&b, "  mcp://%s/tools/%s\n", target.Server, t.Name)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	default:
		return "", fmt.Errorf(
			"cannot list a tool endpoint; read mcp://%s/tools/%s to inspect it",
			target.Server,
			target.Tool,
		)
	}
}

// MCPRead returns a compact parameter summary for mcp://<server>/tools/<tool>.
func MCPRead(ctx context.Context, pool *mcp.Pool, target *MCPTarget) (string, error) {
	if err := requirePool(pool); err != nil {
		return "", err
	}
	if target.Kind != urls.MCPTool {
		return "", fmt.Errorf("read expects mcp://<server>/tools/<tool>; list %s first", mcpListURL(target))
	}
	def, err := pool.Inspect(ctx, target.Server, target.Tool)
	if err != nil {
		return "", fmt.Errorf("failed to inspect %q on server %q: %w", target.Tool, target.Server, err)
	}
	header := fmt.Sprintf("@mcp %s/tools/%s", target.Server, target.Tool)
	return header + "\n" + mcp.SlimTool(*def), nil
}

// MCPWrite invokes mcp://<server>/tools/<tool>; content is the JSON argument
// object (empty content means no arguments).
func MCPWrite(ctx context.Context, pool *mcp.Pool, target *MCPTarget, content string) (string, error) {
	if err := requirePool(pool); err != nil {
		return "", err
	}
	if target.Kind != urls.MCPTool {
		return "", fmt.Errorf("write expects mcp://<server>/tools/<tool>; list %s to find tools", mcpListURL(target))
	}
	var args map[string]any
	if trimmed := strings.TrimSpace(content); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", fmt.Errorf("content must be a JSON object of tool arguments: %w", err)
		}
		if args == nil {
			return "", errors.New("content must be a JSON object of tool arguments, not null")
		}
	}
	out, err := pool.Call(ctx, target.Server, target.Tool, args)
	if err != nil {
		return "", fmt.Errorf("MCP call %s/tools/%s failed: %w", target.Server, target.Tool, err)
	}
	return out, nil
}

func requirePool(pool *mcp.Pool) error {
	if pool == nil {
		return errors.New("mcp:// is unavailable: no MCP servers configured")
	}
	return nil
}

func mcpListURL(target *MCPTarget) string {
	if target == nil || target.Server == "" {
		return "mcp://"
	}
	return "mcp://" + target.Server + "/tools/"
}
