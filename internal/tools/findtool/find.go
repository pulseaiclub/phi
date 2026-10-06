// Package findtool provides the find tool (fd-backed filename search).
package findtool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pulseaiclub/phi/internal/tools/tooldef"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util/filesearch"
)

const defaultFindLimit = 100

var findDescription = fmt.Sprintf(
	`Find files matching a glob pattern and return cwd-relative paths.

Uses fd (respects .gitignore). Use path to restrict the search directory.
Supports * / ** / ? / [abc] / {a,b}. Returns at most %d results.
Prefer this over bash find/ls for filename search.`,
	defaultFindLimit,
)

// FindTool returns the find tool definition + handler.
func FindTool() tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name:        "find",
			Description: findDescription,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"path": llm.Object{
						"type":        "string",
						"description": "Directory to search. Example: ./src",
					},
					"pattern": llm.Object{
						"type":        "string",
						"description": "Glob pattern. Example: **/*.go",
					},
					"limit": llm.Object{
						"type": "integer",
						"description": fmt.Sprintf(
							"Maximum results to return. Example: 50 (default: %d)",
							defaultFindLimit,
						),
					},
				},
				Required: []string{"pattern"},
			},
			Readable: true,
		}),
		tooldef.WithDetail(findDetail),
		tooldef.WithHandler(runFind),
	)
}

func findDetail(in findInput) string {
	pat := strings.TrimSpace(in.Pattern)
	p := strings.TrimSpace(in.Path)
	if p == "" {
		p = "."
	}
	if pat != "" {
		return fmt.Sprintf("find %q in %s", pat, p)
	}
	return "find"
}

type findInput struct {
	Path    string `json:"path,omitempty"`
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit,omitempty"`
}

func runFind(ctx context.Context, in findInput) (tooldef.Result, error) {
	pattern := strings.TrimSpace(in.Pattern)
	if pattern == "" {
		return tooldef.Result{}, errors.New("pattern is required: provide a glob such as *.go or **/*.md")
	}

	searchPath := strings.TrimSpace(in.Path)
	if searchPath == "" {
		searchPath = "."
	}
	absPath, err := tooldef.ResolveToCwd(ctx, searchPath)
	if err != nil {
		return tooldef.Result{}, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return tooldef.Result{}, fmt.Errorf("path not found: %s. Provide an existing directory", absPath)
	}
	if !info.IsDir() {
		return tooldef.Result{}, fmt.Errorf("path is not a directory: %s (find expects a directory path)", absPath)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultFindLimit
	}

	files, truncated, err := runFD(ctx, pattern, absPath, limit)
	if err != nil {
		return tooldef.Result{}, err
	}

	for i, p := range files {
		files[i] = tooldef.RelToCwd(ctx, p)
	}
	content := renderFindResult(files, truncated, limit)
	return tooldef.Result{
		Content: content,
		Detail:  fmt.Sprintf("%d files", len(files)),
		Output:  content,
	}, nil
}

func runFD(ctx context.Context, pattern, absSearchRoot string, limit int) ([]string, bool, error) {
	bin, err := filesearch.ResolveFD()
	if err != nil {
		return nil, false, err
	}

	args := []string{
		"--glob",
		"--color=never",
		"--hidden",
		"--no-require-git",
		"--type", "f",
		"--ignore-case",
		"--max-results", strconv.Itoa(limit),
	}

	// fd --glob matches the basename unless --full-path is set; path-containing
	// patterns need a leading **/ so they match under the search root.
	effective := pattern
	if strings.Contains(pattern, "/") {
		args = append(args, "--full-path")
		if !strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "**/") && pattern != "**" {
			effective = "**/" + pattern
		}
	}
	args = append(args, "--", effective, absSearchRoot)

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	out := strings.TrimSpace(stdout.String())
	errMsg := strings.TrimSpace(stderr.String())

	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 1 && out == "" {
			if isFDGlobParseError(errMsg) {
				return nil, false, fmt.Errorf("invalid glob pattern %q: %s", pattern, errMsg)
			}
			// fd exits 1 when there are no matches.
			return nil, false, nil
		}
		if out == "" {
			if errMsg == "" {
				errMsg = err.Error()
			}
			if isFDGlobParseError(errMsg) {
				return nil, false, fmt.Errorf("invalid glob pattern %q: %s", pattern, errMsg)
			}
			return nil, false, fmt.Errorf("fd: %s", errMsg)
		}
		// Partial stdout with non-zero exit: keep what we got.
	}

	if out == "" {
		return nil, false, nil
	}

	lines := strings.Split(out, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		abs := line
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(absSearchRoot, abs)
		}
		files = append(files, filepath.Clean(abs))
		if len(files) >= limit {
			break
		}
	}
	truncated := len(files) >= limit
	return files, truncated, nil
}

func isFDGlobParseError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "error parsing glob") ||
		strings.Contains(lower, "invalid glob")
}

func renderFindResult(files []string, truncated bool, limit int) string {
	if len(files) == 0 {
		return "No files found"
	}
	result := strings.Join(files, "\n")
	if truncated {
		result += fmt.Sprintf(
			"\n(%d results limit reached. Use limit=%d for more, or refine pattern/path.)",
			limit,
			limit*2,
		)
	}
	return result
}
