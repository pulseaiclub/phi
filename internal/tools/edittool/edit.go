package edittool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pulseaiclub/phi/internal/tools/tooldef"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
	"github.com/pulseaiclub/phi/internal/util"
)

// The example block burns the gap and selection shapes into the model up
// front; prose alone cost a failed round-trip per novel syntax mistake. The
// anti-pattern block does the same for the shapes the model gets wrong most
// often, taken from real payload failures — and edit_test.go runs both blocks
// through the engine, so the description cannot advertise a payload the parser
// rejects or a rule the parser stopped enforcing.
var editDescription = `Edit files with an anchored patch: quote current text under *** SM:FIND, replace it under *** SM:PUT, insert lines under *** SM:AFTER, or rewrite it in place with ⟪old│new⟫. Elide unchanged runs with ….

<ops>
- *** SM:EDIT relative/path.ts opens a file; bare *** SM:EDIT continues it. Repeat for more files: all edits apply atomically. Append " all" to change every match; JSON-quote paths with spaces.
- *** SM:FIND body must match the file exactly once unless " all". Copy exact text and indentation from the latest read output — not from memory, diffs, or summaries. Use the smallest unique anchor; on ambiguity add parent context, never retry the bare line.
- Each action header needs its own *** SM:FIND. *** SM:PUT states the complete final text that replaces the whole FIND match; an empty body deletes it. *** SM:AFTER keeps the match and inserts its body after the last matched line. A FIND body carrying ⟪old│new⟫ selections needs no action header: each selection rewrites old to new in place, with exactly one "│" divider. Omitting an action without selections is an error.
- Anchors address the file as you read it, never as this payload rewrites it: an anchor cannot match text that an earlier operation in the same payload wrote. Plan every anchor against the current file, so earlier edits never shift later anchors.
- Headers stand alone; bodies are raw lines until the next header or EOF, no closing delimiter. Never use diff prefixes (+/-/space) or @@ hunks.
- In FIND, … captures omitted text: a gap with content after it on its line stays on that line; a gap at line end spans lines. In PUT, each … re-emits the next capture in order. A whole-line … with no capture is an error — type those lines out.
- PUT and AFTER indentation is written verbatim. Failure applies nothing: a match failure returns a copy-ready payload to resend verbatim, a syntax error returns error[SMxxx] with the payload line and the fix. For a new file or a whole-file rewrite use write.
</ops>

<example>
Keep skipped lines and rewrite one call — … captures in FIND replay in PUT order:
*** SM:EDIT src/users.ts
*** SM:FIND
function load(…) {
	…
	return old(…);
}
*** SM:PUT
function load(…) {
	…
	return fresh(…);
}

Small in-place rewrites ride inside FIND as ⟪old│new⟫; no action header:
*** SM:EDIT src/app.ts
*** SM:FIND
total := ⟪a + b│sum(a, b)⟫
if ⟪debug│verbose⟫ {
	log.Printf("total=%d", total)
}
</example>

<anti-patterns>
WRONG — the second action has no *** SM:FIND of its own, so the used-up PUT cannot host it.
*** SM:EDIT a.go
*** SM:FIND
x
*** SM:PUT
y
*** SM:AFTER
z
RIGHT — *** SM:PUT states the final text, so fold the inserted lines into it.
*** SM:EDIT a.go
*** SM:FIND
x
*** SM:PUT
y
z

WRONG — a body line that spells a recognized header ends the body there, so the anchor is lost.
*** SM:EDIT doc/tool.md
*** SM:FIND
Paste this example:
*** SM:EDIT b.go
*** SM:PUT
const b = 2
RIGHT — use write for files that document this syntax: a body line spelling a recognized header (*** SM:EDIT with a path, *** SM:FIND, *** SM:PUT, *** SM:AFTER) starts a new section and cannot be quoted.

WRONG — a ⟪⟫ selection needs both sides.
*** SM:EDIT a.go
*** SM:FIND
const A = ⟪1⟫
RIGHT — write ⟪old│new⟫, where ⟪old│⟫ deletes; to state whole lines instead, use a PUT body.
*** SM:EDIT a.go
*** SM:FIND
const A = ⟪1│2⟫
</anti-patterns>

<critical>
1. One anchor per action: an insert that belongs under text this payload writes goes into the *** SM:PUT body.
2. Anchors are copied verbatim from the latest read and match the file as read — never text this payload writes.
3. A failure applies nothing: rebuild the whole payload and resend it, never stack a follow-up edit on a rejected one.
</critical>`

