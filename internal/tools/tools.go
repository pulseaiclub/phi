package tools

import (
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/bashtool"
	"github.com/pulseaiclub/phi/internal/tools/edittool"
	"github.com/pulseaiclub/phi/internal/tools/findtool"
	"github.com/pulseaiclub/phi/internal/tools/greptool"
	"github.com/pulseaiclub/phi/internal/tools/lstool"
	"github.com/pulseaiclub/phi/internal/tools/mcptool"
	"github.com/pulseaiclub/phi/internal/tools/readtool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tools/writetool"
)

type (
	// Result re-exports tooldef.Result.
	Result = tooldef.Result
	// Handler re-exports tooldef.Handler.
	Handler = tooldef.Handler
	// Tool re-exports tooldef.Tool.
	Tool = tooldef.Tool
	// Registry re-exports tooldef.Registry.
	Registry = tooldef.Registry
)

// Definitions and the registry helpers are re-exported from tooldef.
var (
	Definitions    = tooldef.Definitions
	NewRegistry    = tooldef.NewRegistry
	WithToolCallID = tooldef.WithToolCallID
	ToolCallID     = tooldef.ToolCallID
	WithCwd        = tooldef.WithCwd
)

type (
	// ShellExecResult re-exports bashtool.ShellExecResult.
	ShellExecResult = bashtool.ShellExecResult
	// ShellExecOptions re-exports bashtool.ShellExecOptions.
	ShellExecOptions = bashtool.ShellExecOptions
	// BashOutputTail re-exports bashtool.BashOutputTail.
	BashOutputTail = bashtool.BashOutputTail
)

// Bash output limits are re-exported from bashtool.
const (
	BashMaxOutputLines = bashtool.BashMaxOutputLines
	BashMaxOutputBytes = bashtool.BashMaxOutputBytes
)

// ExecShell and NewBashOutputTail are re-exported from bashtool.
var (
	ExecShell         = bashtool.ExecShell
	NewBashOutputTail = bashtool.NewBashOutputTail
)

type (
	// AgentDeps re-exports agenttool.AgentDeps.
	AgentDeps = agenttool.AgentDeps
	// AgentResult re-exports agenttool.AgentResult.
	AgentResult = agenttool.AgentResult
)

// AgentTools, ParseAgentResult, and MCPTools are re-exported tool helpers.
var (
	AgentTools       = agenttool.AgentTools
	ParseAgentResult = agenttool.ParseAgentResult
	MCPTools         = mcptool.Tools
)

// DefaultTools returns the built-in agent tool set.
func DefaultTools() []Tool {
	return []Tool{
		readtool.ReadTool(),
		greptool.GrepTool(),
		findtool.FindTool(),
		lstool.LsTool(),
		edittool.EditTool(),
		bashtool.BashTool(),
		writetool.WriteTool(),
	}
}

// ReadonlyTools returns exploration tools without write/edit.
// Bash remains registered; pair with ModeReadonly (and typically
// ChildPolicy) so write/edit stay denied while non-deny bash is allowed.
func ReadonlyTools() []Tool {
	return []Tool{
		bashtool.BashTool(),
		readtool.ReadTool(),
		greptool.GrepTool(),
		lstool.LsTool(),
		findtool.FindTool(),
	}
}
