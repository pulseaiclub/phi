package tooldef

import (
	"context"
	"encoding/json"

	"github.com/pulseaiclub/phi/internal/llm"
)

// Result is what a tool returns to the model / UI.
type Result struct {
	// Content is sent back to the model as the tool message body.
	Content string
	// Detail is a short one-line summary for the TUI tool row.
	Detail string
	// Output is the display body for the TUI (may equal Content).
	Output string
	// Expanded asks the TUI to start the tool row open.
	Expanded bool
}

// Handler runs a tool given raw JSON arguments.
type Handler func(ctx context.Context, input json.RawMessage) (Result, error)

// Tool is a schema + implementation pair.
type Tool struct {
	Definition llm.ToolDefinition
	Run        Handler
	// DetailFromArgs extracts a one-line detail for the UI before execution.
	DetailFromArgs func(input json.RawMessage) string
}

// ToolOption mutates a Tool during construction.
type ToolOption func(tool *Tool)

// NewTool builds a Tool from options.
func NewTool(option ...ToolOption) Tool {
	tool := &Tool{}
	for _, opt := range option {
		opt(tool)
	}
	return *tool
}

// WithDetail registers a typed detail extractor. Args that fail to decode
// surface as the detail line itself.
func WithDetail[T any](call func(input T) string) ToolOption {
	return func(tool *Tool) {
		tool.DetailFromArgs = func(input json.RawMessage) string {
			var in T
			err := json.Unmarshal(input, &in)
			if err != nil {
				return err.Error()
			}
			return call(in)
		}
	}
}

// WithDefinition sets the LLM-facing schema.
func WithDefinition(definition llm.ToolDefinition) ToolOption {
	return func(tool *Tool) {
		tool.Definition = definition
	}
}

// WithHandler adapts a typed handler, decoding raw JSON args into T.
func WithHandler[T any](call func(ctx context.Context, input T) (Result, error)) ToolOption {
	return func(tool *Tool) {
		tool.Run = func(ctx context.Context, input json.RawMessage) (Result, error) {
			var in T
			if err := json.Unmarshal(input, &in); err != nil {
				return Result{}, err
			}
			return call(ctx, in)
		}
	}
}

// WithRun installs a raw handler for tools that decode args themselves.
func WithRun(run Handler) ToolOption {
	return func(tool *Tool) {
		tool.Run = run
	}
}

// Definitions extracts LLM schemas from tools.
func Definitions(tools []Tool) []llm.ToolDefinition {
	out := make([]llm.ToolDefinition, len(tools))
	for i, t := range tools {
		out[i] = t.Definition
	}
	return out
}

// Registry maps tool name → Tool.
type Registry map[string]Tool

// NewRegistry indexes tools by name.
func NewRegistry(tools []Tool) Registry {
	m := make(Registry, len(tools))
	for _, t := range tools {
		m[t.Definition.Name] = t
	}
	return m
}
