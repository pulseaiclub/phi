package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/hooks"
	"github.com/pulseaiclub/phi/internal/session"
)

// runOptions holds parsed `phi run` flags.
type runOptions struct {
	prompt       string
	jsonl        bool
	maxRounds    int
	timeout      time.Duration
	session      string
	continueLast bool
	sessionDir   string
	help         bool
}

func runCmd(args []string) int {
	opts, err := parseRunArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi run:", err)
		printRunUsage(os.Stderr)
		return ExitUsage
	}
	if opts.help {
		printRunUsage(os.Stdout)
		return ExitOK
	}
	if strings.TrimSpace(opts.prompt) == "" {
		fmt.Fprintln(os.Stderr, "phi run: prompt is required (-p \"...\")")
		printRunUsage(os.Stderr)
		return ExitUsage
	}
	if opts.continueLast && opts.session != "" {
		fmt.Fprintln(os.Stderr, "phi run: --continue-last and --session are mutually exclusive")
		return ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	bs, err := loadRunBootstrap(ctx, opts.sessionDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi run:", err)
		return ExitUsage
	}

	resumeID, resumePath := "", ""
	if opts.continueLast {
		list, err := session.ListSessions(bs.SessionDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "phi run:", err)
			return ExitError
		}
		if len(list) == 0 {
			fmt.Fprintln(os.Stderr, "phi run: --continue-last found no sessions in", bs.SessionDir)
			return ExitError
		}
		resumePath = list[0].File
	} else if opts.session != "" {
		resumeID = opts.session
	}

	engineOpts := agent.EngineOpts{
		Model: bs.Config.Model(),
		SessionOpts: agent.SessionOpts{
			Cwd:        bs.Cwd,
			SessionDir: bs.SessionDir,
			Persist:    true,
			ResumeID:   resumeID,
			ResumePath: resumePath,
		},
		Gate: bs.Gate,
		// Ask is nil: in headless mode any Ask decision is denied, so no
		// approval UI is ever reachable (Ask≡Deny even if the config mode
		// does not fold Ask).
		Ask:   nil,
		Hooks: loadRunHooks(bs),
	}
	if bs.Config.Agents.Enabled {
		hooksMgr := engineOpts.Hooks
		jobs, jobErr := agent.NewJobManager(bs.Proj.JobsDir(), bs.Config.Model(), nil, func() *hooks.Manager {
			return hooksMgr
		})
		if jobErr != nil {
			fmt.Fprintln(os.Stderr, "phi run:", jobErr)
			return ExitUsage
		}
		defer func() { _ = jobs.Close(context.Background()) }()
		engineOpts.Jobs = jobs
	}

	engine, err := agent.NewEngine(engineOpts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi run:", err)
		return ExitUsage
	}
	if opts.maxRounds > 0 {
		if err := engine.SetMaxRounds(opts.maxRounds); err != nil {
			fmt.Fprintln(os.Stderr, "phi run:", err)
			return ExitUsage
		}
	}

	fmt.Fprintf(os.Stderr, "session: %s\n", engine.SessionID())
	if f := engine.SessionFile(); f != "" {
		fmt.Fprintf(os.Stderr, "file: %s\n", f)
	}

	runCtx := ctx
	if opts.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}

	return runLoop(runCtx, engine, opts)
}

// loadRunHooks discovers user + project hooks for headless `phi run`.
// Failures are non-fatal (fail-open). Warnings go to debuglog and a one-line stderr hint.
func loadRunHooks(bs *runBootstrap) *hooks.Manager {
	if bs == nil || bs.Proj == nil {
		return nil
	}
	mgr, warns, err := hooks.LoadForCwd(bs.Proj.Global().HooksDir(), bs.Cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: hooks:", err)
		return nil
	}
	hooks.LogWarnings(warns)
	if summary := hooks.FormatWarningsSummary(warns); summary != "" {
		fmt.Fprintln(os.Stderr, summary)
	}
	return mgr
}

