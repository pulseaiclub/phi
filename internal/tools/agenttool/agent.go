package agenttool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/util"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
)

const agentSummaryLimit = 12000 // bytes, keep parent context small

const agentLaunchGuidance = `Launch a specialized sub-agent. Pick a role:

- explore (default): no write/edit; full bash except hard denies. Multi-hop recon / unknown location — keep tool noise out of this context.
- review: same tools as explore; diffs, checks, quality/security report — do not edit.
- worker: read/write/edit + bash; implement a scoped, self-contained change and verify. Not for open-ended exploration.

When NOT to use any sub-agent:
- Exact file path already known — use read yourself
- Exact symbol like "class Foo" — use grep yourself
- Tiny local edit — edit/write yourself

How to use:
1. Use agent_spawn to launch a job, then agent_control (action="wait") for its summary. For parallel jobs, spawn all first, then wait each.
2. Stateless: put a highly detailed, self-contained prompt and say what the final summary must include.
3. You only receive the final summary. Summarize for the user if needed.
4. Sub-agents cannot spawn further agents. Do not put secrets in the prompt.
5. Verify before relying on a worker's edits in follow-up work.`

// agentConfig is the resolved wiring of the agent_* tools. ParentID/WorkDir stay
// callbacks because the session can change under us (/resume).
type agentConfig struct {
	manager  *job.Manager
	parentID func() string
	workDir  func() string
	gate     permission.Gate
}

// AgentOption configures [AgentTools].
type AgentOption func(*agentConfig)

// WithManager wires the process-level job manager. AgentTools returns no tools
// without one, so sub-agents stay unregistered.
func WithManager(manager *job.Manager) AgentOption {
	return func(cfg *agentConfig) { cfg.manager = manager }
}

// WithParentID sets the parent session attributed to spawned jobs.
func WithParentID(parentID func() string) AgentOption {
	return func(cfg *agentConfig) { cfg.parentID = parentID }
}

// WithWorkDir sets the session cwd that relative sub-agent workdirs resolve against.
func WithWorkDir(workDir func() string) AgentOption {
	return func(cfg *agentConfig) { cfg.workDir = workDir }
}

// WithGate sets the trusted parent gate; never accepted from tool arguments.
func WithGate(gate permission.Gate) AgentOption {
	return func(cfg *agentConfig) { cfg.gate = gate }
}

// AgentTools returns agent_spawn and agent_control.
// Depth is forced to 0; the parent id comes from WithParentID, not model args.
func AgentTools(opts ...AgentOption) []tooldef.Tool {
	cfg := agentConfig{
		parentID: func() string { return "" },
		workDir:  func() string { return "" },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.manager == nil {
		return nil
	}
	return []tooldef.Tool{
		agentSpawnTool(cfg),
		agentControlTool(cfg),
	}
}

func agentSpawnTool(cfg agentConfig) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "agent_spawn",
			Description: fmt.Sprintf(agentLaunchGuidance+`

Starts asynchronously and returns job_id immediately. Use agent_control (action="wait") for the summary. Best for parallel jobs.

Concurrency cap: at most %d sub-agents run concurrently; spawning more fails (jobs are not queued).`, cfg.manager.MaxConcurrent()),
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"prompt": llm.Object{
						"type":        "string",
						"description": "Self-contained task. Include context, scope, and exactly what the final summary must return. The sub-agent cannot ask follow-ups.",
					},
					"description": llm.Object{
						"type":        "string",
						"description": "Very short label for the UI / job list (e.g. \"find auth config\").",
					},
					"role": llm.Object{
						"type":        "string",
						"description": "explore (default) | review | worker. See tool description for when to pick each.",
						"enum":        []string{"explore", "review", "worker"},
					},
					"workdir": llm.Object{
						"type":        "string",
						"description": "Working directory for the sub-agent (default: parent session cwd).",
					},
					"timeout_sec": llm.Object{
						"type":        "integer",
						"description": "Optional run timeout in seconds for the job itself (not wait).",
					},
				},
				Required: []string{"prompt"},
			},
		}),
		tooldef.WithDetail(spawnDetail),
		tooldef.WithPermission(tooldef.Declaration[spawnInput]{
			Kind: permission.KindDelegate,
			// The workdir becomes the child's workspace root, so the gate must
			// see it; an omitted workdir resolves to the session cwd.
			Paths: func(in spawnInput) ([]string, error) { return []string{in.WorkDir}, nil },
		}),
		tooldef.WithHandler(func(ctx context.Context, in spawnInput) (tooldef.Result, error) {
			role, err := job.ParseRole(in.Role)
			if err != nil {
				return tooldef.Result{}, err
			}
			wd, err := permission.AbsCleanAt(strings.TrimSpace(in.WorkDir), cfg.workDir())
			if err != nil {
				return tooldef.Result{}, fmt.Errorf("agent_spawn workdir: %w", err)
			}
			req := job.SpawnRequest{
				Prompt:          in.Prompt,
				Description:     in.Description,
				ParentID:        cfg.parentID(),
				ParentToolUseID: tooldef.ToolCallID(ctx),
				Depth:           0,
				Role:            role,
				WorkDir:         wd,
				Gate:            cfg.gate,
			}
			if in.TimeoutSec > 0 {
				req.Timeout = time.Duration(in.TimeoutSec) * time.Second
			}
			info, err := cfg.manager.Spawn(ctx, req)
			if err != nil {
				return tooldef.Result{}, err
			}
			label := spawnDetail(in)
			body := mustJSON(map[string]any{
				"job_id":      info.ID,
				"status":      info.Status,
				"role":        info.Role,
				"label":       label,
				"dir":         info.Dir,
				"result_path": info.ResultPath,
			})
			return tooldef.Result{Content: body, Detail: label, Output: body}, nil
		}),
	)
}

