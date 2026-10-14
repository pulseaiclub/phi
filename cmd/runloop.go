package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/util"
)

// runOptions holds parsed `phi run` flags.
type runOptions struct {
	prompt       string
	jsonl        bool
	yolo         bool
	maxRounds    int
	timeout      time.Duration
	session      string
	continueLast bool
	sessionDir   string
	builtinTools []tools.Tool
}

// runHeadless runs one agent loop and returns an exitError (or nil) for main.
func runHeadless(opts runOptions) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	bs, err := loadRunBootstrap(ctx, opts.sessionDir, opts.yolo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi run:", err)
		return exitCode(ExitUsage)
	}
	if opts.yolo {
		fmt.Fprintln(os.Stderr, "warning: --yolo skips all permission checks for this run")
	}

	resumeID, resumePath := "", ""
	if opts.continueLast {
		list, err := session.ListSessions(bs.SessionDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "phi run:", err)
			return exitCode(ExitError)
		}
		if len(list) == 0 {
			fmt.Fprintln(os.Stderr, "phi run: --continue-last found no sessions in", bs.SessionDir)
			return exitCode(ExitError)
		}
		resumePath = list[0].File
	} else if opts.session != "" {
		resumeID = opts.session
	}

	// Ask stays nil: in headless mode any Ask decision is denied, so no
	// approval UI is ever reachable (Ask≡Deny even if the config mode
	// does not fold Ask).
	extRunner := loadRunExtensions(bs)
	if extRunner != nil {
		defer extRunner.Close()
	}
	engineOpts := []agent.EngineOption{
		agent.WithGate(bs.Gate),
		agent.WithExtensions(extRunner),
		agent.WithTools(opts.builtinTools),
	}
	if opts.maxRounds > 0 {
		engineOpts = append(engineOpts, agent.WithMaxRounds(opts.maxRounds))
	}
	primary := bs.Config.Model()
	engineOpts = append(engineOpts, agent.WithHooks(model.HooksFor(primary.Name)))
	if pool, err := mcp.LoadPool(bs.Proj.MCPConfigFile()); err != nil {
		fmt.Fprintln(os.Stderr, "warning: mcp:", err)
	} else if pool != nil {
		engineOpts = append(engineOpts, agent.WithMCP(pool))
		defer func() { _ = pool.Close() }()
	}
	if bs.Config.Agents.Enabled {
		parent := bs.Config.Model()
		jobs, jobErr := agent.NewJobManager(bs.Proj.JobsDir(), parent, func(role job.Role) llm.ModelConfig {
			m := bs.Config.Agents.Models
			var name string
			switch job.NormalizeRole(string(role)) {
			case job.RoleReview:
				name = m.Review
			case job.RoleWorker:
				name = m.Worker
			default:
				name = m.Explore
			}
			if cfg, ok := bs.Config.FindModel(name); ok {
				return cfg
			}
			return parent
		}, func() *extension.Runner {
			return extRunner
		})
		if jobErr != nil {
			fmt.Fprintln(os.Stderr, "phi run:", jobErr)
			return exitCode(ExitUsage)
		}
		defer func() { _ = jobs.Close(context.Background()) }()
		engineOpts = append(engineOpts, agent.WithJobs(jobs))
	}

	sessionOpts := []agent.SessionOption{
		agent.WithCwd(bs.Cwd),
		agent.WithSessionDir(bs.SessionDir),
		agent.WithPersist(true),
	}
	if resumePath != "" {
		sessionOpts = append(sessionOpts, agent.WithResumePath(resumePath))
	} else if resumeID != "" {
		sessionOpts = append(sessionOpts, agent.WithResumeID(resumeID))
	}
	sess, err := agent.NewSession(sessionOpts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi run:", err)
		return exitCode(ExitUsage)
	}

	engine, err := agent.NewEngine(bs.Config.Model(), sess, engineOpts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi run:", err)
		return exitCode(ExitUsage)
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

	return exitCode(runLoop(runCtx, engine, opts))
}

// loadRunExtensions discovers user + project extensions for headless `phi run`.
// Failures are non-fatal (fail-open). Warnings go to stderr as a one-line hint.
func loadRunExtensions(bs *runBootstrap) *extension.Runner {
	if bs == nil || bs.Proj == nil {
		return nil
	}
	r, warns, err := extension.Load(bs.Proj.Global().ExtensionsDir(), bs.Proj.ExtensionsDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: extensions:", err)
		return nil
	}
	if n := len(warns); n > 0 {
		fmt.Fprintf(os.Stderr, "warning: extensions: %d warning(s) while loading\n", n)
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, "  ", w.String())
		}
	}
	return r
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
				fmt.Fprintf(os.Stderr, "tool: %s [%s] %s\n", r.Name, r.Status, util.TruncateBytes(r.Detail, 100, "…"))
				if r.Error != "" {
					fmt.Fprintln(os.Stderr, "  ", util.TruncateBytes(r.Error, 200, "…"))
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

func selectBuiltinTools(raw string) ([]tools.Tool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("--tools requires at least one built-in tool name")
	}

	requested := make(map[string]struct{})
	for part := range strings.SplitSeq(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, errors.New("--tools contains an empty tool name")
		}
		requested[name] = struct{}{}
	}

	defaults := tools.DefaultTools()
	available := make([]string, 0, len(defaults))
	selected := make([]tools.Tool, 0, len(requested))
	// Keep the default schema order stable regardless of flag order; the map
	// also collapses duplicate names without exposing duplicate definitions.
	for _, tool := range defaults {
		name := tool.Definition.Name
		available = append(available, name)
		if _, ok := requested[name]; ok {
			selected = append(selected, tool)
			delete(requested, name)
		}
	}

	if len(requested) > 0 {
		unknown := make([]string, 0, len(requested))
		for name := range requested {
			unknown = append(unknown, strconv.Quote(name))
		}
		sort.Strings(unknown)
		noun := "tool"
		if len(unknown) > 1 {
			noun = "tools"
		}
		return nil, fmt.Errorf(
			"--tools contains unknown built-in %s %s (available: %s)",
			noun,
			strings.Join(unknown, ", "),
			strings.Join(available, ", "),
		)
	}

	return selected, nil
}

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
	_, _ = enc.out.Write(data)
	_, _ = enc.out.Write([]byte("\n"))
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

// jsonlUsage reports the disjoint buckets: prompt is the input that missed the
// cache, so prompt+cached+cacheWrite is the whole prompt and total covers it
// plus the reply. Without the cache fields a consumer would read prompt against
// total and see a shortfall on every cached turn.
type jsonlUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Cached     int `json:"cached,omitempty"`
	CacheWrite int `json:"cacheWrite,omitempty"`
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
	Type   string `json:"type"`  // "compaction"
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
				Cached:     m.Usage.CachedTokens,
				CacheWrite: m.Usage.CacheWriteTokens,
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