// runLoop consumes the same engine.Loop the TUI uses — no second loop is
// implemented here — and maps events to stdout/stderr + exit codes.
func runLoop(ctx context.Context, engine *agent.Engine, opts runOptions) int {
	enc := &jsonlEncoder{out: os.Stdout, enabled: opts.jsonl}

	exit := ExitOK
	finalText := ""

	for ev, err := range engine.Loop(ctx, opts.prompt, agent.LoopOpts{}) {
		if err != nil {
			exit = classifyRunError(err)
			enc.errorEvent(err.Error())
			fmt.Fprintln(os.Stderr, "error:", err)
			break
		}
		if ev == nil {
			continue
		}
		enc.event(ev)
		if !opts.jsonl {
			switch e := ev.(type) {
			case session.AssistantMessageUpdate:
				if e.Message.State == session.StateComplete {
					finalText = e.Message.FlatText()
				}
			case session.ToolData:
				r := e.Run
				fmt.Fprintf(os.Stderr, "tool: %s [%s] %s\n", r.Name, r.Status, truncate(r.Detail, 100))
				if r.Error != "" {
					fmt.Fprintln(os.Stderr, "  ", truncate(r.Error, 200))
				}
			}
		}
	}

	if ctxErr := ctx.Err(); ctxErr != nil && exit == ExitOK {
		exit = ExitError // context cancellation is not success
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			enc.errorEvent(ctxErr.Error())
			fmt.Fprintln(os.Stderr, "error:", ctxErr)
		}
	}

	if !opts.jsonl && exit == ExitOK && strings.TrimSpace(finalText) != "" {
		fmt.Fprintln(os.Stdout, finalText)
	}

	enc.doneEvent(engine.SessionID(), engine.SessionFile(), exit)
	return exit
}

// --- flag parsing ---------------------------------------------------------

func parseRunArgs(args []string) (runOptions, error) {
	var o runOptions
	i := 0
	next := func(name string) (string, error) {
		i++
		if i >= len(args) {
			return "", fmt.Errorf("%s requires a value", name)
		}
		return args[i], nil
	}
	for ; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			o.help = true
		case arg == "--jsonl":
			o.jsonl = true
		case arg == "--continue-last":
			o.continueLast = true
		case arg == "-p" || arg == "--prompt":
			v, err := next(arg)
			if err != nil {
				return o, err
			}
			o.prompt = v
		case strings.HasPrefix(arg, "--prompt="):
			o.prompt = strings.TrimPrefix(arg, "--prompt=")
		case strings.HasPrefix(arg, "-p="):
			o.prompt = strings.TrimPrefix(arg, "-p=")
		case arg == "--max-rounds":
			v, err := next(arg)
			if err != nil {
				return o, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return o, fmt.Errorf("--max-rounds must be a positive integer, got %q", v)
			}
			o.maxRounds = n
		case strings.HasPrefix(arg, "--max-rounds="):
			v := strings.TrimPrefix(arg, "--max-rounds=")
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return o, fmt.Errorf("--max-rounds must be a positive integer, got %q", v)
			}
			o.maxRounds = n
		case arg == "--timeout":
			v, err := next(arg)
			if err != nil {
				return o, err
			}
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return o, fmt.Errorf("--timeout must be a positive duration, got %q", v)
			}
			o.timeout = d
		case strings.HasPrefix(arg, "--timeout="):
			v := strings.TrimPrefix(arg, "--timeout=")
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return o, fmt.Errorf("--timeout must be a positive duration, got %q", v)
			}
			o.timeout = d
		case arg == "--session":
			v, err := next(arg)
			if err != nil {
				return o, err
			}
			o.session = v
		case strings.HasPrefix(arg, "--session="):
			o.session = strings.TrimPrefix(arg, "--session=")
		case arg == "--session-dir":
			v, err := next(arg)
			if err != nil {
				return o, err
			}
			o.sessionDir = v
		case strings.HasPrefix(arg, "--session-dir="):
			o.sessionDir = strings.TrimPrefix(arg, "--session-dir=")
		default:
			return o, fmt.Errorf("unknown flag %q", arg)
		}
	}
	return o, nil
}

func printRunUsage(w *os.File) {
	fmt.Fprintf(w, `usage: phi run -p "PROMPT" [flags]

Run one agent loop headlessly and exit. Human logs go to stderr; with
--jsonl, machine-readable events go to stdout (one JSON object per line).

flags:
  -p, --prompt STRING   prompt to run (required)
      --jsonl           emit JSONL events to stdout
      --max-rounds N    cap tool rounds (default 64)
      --timeout DURATION stop after a wall-clock duration (e.g. 10m; default unlimited)
      --session ID      resume a persisted session by id or unique prefix
      --continue-last   resume the newest persisted session for this directory
      --session-dir DIR override the session storage directory
  -h, --help            show this help

exit codes:
  0 success   1 runtime/LLM error   2 max rounds   3 config/usage
`)
}

// --- JSONL event schema ---------------------------------------------------

