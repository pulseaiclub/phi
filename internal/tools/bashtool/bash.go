package bashtool

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
)

const (
	bashDefaultTimeout = 300
)

var bashDescription = `Run a shell command and return combined stdout/stderr.

Use for one-off build, test, git, or inspection commands. Large output is
truncated with the retained output written to a temp file.`

// BashTool returns the bash tool definition + handler.
func BashTool() tooldef.Tool {
	return tooldef.Tool{
		Definition: llm.ToolDefinition{
			Name:        "bash",
			Description: bashDescription,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"command": llm.Object{
						"type":        "string",
						"description": "Shell command to run. Example: go test ./...",
					},
					"timeout": llm.Object{
						"type":        "integer",
						"description": "Timeout in seconds, 1-3600. Example: 120 (default: 300).",
					},
				},
				Required: []string{"command"},
			},
		},
		DetailFromArgs: func(input json.RawMessage) string {
			var in bashInput
			_ = json.Unmarshal(input, &in)
			return strings.TrimSpace(in.Command)
		},
		Run: runBash,
	}
}

type bashInput struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func runBash(ctx context.Context, input json.RawMessage) (tooldef.Result, error) {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tooldef.Result{}, fmt.Errorf("failed to parse bash arguments: %w", err)
	}
	cmd := strings.TrimSpace(in.Command)
	if cmd == "" {
		return tooldef.Result{}, fmt.Errorf("empty command")
	}

	timeout := in.Timeout
	if timeout <= 0 {
		timeout = bashDefaultTimeout
	}
	if timeout > 3600 {
		timeout = 3600
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	c, err := buildShellCommand(ctx, cmd)
	if err != nil {
		return tooldef.Result{}, err
	}
	// Bound the shared stdout/stderr collector so runaway output cannot be
	// buffered unboundedly.
	cb := newCappedBuffer(BashMaxCollectBytes)
	c.Stdout = cb
	c.Stderr = cb
	err = c.Run()

	out := formatBashOutput(cb.String(), cb.Truncated())
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}

	content := out
	if err != nil {
		if ctx.Err() != nil {
			content = out + "\n(command canceled or timed out)"
		} else {
			content = fmt.Sprintf("%s\n(exit error: %v)", out, err)
		}
	}
	return tooldef.Result{Content: content, Detail: cmd, Output: content}, nil
}
