package tools

import (
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/bashtool"
	"github.com/pulseaiclub/phi/internal/tools/fetchtool"
	"github.com/pulseaiclub/phi/internal/tools/globtool"
	"github.com/pulseaiclub/phi/internal/tools/greptool"
	"github.com/pulseaiclub/phi/internal/tools/listtool"
	"github.com/pulseaiclub/phi/internal/tools/readtool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tools/writetool"
)

// Core types — re-exported so callers keep importing tools.
type (
	Result   = tooldef.Result
	Handler  = tooldef.Handler
	Tool     = tooldef.Tool
	Registry = tooldef.Registry
)

var (
	Definitions    = tooldef.Definitions
	NewRegistry    = tooldef.NewRegistry
	WithToolCallID = tooldef.WithToolCallID
	ToolCallID     = tooldef.ToolCallID
)

// Bash / shell helpers used by the TUI.
type (
	ShellExecResult  = bashtool.ShellExecResult
	ShellExecOptions = bashtool.ShellExecOptions
	BashOutputTail   = bashtool.BashOutputTail
)

const (
	BashMaxOutputLines = bashtool.BashMaxOutputLines
	BashMaxOutputBytes = bashtool.BashMaxOutputBytes
)

var (
	ExecShell         = bashtool.ExecShell
	NewBashOutputTail = bashtool.NewBashOutputTail
)

// Agent helpers used by the TUI / mapper.
type (
	AgentDeps   = agenttool.AgentDeps
	AgentResult = agenttool.AgentResult
)

var (
	AgentTools       = agenttool.AgentTools
	ParseAgentResult = agenttool.ParseAgentResult
)

// DefaultTools returns the built-in agent tool set.
func DefaultTools() []Tool {
	return []Tool{
		bashtool.BashTool(),
		readtool.ReadTool(),
		writetool.WriteTool(),
		greptool.GrepTool(),
		listtool.ListTool(),
		writetool.EditTool(),
		fetchtool.FetchTool(),
		globtool.GlobTool(),
	}
}

// ReadonlyTools returns exploration tools without write/edit/fetch.
// Bash remains available but should be paired with ModeReadonly so only
// allowlisted commands run (no file mutations via the shell).
func ReadonlyTools() []Tool {
	return []Tool{
		bashtool.BashTool(),
		readtool.ReadTool(),
		greptool.GrepTool(),
		listtool.ListTool(),
		globtool.GlobTool(),
	}
}
