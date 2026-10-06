package readtool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pulseaiclub/phi/internal/tools/tooldef"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

const (
	readDefaultMaxLines = 1000
	readDefaultMaxBytes = 50 * 1024
	// Cap whole-file reads used for @file tags; larger files must be handled outside edit.
	readMaxHashBytes = 8 << 20 // 8 MiB
)

var readDescription = fmt.Sprintf(`Read a file and return its contents with an @file path header.

Pass the file path; use offset (1-based) and limit to paginate. Body lines are
N|content — quote the content in edit payloads, not the N| prefix.
Output body is capped at %d lines and %d KiB per call.`,
	readDefaultMaxLines, readDefaultMaxBytes/1024)

// ReadTool returns the read tool definition + handler.
func ReadTool() tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name:        "read",
			Description: readDescription,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"path": llm.Object{
						"type":        "string",
						"description": "Path to an existing file. Example: src/main.go",
					},
					"offset": llm.Object{
						"type":        "integer",
						"description": "First line to return, 1-based. Example: 11",
					},
					"limit": llm.Object{
						"type":        "integer",
						"description": fmt.Sprintf("Maximum lines to return; capped at %d.", readDefaultMaxLines),
					},
				},
				Required: []string{"path"},
			},
			Readable: true,
		}),
		tooldef.WithDetail(readDetail),
		tooldef.WithHandler(runRead),
	)
}

func readDetail(in readInput) string {
	return strings.TrimSpace(in.Path)
}

type readInput struct {
	Path   string `json:"path"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

func runRead(ctx context.Context, in readInput) (tooldef.Result, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return tooldef.Result{}, errors.New("path is required")
	}
	path, err := tooldef.ResolveToCwd(ctx, path)
	if err != nil {
		return tooldef.Result{}, err
	}

	st, err := os.Stat(path)
	if err != nil {
		return tooldef.Result{}, err
	}
	if st.Size() > readMaxHashBytes {
		return tooldef.Result{}, fmt.Errorf(
			"file %s is %d bytes; refuse to hash files larger than %d bytes for edit anchors",
			path, st.Size(), readMaxHashBytes,
		)
	}

	select {
	case <-ctx.Done():
		return tooldef.Result{}, ctx.Err()
	default:
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return tooldef.Result{}, err
	}
	text := util.NormalizeLF(string(raw))
	display := tooldef.RelToCwd(ctx, path)
	header := "@file " + display

	startLine := in.Offset
	startLine = max(startLine, 1)
	limit := in.Limit
	if limit <= 0 || limit > readDefaultMaxLines {
		limit = readDefaultMaxLines
	}

	lines := strings.Split(text, "\n")
	// Trailing empty split from final newline is fine for line numbering.
	if text == "" {
		out := header + "\n(empty file)"
		return tooldef.Result{Content: out, Detail: display, Output: out}, nil
	}

	var (
		b         strings.Builder
		collected int
		bytesN    int
	)
	b.WriteString(header)
	b.WriteByte('\n')

	for lineNo := startLine; lineNo <= len(lines); lineNo++ {
		select {
		case <-ctx.Done():
			return tooldef.Result{}, ctx.Err()
		default:
		}
		line := lines[lineNo-1]
		if bytesN+len(line)+1 > readDefaultMaxBytes {
			fmt.Fprintf(&b, "\n... truncated at %d bytes. Next offset: %d\n", readDefaultMaxBytes, lineNo)
			break
		}
		fmt.Fprintf(&b, "%d|%s\n", lineNo, line)
		bytesN += len(line) + 1
		collected++
		if collected >= limit {
			if lineNo < len(lines) {
				fmt.Fprintf(&b, "... truncated at %d lines. Next offset: %d\n", limit, lineNo+1)
			}
			break
		}
	}

	out := b.String()
	return tooldef.Result{Content: out, Detail: display, Output: out}, nil
}
