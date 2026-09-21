package greptool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/pulseaiclub/phi/internal/tools/tooldef"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/util"
)

var (
	rgPath     string
	rgPathOnce sync.Once
	rgPathErr  error
)

const (
	grepDefaultLimit    = 100
	grepDefaultMaxBytes = 50 * 1024
	grepMaxLineRunes    = 500
	grepTruncatedSuffix = "... [truncated]"
	// A --json event embeds the whole matched line, so a single minified bundle
	// or sourcemap line can yield a multi-megabyte event. Events above this cap
	// are skipped, never fatal.
	grepMaxEventBytes = 2 << 20
)

// errOversizedEvent reports a ripgrep event larger than grepMaxEventBytes.
var errOversizedEvent = errors.New("ripgrep event exceeds size cap")

var grepDescription = fmt.Sprintf(
	`Search file contents by regex or literal text and return matching lines as LINE#HASH anchors.

Each matched file is preceded by an @file path#TAG header (4 hex chars for edit.hash).
Use the glob parameter to limit files (e.g. *_test.go); that is not the find tool.
Results are capped at %d matches and %dKB; increase limit or refine the pattern if truncated.
Use read for full untruncated line text. Prefer this over bash grep/rg.`,
	grepDefaultLimit,
	grepDefaultMaxBytes/1024,
)

// GrepTool returns the grep (search) tool definition + handler.
func GrepTool() tooldef.Tool {
	return tooldef.Tool{
		Definition: llm.ToolDefinition{
			Name:        "grep",
			Description: grepDescription,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"pattern": llm.Object{
						"type":        "string",
						"description": "Regex or literal string to search. Example: func Test.*",
					},
					"path": llm.Object{
						"type":        "string",
						"description": "Directory or file to search. Example: ./src",
					},
					"glob": llm.Object{
						"type":        "string",
						"description": "File pattern filter. Example: *_test.go",
					},
					"include": llm.Object{
						"type":        "string",
						"description": "Deprecated alias for glob; prefer glob.",
					},
					"ignoreCase": llm.Object{
						"type":        "boolean",
						"description": "true for case-insensitive search (default: false)",
					},
					"literal": llm.Object{
						"type":        "boolean",
						"description": "true to treat pattern as literal text (default: false)",
					},
					"context": llm.Object{
						"type":        "integer",
						"description": "Lines of context around each match. Example: 2",
					},
					"limit": llm.Object{
						"type": "integer",
						"description": fmt.Sprintf(
							"Maximum matches to return. Example: 50 (default: %d)",
							grepDefaultLimit,
						),
					},
				},
				Required: []string{"pattern"},
			},
			Readable: true,
		},
		DetailFromArgs: func(input json.RawMessage) string {
			var in grepInput
			_ = json.Unmarshal(input, &in)
			pat := strings.TrimSpace(in.Pattern)
			p := strings.TrimSpace(in.Path)
			if p == "" {
				p = "."
			}
			if pat != "" {
				return fmt.Sprintf("grep %q in %s", pat, p)
			}
			return "grep"
		},
		Run: runGrep,
	}
}

type grepInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Glob       string `json:"glob,omitempty"`
	Include    string `json:"include,omitempty"`
	IgnoreCase bool   `json:"ignoreCase,omitempty"`
	Literal    bool   `json:"literal,omitempty"`
	Context    int    `json:"context,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type rgJSONEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

type grepMatch struct {
	filePath   string
	lineNumber int
}

func runGrep(ctx context.Context, input json.RawMessage) (tooldef.Result, error) {
	var in grepInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tooldef.Result{}, fmt.Errorf("failed to parse grep arguments: %w", err)
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return tooldef.Result{}, errors.New("pattern is required: provide a regex or literal search string")
	}

	// Resolve ripgrep binary.
	rgPathLocal, err := resolveRipgrepPath()
	if err != nil {
		return tooldef.Result{}, err
	}

	// Resolve search path.
	searchRel := in.Path
	if searchRel == "" {
		searchRel = "."
	}
	// rg --json echoes the search path as given. Always pass absolute so match
	// paths are absolute and ReadFile works regardless of process cwd.
	searchPath, err := tooldef.ResolveToCwd(ctx, searchRel)
	if err != nil {
		return tooldef.Result{}, err
	}

	if _, err := os.Stat(searchPath); err != nil {
		return tooldef.Result{}, fmt.Errorf("path not found: %s. Check the path and try again", searchPath)
	}

	contextN := in.Context
	contextN = max(contextN, 0)
	effectiveLimit := in.Limit
	if effectiveLimit < 1 {
		effectiveLimit = grepDefaultLimit
	}

	glob := in.Glob
	if glob == "" {
		glob = in.Include
	}

	// Build ripgrep args.
	args := []string{"--json", "--line-number", "--color=never", "--hidden"}
	if in.IgnoreCase {
		args = append(args, "--ignore-case")
	}
	if in.Literal {
		args = append(args, "--fixed-strings")
	}
	if glob != "" {
		args = append(args, "--glob", glob)
	}
	args = append(args, "--", in.Pattern, searchPath)

	cmd := exec.CommandContext(ctx, rgPathLocal, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return tooldef.Result{}, fmt.Errorf("ripgrep stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return tooldef.Result{}, fmt.Errorf("ripgrep stderr: %w", err)
	}
	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stderrBuf, stderr)
	}()

	if err := cmd.Start(); err != nil {
		return tooldef.Result{}, fmt.Errorf("failed to run ripgrep: %w", err)
	}

	reader := bufio.NewReaderSize(stdout, 64*1024)

	var matches []grepMatch
	matchCount := 0
	oversizedEvents := 0
	matchLimitReached := false
	killedForLimit := false

	for {
		if ctx.Err() != nil {
			stopRipgrep(cmd, stdout)
			return tooldef.Result{}, ctx.Err()
		}
		line, err := readEvent(reader, grepMaxEventBytes)
		if errors.Is(err, errOversizedEvent) {
			oversizedEvents++
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			stopRipgrep(cmd, stdout)
			return tooldef.Result{}, fmt.Errorf("reading ripgrep output: %w", err)
		}
		var ev rgJSONEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "match" {
			continue
		}
		fp := ev.Data.Path.Text
		ln := ev.Data.LineNumber
		if fp == "" || ln < 1 {
			continue
		}
		matchCount++
		matches = append(matches, grepMatch{filePath: fp, lineNumber: ln})
		if matchCount >= effectiveLimit {
			matchLimitReached = true
			killedForLimit = true
			stopRipgrep(cmd, stdout)
			break
		}
	}

	// Reap ripgrep. Paths that killed it early already waited inside stopRipgrep.
	var waitErr error
	if !killedForLimit {
		waitErr = cmd.Wait()
	}
	<-stderrDone
	if ctx.Err() != nil {
		return tooldef.Result{}, ctx.Err()
	}
	if !killedForLimit && waitErr != nil {
		code := exitCode(waitErr)
		if code != 0 && code != 1 {
			msg := strings.TrimSpace(stderrBuf.String())
			if msg == "" {
				msg = fmt.Sprintf("ripgrep exited with code %d", code)
			}
			return tooldef.Result{}, errors.New(msg)
		}
	}

	if matchCount == 0 {
		content := "No matches found"
		if oversizedEvents > 0 {
			content = fmt.Sprintf(
				"No matches found: %d match lines exceeded %s and were skipped. "+
					"Narrow the search with path or glob",
				oversizedEvents,
				formatBytes(grepMaxEventBytes),
			)
		}
		return tooldef.Result{Content: content, Detail: "0 matches", Output: content}, nil
	}

	// Read matched files to produce output.
	fileCache := make(map[string][]string)
	fileTag := make(map[string]string)
	getFileLines := func(abs string) []string {
		if cached, ok := fileCache[abs]; ok {
			return cached
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			fileCache[abs] = nil
			return nil
		}
		text := util.NormalizeLF(string(b))
		lines := strings.Split(text, "\n")
		fileCache[abs] = lines
		fileTag[abs] = util.ComputeFileHash(text)
		return lines
	}

	formatPath := func(filePath string) string {
		return tooldef.RelToCwd(ctx, filePath)
	}

	var out []string
	linesTruncated := false
	lastAbs := ""
	for _, m := range matches {
		if m.filePath != lastAbs {
			lastAbs = m.filePath
			_ = getFileLines(m.filePath)
			if tag, ok := fileTag[m.filePath]; ok && tag != "" {
				out = append(out, util.FormatFileHeader(formatPath(m.filePath), tag))
			}
		}
		block, lt := formatGrepBlock(formatPath, getFileLines, m.filePath, m.lineNumber, contextN)
		if lt {
			linesTruncated = true
		}
		out = append(out, block...)
	}

	raw := strings.Join(out, "\n")
	truncRes := truncateHead(raw, grepDefaultMaxBytes)
	output := truncRes.Content
	byteTrunc := truncRes.Truncated

	var notices []string
	if matchLimitReached {
		notices = append(notices, fmt.Sprintf(
			"%d matches limit reached. Use limit=%d for more, or refine pattern",
			effectiveLimit, effectiveLimit*2,
		))
	}
	if byteTrunc {
		notices = append(notices, formatBytes(grepDefaultMaxBytes)+" limit reached")
	}
	if linesTruncated {
		notices = append(notices, fmt.Sprintf(
			"Some lines truncated to %d chars. Use read to see full lines",
			grepMaxLineRunes,
		))
	}
	if oversizedEvents > 0 {
		notices = append(notices, fmt.Sprintf(
			"%d match lines exceeded %s and were skipped. Narrow the search with path or glob",
			oversizedEvents,
			formatBytes(grepMaxEventBytes),
		))
	}
	if len(notices) > 0 {
		output += "\n\n[" + strings.Join(notices, ". ") + "]"
	}

	detail := fmt.Sprintf("%d matches", matchCount)
	return tooldef.Result{Content: output, Detail: detail, Output: output}, nil
}

// readEvent reads one newline-terminated ripgrep JSON event, buffering at most
// maxBytes of it. A larger event is consumed but reported as errOversizedEvent.
//
// Consuming it is the point: a reader that stops mid-stream leaves ripgrep
// blocked writing into a full pipe, and cmd.Wait never returns.
func readEvent(r *bufio.Reader, maxBytes int) ([]byte, error) {
	buf := make([]byte, 0, 1024)
	oversized := false
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 {
			if oversized || len(buf)+len(chunk) > maxBytes {
				oversized = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case err == nil:
			if oversized {
				return nil, errOversizedEvent
			}
			return buf, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && !oversized && len(buf) > 0:
			// Final event without a trailing newline; report EOF on the next read.
			return buf, nil
		default:
			return nil, err
		}
	}
}

// stopRipgrep kills ripgrep, drains stdout and reaps it. The drain is required:
// skipping it leaves Wait blocked on a pipe no one is reading.
func stopRipgrep(cmd *exec.Cmd, stdout io.Reader) {
	_ = cmd.Process.Kill()
	_, _ = io.Copy(io.Discard, stdout)
	_ = cmd.Wait()
}

func resolveRipgrepPath() (string, error) {
	rgPathOnce.Do(func() {
		p, err := project.GetDefaultProject().Global().LookBin("rg")
		if err != nil {
			rgPathErr = fmt.Errorf("ripgrep (rg) is not available: %w", err)
			return
		}
		rgPath = p
	})
	return rgPath, rgPathErr
}

func formatGrepBlock(
	formatPath func(string) string,
	getLines func(string) []string,
	absPath string,
	lineNumber, contextN int,
) (lines []string, anyLineTruncated bool) {
	rel := formatPath(absPath)
	fileLines := getLines(absPath)
	if len(fileLines) == 0 {
		return []string{fmt.Sprintf("%s:%d: (unable to read file)", rel, lineNumber)}, false
	}

	start, end := lineNumber, lineNumber
	if contextN > 0 {
		start = max(1, lineNumber-contextN)
		end = min(len(fileLines), lineNumber+contextN)
	}

	numLines := end - start + 1
	lines = make([]string, 0, numLines)

	for cur := start; cur <= end; cur++ {
		var lineText string
		if cur >= 1 && cur <= len(fileLines) {
			lineText = fileLines[cur-1]
		}
		lineText = util.ReplaceAll(lineText, "\r", "")
		h := util.ComputeLineHash(lineText)
		ref := fmt.Sprintf("%d#%s", cur, h)
		truncLine, wasTrunc := truncateLine(lineText, grepMaxLineRunes)
		if wasTrunc {
			anyLineTruncated = true
		}
		var prefix string
		if cur == lineNumber {
			prefix = ">>"
		} else {
			prefix = "  "
		}
		lines = append(lines, fmt.Sprintf("%s:%s%s|%s", rel, prefix, ref, truncLine))
	}
	return lines, anyLineTruncated
}

func truncateLine(line string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = grepMaxLineRunes
	}
	runes := []rune(line)
	if len(runes) <= maxChars {
		return line, false
	}
	return string(runes[:maxChars]) + grepTruncatedSuffix, true
}

type truncResult struct {
	Content   string
	Truncated bool
}

func truncateHead(content string, maxBytes int) truncResult {
	if len(content) <= maxBytes {
		return truncResult{Content: content, Truncated: false}
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return truncResult{Content: content, Truncated: false}
	}
	firstLineLen := len(lines[0])
	if firstLineLen > maxBytes {
		return truncResult{Content: "", Truncated: true}
	}
	var out []string
	byteCount := 0
	for i, line := range lines {
		lineLen := len(line)
		if i > 0 {
			lineLen++ // account for newline
		}
		if byteCount+lineLen > maxBytes {
			break
		}
		out = append(out, line)
		byteCount += lineLen
	}
	if len(out) < len(lines) {
		return truncResult{Content: strings.Join(out, "\n"), Truncated: true}
	}
	return truncResult{Content: content, Truncated: false}
}

func formatBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	return fmt.Sprintf("%dKB", n/1024)
}

func exitCode(err error) int {
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return -1
}