// jsonlEncoder writes the pinned event schema to a writer. Fields are
// explicit so the wire format never depends on Go struct tags of internal
// session types and never carries API keys or other config secrets.
type jsonlEncoder struct {
	out     io.Writer
	enabled bool
}

func (enc *jsonlEncoder) emit(v any) {
	if enc == nil || !enc.enabled {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(enc.out, `{"type":"error","message":%q}`+"\n", "encode event: "+err.Error())
		return
	}
	enc.out.Write(data)
	enc.out.Write([]byte("\n"))
}

type jsonlAssistant struct {
	Type     string      `json:"type"` // "assistant"
	ID       string      `json:"id"`
	State    string      `json:"state"` // streaming | complete | cancelled | error
	Reason   string      `json:"reason,omitempty"`
	Text     string      `json:"text"`
	Thinking string      `json:"thinking,omitempty"`
	Usage    *jsonlUsage `json:"usage,omitempty"`
}

type jsonlUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

type jsonlTool struct {
	Type      string `json:"type"` // "tool"
	ToolUseID string `json:"toolUseId"`
	ToolName  string `json:"toolName,omitempty"`
	Status    string `json:"status"` // in-progress | done | error | cancelled | rejected
	Detail    string `json:"detail,omitempty"`
	Output    string `json:"output,omitempty"`
}

type jsonlCompaction struct {
	Type   string `json:"type"` // "compaction"
	Phase  string `json:"phase"` // started | complete
	Failed bool   `json:"failed,omitempty"`
}

type jsonlError struct {
	Type    string `json:"type"` // "error"
	Message string `json:"message"`
}

type jsonlDone struct {
	Type      string `json:"type"` // "done"
	SessionID string `json:"sessionId,omitempty"`
	File      string `json:"file,omitempty"`
	ExitCode  int    `json:"exitCode"`
}

func (enc *jsonlEncoder) event(ev session.Event) {
	switch e := ev.(type) {
	case session.AssistantMessageUpdate:
		m := e.Message
		var usage *jsonlUsage
		if m.Usage.Reported() {
			usage = &jsonlUsage{
				Prompt:     m.Usage.PromptTokens,
				Completion: m.Usage.CompletionTokens,
				Total:      m.Usage.TotalTokens,
			}
		}
		enc.emit(jsonlAssistant{
			Type:     "assistant",
			ID:       m.ID,
			State:    m.State.String(),
			Reason:   reasonString(m.StopReason),
			Text:     assistantText(m),
			Thinking: thinkingText(m.Content),
			Usage:    usage,
		})
	case session.ToolData:
		r := e.Run
		enc.emit(jsonlTool{
			Type:      "tool",
			ToolUseID: r.ToolUseID,
			ToolName:  r.Name,
			Status:    r.Status.String(),
			Detail:    r.Detail,
			Output:    r.Output,
		})
	case session.CompactionStarted:
		enc.emit(jsonlCompaction{Type: "compaction", Phase: "started"})
	case session.CompactionComplete:
		enc.emit(jsonlCompaction{Type: "compaction", Phase: "complete", Failed: e.Failed})
	}
}

func (enc *jsonlEncoder) errorEvent(message string) {
	enc.emit(jsonlError{Type: "error", Message: message})
}

func (enc *jsonlEncoder) doneEvent(sessionID, file string, exit int) {
	enc.emit(jsonlDone{Type: "done", SessionID: sessionID, File: file, ExitCode: exit})
}

func reasonString(r session.StopReason) string {
	switch r {
	case session.StopEndTurn:
		return "end_turn"
	case session.StopToolUse:
		return "tool_use"
	case session.StopMaxTokens:
		return "max_tokens"
	default:
		return ""
	}
}

func thinkingText(blocks []session.ContentBlock) string {
	var out strings.Builder
	for _, b := range blocks {
		if b.Type == session.BlockThinking {
			out.WriteString(b.Text)
		}
	}
	return out.String()
}

// assistantText joins the text blocks of an assistant event. It does not use
// Message.FlatText because raw events may carry the zero Role (RoleUser),
// which would make FlatText fall back to m.Text.
func assistantText(m session.Message) string {
	var out strings.Builder
	for _, b := range m.Content {
		if b.Type == session.BlockText {
			out.WriteString(b.Text)
		}
	}
	if out.Len() == 0 {
		return m.Text
	}
	return out.String()
}

// classifyRunError maps a loop error to the exit-code contract:
// max rounds → 2, anything else → 1.
func classifyRunError(err error) int {
	if errors.Is(err, agent.ErrMaxRounds) {
		return ExitMaxRounds
	}
	return ExitError
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
