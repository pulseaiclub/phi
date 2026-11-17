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
	"runtime"
	"strconv"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/util/filesearch"
)

const (
	defaultFindLimit = 100
	// maxFindLimit bounds a page so the values derived from it stay in range:
	// fd is asked for limit+1, and the truncation hint prints limit*2. Without
	// the bound, a huge limit from the schema overflows into a negative
	// --max-results, which fd rejects.
	maxFindLimit = 10_000
)

var findDescription = fmt.Sprintf(
	`Find files matching a glob pattern and return cwd-relative paths.

Uses fd (respects .gitignore). Use path to restrict the search directory.
Supports * / ** / ? / [abc] / {a,b}. Returns at most %d results; a larger limit is capped at %d.
Prefer this over bash find/ls for filename search.`,
	defaultFindLimit,
	maxFindLimit,
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
							"Maximum results to return; capped at %d. Example: 50 (default: %d)",
							maxFindLimit,
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
		tooldef.WithPermission(tooldef.Declaration[findInput]{
			Kind: permission.KindRead,
			// An omitted path searches cwd, which is the gate's business too.
			Paths: func(in findInput) ([]string, error) {
				if strings.TrimSpace(in.Path) == "" {
					return []string{"."}, nil
				}
				return []string{in.Path}, nil
			},
		}),
	)
}

// findDetail is the row's detail only: the UI already paints the tool name, so
// repeating it here renders as "find find ...".
func findDetail(in findInput) string {
	pat := strings.TrimSpace(in.Pattern)
	p := strings.TrimSpace(in.Path)
	if p == "" {
		p = "."
	}
	if pat == "" {
		return p
	}
	return fmt.Sprintf("%q in %s", pat, p)
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

	limit := normalizeFindLimit(in.Limit)

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

// normalizeFindLimit folds a missing limit into the default and caps an
// oversized one, so every derived value (fd's limit+1, the hint's limit*2)
// stays positive.
func normalizeFindLimit(limit int) int {
	if limit <= 0 {
		return defaultFindLimit
	}
	if limit > maxFindLimit {
		return maxFindLimit
	}
	return limit
}

// fdArgs builds fd's command line for one page. The limit is normalized here as
// well, so no caller can reach fd with the negative --max-results that a
// limit+1 overflow would produce.
func fdArgs(pattern, absSearchRoot string, limit int) ([]string, error) {
	limit = normalizeFindLimit(limit)
	args := []string{
		"--color=never",
		"--hidden",
		"--no-require-git",
		"--type", "f",
		"--ignore-case",
		// Ask for one more than the page: exactly limit results means there
		// is nothing more to show, and only the extra result proves there is.
		"--max-results", strconv.Itoa(limit + 1),
	}

	// On Windows, fd's --glob matching is broken (globset matches backslash
	// paths against slash-only pattern semantics), so we translate the glob
	// to a regex. On other platforms, --glob works correctly and is simpler.
	if runtime.GOOS == "windows" {
		var err error
		args, err = appendWindowsPattern(args, pattern)
		if err != nil {
			return nil, err
		}
	} else {
		args = appendGlobPattern(args, pattern)
	}
	return append(args, absSearchRoot), nil
}

func runFD(ctx context.Context, pattern, absSearchRoot string, limit int) ([]string, bool, error) {
	bin, err := filesearch.ResolveFD()
	if err != nil {
		return nil, false, err
	}

	args, err := fdArgs(pattern, absSearchRoot, limit)
	if err != nil {
		return nil, false, err
	}
	// parseFDOutput decides truncation against this same cap.
	limit = normalizeFindLimit(limit)

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	out := strings.TrimSpace(stdout.String())
	if err != nil {
		if runErr := fdRunError(pattern, out, strings.TrimSpace(stderr.String())); runErr != nil {
			return nil, false, runErr
		}
	}

	if out == "" {
		return nil, false, nil
	}

	files, truncated := parseFDOutput(out, absSearchRoot, limit)
	return files, truncated, nil
}

// parseFDOutput turns fd's newline-separated output into absolute, cleaned
// paths. It keeps at most limit and reports truncation only when fd returned
// more than the page, so a directory holding exactly limit files is a complete
// listing rather than a cut one.
func parseFDOutput(out, searchRoot string, limit int) ([]string, bool) {
	if out == "" {
		return nil, false
	}
	lines := strings.Split(out, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		abs := line
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(searchRoot, abs)
		}
		files = append(files, filepath.Clean(abs))
		if len(files) > limit {
			return files[:limit], true
		}
	}
	return files, false
}

// fdRunError turns a failed fd run into an actionable error, or nil when the run
// simply matched nothing.
//
// fd 10.x exits 0 with empty output when nothing matched and reserves a non-zero
// exit plus stderr for real failures (a rejected pattern, an unreadable root),
// so stderr text must never be read as "no matches": that reports a file as
// absent when the search never ran. An exit with empty stderr stays "no
// matches", which is how older fd and `-q` signal it.
func fdRunError(pattern, out, errMsg string) error {
	if out != "" {
		// Partial output with a non-zero exit: keep what we got.
		return nil
	}
	if errMsg == "" {
		return nil
	}
	if isFDPatternError(errMsg) {
		return fmt.Errorf("invalid glob pattern %q: %s", pattern, errMsg)
	}
	return fmt.Errorf("fd: %s", errMsg)
}

// isFDPatternError reports whether fd blamed the pattern rather than the tree,
// so the caller can say which argument to fix.
func isFDPatternError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "error parsing glob") ||
		strings.Contains(lower, "error parsing regex") ||
		strings.Contains(lower, "path-separation character") ||
		strings.Contains(lower, "invalid glob")
}

// appendGlobPattern appends fd --glob arguments for the pattern (macOS/Linux).
func appendGlobPattern(args []string, pattern string) []string {
	args = append(args, "--glob")
	// Normalize backslash to forward slash (on non-Windows, backslash is
	// treated as an escape character in globs, not a path separator).
	normalized := strings.ReplaceAll(pattern, `\`, "/")
	effective := normalized
	if strings.Contains(normalized, "/") {
		args = append(args, "--full-path")
		if !strings.HasPrefix(normalized, "/") && !strings.HasPrefix(normalized, "**/") {
			effective = "**/" + normalized
		}
	}
	return append(args, "--", effective)
}

// appendWindowsPattern appends fd regex arguments for the pattern (Windows workaround).
//
// Unlike the --glob path, every generated regex contains a path separator
// ([^/\\] / [/\\]), and fd aborts with "contains a path-separation character"
// when a separator appears in a pattern without --full-path. So match the full
// path unconditionally and anchor the pattern with a leading **/ so bare names
// still match at any depth.
func appendWindowsPattern(args []string, pattern string) ([]string, error) {
	// Normalize backslash to forward slash so the regex can match either separator.
	normalized := strings.ReplaceAll(pattern, `\`, "/")
	if !strings.HasPrefix(normalized, "/") && !strings.HasPrefix(normalized, "**/") {
		normalized = "**/" + normalized
	}

	re, err := globToRegex(normalized)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
	}
	return append(args, "--full-path", "--", re), nil
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