type spawnInput struct {
	Prompt      string `json:"prompt"`
	Description string `json:"description"`
	Role        string `json:"role"`
	WorkDir     string `json:"workdir"`
	TimeoutSec  int    `json:"timeout_sec"`
}

// spawnDetail is the one-line TUI label of a spawn row. The finished row keeps
// this same label, so the title never flips to a raw job id mid-run.
func spawnDetail(in spawnInput) string {
	return roleDetail(in.Role, jobLabel(in.Description, in.Prompt))
}

// jobLabel names a job for humans: the caller's description, else the head of
// the prompt. Whitespace collapses so the label stays on one TUI row.
func jobLabel(description, prompt string) string {
	if d := oneLine(description); d != "" {
		return d
	}
	return util.TruncateRunes(oneLine(prompt), 80, "…")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

type controlInput struct {
	JobID  string `json:"job_id"`
	Action string `json:"action"`
}

func controlDetail(in controlInput) string {
	return in.JobID
}

// roleDetail is the one-line TUI label: "explore: find auth". The role reads as
// a prefix, so the row needs no separate tool name.
func roleDetail(role, rest string) string {
	r := string(job.NormalizeRole(role))
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return r
	}
	return r + ": " + rest
}

func agentControlTool(cfg agentConfig) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "agent_control",
			Description: `Control an existing sub-agent job.

- wait: block until the job reaches a terminal status and return its result.md summary.
- cancel: cancel a running or starting job and wait until it stops.

timeout_sec applies only to wait: it limits how long the call blocks and does NOT cancel the job.`,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"job_id": llm.Object{
						"type":        "string",
						"description": "Job id from agent_spawn.",
					},
					"action": llm.Object{
						"type":        "string",
						"description": "wait for a summary or cancel the job.",
						"enum":        []string{"wait", "cancel"},
					},
					"timeout_sec": llm.Object{
						"type":        "integer",
						"description": "For wait only: max seconds to block (does not cancel the job).",
					},
				},
				Required: []string{"job_id", "action"},
			},
		}),
		tooldef.WithDetail(controlDetail),
		// wait/cancel touch no path: controlling existing work is not a new
		// permission surface.
		tooldef.WithPermission(tooldef.Declaration[controlInput]{
			Kind: permission.KindControl,
		}),
		tooldef.WithRun(func(ctx context.Context, input json.RawMessage) (tooldef.Result, error) {
			var in controlInput
			if err := json.Unmarshal(input, &in); err != nil {
				return tooldef.Result{}, fmt.Errorf("%w: %w", job.ErrInvalid, err)
			}
			switch in.Action {
			case "cancel":
				if err := cfg.manager.HandleCancel(ctx, input); err != nil {
					return tooldef.Result{}, err
				}
				body := mustJSON(map[string]any{"ok": true})
				return tooldef.Result{Content: body, Detail: "stopped", Output: body}, nil
			case "wait":
				// HandleWait preserves polling timeouts without canceling the job.
			default:
				return tooldef.Result{}, fmt.Errorf("%w: action must be wait or cancel", job.ErrInvalid)
			}
			res, err := cfg.manager.HandleWait(ctx, input)
			if err != nil {
				return tooldef.Result{}, err
			}
			summary := util.TruncateBytes(res.Summary, agentSummaryLimit, "\n…(truncated)")
			label := roleDetail(string(res.Info.Role), jobLabel(res.Info.Description, res.Info.Prompt))
			body := mustJSON(map[string]any{
				"job_id":      res.Info.ID,
				"status":      res.Info.Status,
				"role":        res.Info.Role,
				"label":       label,
				"error":       res.Info.Error,
				"result_path": res.Info.ResultPath,
				"summary":     summary,
			})
			return tooldef.Result{
				Content: body,
				Detail:  label,
				Output:  body,
			}, nil
		}),
	)
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
