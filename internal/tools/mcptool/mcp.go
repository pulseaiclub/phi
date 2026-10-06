package mcptool

import (
	"context"
	"errors"
	"fmt"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// Tools returns the three MCP meta-tools bound to pool.
// If pool is nil, returns nil.
func Tools(pool *mcp.Pool) []tooldef.Tool {
	if pool == nil {
		return nil
	}
	return []tooldef.Tool{
		listTool(pool),
		inspectTool(pool),
		callTool(pool),
	}
}

type serverInput struct {
	Server string `json:"server"`
}

type serverToolInput struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
}

type callInput struct {
	Server string         `json:"server"`
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
}

func listTool(pool *mcp.Pool) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "mcp_list",
			Description: `List MCP tool names on one server (compact text, not full JSON schemas).

Returns space-separated tool names. Schemas never enter the model context — use mcp_inspect for one tool's params.`,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"server": llm.Object{
						"type":        "string",
						"description": "MCP server name",
					},
				},
				Required: []string{"server"},
			},
		}),
		tooldef.WithDetail(func(in serverInput) string { return in.Server }),
		tooldef.WithHandler(func(ctx context.Context, in serverInput) (tooldef.Result, error) {
			if in.Server == "" {
				return tooldef.Result{}, errors.New("mcp_list: server is required")
			}
			tools, err := pool.ListTools(ctx, in.Server)
			if err != nil {
				return tooldef.Result{}, err
			}
			body := mcp.CompactToolNames(tools)
			return tooldef.Result{
				Content: body,
				Detail:  fmt.Sprintf("%s: %d tools", in.Server, len(tools)),
				Output:  body,
			}, nil
		}),
	)
}

func inspectTool(pool *mcp.Pool) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "mcp_inspect",
			Description: `Show a compact parameter summary for one MCP tool (slim text).

Use after mcp_list to learn required args before mcp_call.`,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"server": llm.Object{
						"type":        "string",
						"description": "MCP server name",
					},
					"tool": llm.Object{
						"type":        "string",
						"description": "Tool name on that server",
					},
				},
				Required: []string{"server", "tool"},
			},
		}),
		tooldef.WithDetail(func(in serverToolInput) string { return in.Server + "/" + in.Tool }),
		tooldef.WithHandler(func(ctx context.Context, in serverToolInput) (tooldef.Result, error) {
			def, err := pool.Inspect(ctx, in.Server, in.Tool)
			if err != nil {
				return tooldef.Result{}, err
			}
			body := mcp.SlimTool(*def)
			return tooldef.Result{Content: body, Detail: in.Server + "/" + in.Tool, Output: body}, nil
		}),
	)
}

func callTool(pool *mcp.Pool) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "mcp_call",
			Description: `Call one MCP tool on a configured server.

Prefer mcp_list then mcp_inspect before calling unfamiliar tools.`,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"server": llm.Object{
						"type":        "string",
						"description": "MCP server name",
					},
					"tool": llm.Object{
						"type":        "string",
						"description": "Tool name on that server",
					},
					"args": llm.Object{
						"type":        "object",
						"description": "JSON object of tool arguments",
					},
				},
				Required: []string{"server", "tool"},
			},
		}),
		tooldef.WithDetail(func(in serverToolInput) string { return in.Server + "/" + in.Tool }),
		tooldef.WithHandler(func(ctx context.Context, in callInput) (tooldef.Result, error) {
			out, err := pool.Call(ctx, in.Server, in.Tool, in.Args)
			if err != nil {
				return tooldef.Result{}, err
			}
			body := mcp.FormatCallResult(out, 32_000)
			return tooldef.Result{Content: body, Detail: in.Server + "/" + in.Tool, Output: body}, nil
		}),
	)
}
