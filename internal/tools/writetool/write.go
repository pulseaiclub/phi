package writetool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

var writeDescription = `Write content to a file. Creates the file if it does not exist; overwrites the entire file if it does. Creates parent directories.
Calling an MCP tool is also a write: path mcp://<server>/tools/<tool> and content set to that tool's JSON argument object (read the tool first for its params).`

// WriteTool returns the write tool definition + handler. pool enables MCP tool
// invocations via mcp:// URLs; nil keeps write file-only.
func WriteTool(pool *mcp.Pool) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name:        "write",
			Description: writeDescription,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"path": llm.Object{
						"type":        "string",
						"description": "File path to write (created or overwritten), or mcp://<server>/tools/<tool> to call an MCP tool. Example: src/new.go",
					},
					"content": llm.Object{
						"type":        "string",
						"description": "Content to write to the file, or the MCP tool's JSON argument object when path is an mcp:// URL.",
					},
				},
				Required: []string{"path", "content"},
			},
		}),
		tooldef.WithDetail(writeDetail),
		tooldef.WithHandler(func(ctx context.Context, in writeInput) (tooldef.Result, error) {
			return runWrite(ctx, pool, in)
		}),
	)
}

func writeDetail(in writeInput) string {
	return strings.TrimSpace(in.Path)
}

type writeInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func runWrite(ctx context.Context, pool *mcp.Pool, in writeInput) (tooldef.Result, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return tooldef.Result{}, errors.New("path is required")
	}

	if target, err := tooldef.ParseMCPTarget(path); err != nil {
		return tooldef.Result{}, err
	} else if target != nil {
		body, err := tooldef.MCPWrite(ctx, pool, target, in.Content)
		if err != nil {
			return tooldef.Result{}, err
		}
		return tooldef.Result{Content: body, Detail: target.Server + "/" + target.Tool, Output: body}, nil
	}

	path, err := tooldef.ResolveToCwd(ctx, path)
	if err != nil {
		return tooldef.Result{}, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return tooldef.Result{}, fmt.Errorf("failed to create parent directories: %w", err)
	}

	//nolint:gosec // G306: source files should stay world-readable
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return tooldef.Result{}, fmt.Errorf("failed to write file %s: %w", path, err)
	}

	display := tooldef.RelToCwd(ctx, path)
	detail := fmt.Sprintf("wrote %d bytes to %s", len(in.Content), display)
	return tooldef.Result{Content: detail, Detail: display, Output: detail}, nil
}