// EditTool returns the edit (sloppy) tool definition + handler.
func EditTool() tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name:        "edit",
			Description: editDescription,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"payload": llm.Object{
						"type": "string",
						"description": "The sloppy edit payload: *** SM:EDIT / *** SM:FIND / *** SM:PUT or *** " +
							"SM:AFTER blocks. Example: *** SM:EDIT src/app.ts\n*** SM:FIND\nconst x = 1;\n" +
							"*** SM:PUT\nconst x = 2;",
					},
				},
				Required: []string{"payload"},
			},
		}),
		tooldef.WithDetail(editDetail),
		tooldef.WithHandler(runEdit),
	)
}

// editInput is the edit tool payload.
type editInput struct {
	Payload string `json:"payload"`
	// Patch and Input are aliases for payload.
	Patch string `json:"patch,omitempty"`
	Input string `json:"input,omitempty"`
}

func (in editInput) text() string {
	for _, candidate := range []string{in.Payload, in.Patch, in.Input} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func editDetail(in editInput) string {
	paths := sloppy.TargetPaths(in.text())
	if len(paths) == 0 {
		return "edit"
	}
	return strings.Join(paths, ", ")
}

// fileRead is one file loaded for editing, with its disk shape remembered so
// the write-back keeps the original conventions.
type fileRead struct {
	path    string // absolute
	display string
	text    string // LF-normalized
	crlf    bool
}

func runEdit(ctx context.Context, in editInput) (tooldef.Result, error) {
	payload := in.text()
	if strings.TrimSpace(payload) == "" {
		return tooldef.Result{}, errors.New(
			"edit requires a payload: start it with *** SM:EDIT relative/path")
	}
	sections, err := sloppy.Parse(payload)
	if err != nil {
		return tooldef.Result{}, applyError{atomicityNotice + "\n" + err.Error()}
	}
	if len(sections) == 0 {
		return tooldef.Result{}, applyError{
			atomicityNotice + "\nmissing file target: start the payload with *** SM:EDIT relative/path",
		}
	}

	files := make(map[string]fileRead, len(sections))
	read := func(path string) (string, error) {
		file, err := readFile(ctx, path)
		if err != nil {
			return "", err
		}
		files[path] = file
		return file.text, nil
	}
	results, err := applySections(sections, read)
	if err != nil {
		return tooldef.Result{}, err
	}

	var body strings.Builder
	displays := make([]string, 0, len(sections))
	for _, section := range sections {
		file := files[section.Path]
		result := results[section.Path]
		displays = append(displays, file.display)

		if result.Content != file.text {
			text := result.Content
			if file.crlf {
				text = strings.ReplaceAll(text, "\n", "\r\n")
			}
			//nolint:gosec // G306: source files should stay world-readable
			if err := os.WriteFile(file.path, []byte(text), 0o644); err != nil {
				return tooldef.Result{}, fmt.Errorf("failed to write file %s: %w", file.display, err)
			}
		}

		if body.Len() > 0 {
			body.WriteString("\n")
		}
		body.WriteString(file.display)
		body.WriteString("\n")
		body.WriteString(util.GenerateFileDiff(file.display, file.text, result.Content, 3))
		for _, note := range result.Notes {
			body.WriteString(note)
			body.WriteString("\n")
		}
	}

	output := strings.TrimRight(body.String(), "\n")
	return tooldef.Result{
		Content: output,
		Detail:  strings.Join(displays, ", "),
		Output:  output,
	}, nil
}

// readFile loads a file for editing, resolved against the tool cwd.
func readFile(ctx context.Context, path string) (fileRead, error) {
	absolute, err := tooldef.ResolveToCwd(ctx, path)
	if err != nil {
		return fileRead{}, err
	}
	raw, err := os.ReadFile(absolute)
	if err != nil {
		return fileRead{}, err
	}
	text := util.NormalizeLF(string(raw))
	return fileRead{
		path:    absolute,
		display: tooldef.RelToCwd(ctx, absolute),
		text:    text,
		crlf:    strings.Contains(string(raw), "\r\n"),
	}, nil
}
