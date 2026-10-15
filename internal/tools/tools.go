package tools

import (
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/bashtool"
	"github.com/pulseaiclub/phi/internal/tools/edittool"
	"github.com/pulseaiclub/phi/internal/tools/findtool"
	"github.com/pulseaiclub/phi/internal/tools/greptool"
	"github.com/pulseaiclub/phi/internal/tools/lstool"
	"github.com/pulseaiclub/phi/internal/tools/readtool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tools/writetool"
)

// DefaultTools returns the built-in agent tool set. mcpPool wires ls/read/write
// to resolve mcp:// URLs; nil keeps them file-only.
func DefaultTools(mcpPool *mcp.Pool) []tooldef.Tool {
	return []tooldef.Tool{
		readtool.ReadTool(mcpPool),
		greptool.GrepTool(),
		findtool.FindTool(),
		lstool.LsTool(mcpPool),
		edittool.EditTool(),
		bashtool.BashTool(),
		writetool.WriteTool(mcpPool),
	}
}

// ReadonlyTools returns exploration tools without write/edit.
// Bash remains registered; pair with ModeReadonly (and typically
// ChildPolicy) so write/edit stay denied while non-deny bash is allowed.
func ReadonlyTools(mcpPool *mcp.Pool) []tooldef.Tool {
	return []tooldef.Tool{
		bashtool.BashTool(),
		readtool.ReadTool(mcpPool),
		greptool.GrepTool(),
		lstool.LsTool(mcpPool),
		findtool.FindTool(),
	}
}
