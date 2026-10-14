package agenttool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
1. Use agent_spawn to launch a job, then agent_wait for its summary. For parallel jobs, spawn all first, then wait each.
2. Stateless: put a highly detailed, self-contained prompt and say what the final summary must include.
3. You only receive the final summary. Summarize for the user if needed.
4. Sub-agents cannot spawn further agents. Do not put secrets in the prompt.
5. Verify before relying on a worker's edits in follow-up work.`

// AgentDeps wires sub-agent tools to a process-level [job.Manager].
// ParentID/WorkDir are read at call time (session may change via /resume).
type AgentDeps struct {
	Manager  *job.Manager
	ParentID func() string
	WorkDir  func() string
}

// AgentTools returns agent_spawn / list / wait / cancel.
// Depth is forced to 0; ParentID comes from ParentID(), not model args.
func AgentTools(deps AgentDeps) []tooldef.Tool {
	if deps.Manager == nil {
		return nil
	}
	if deps.ParentID == nil {
		deps.ParentID = func() string { return "" }
	}
	if deps.WorkDir == nil {
		deps.WorkDir = func() string { return "" }
	}
	return []tooldef.Tool{
		agentSpawnTool(deps),
		agentWaitTool(deps),
		agentCancelTool(deps),
	}
}

func agentSpawnTool(deps AgentDeps) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "agent_spawn",
			Description: fmt.Sprintf(agentLaunchGuidance+`

Starts asynchronously and returns job_id immediately. Use agent_wait for the summary. Best for parallel jobs.

Concurrency cap: at most %d sub-agents run concurrently; spawning more fails (jobs are not queued).`, deps.Manager.MaxConcurrent()),
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
		tooldef.WithHandler(func(ctx context.Context, in spawnInput) (tooldef.Result, error) {
			role, err := job.ParseRole(in.Role)
			if err != nil {
				return tooldef.Result{}, err
			}
			wd := strings.TrimSpace(in.WorkDir)
			if wd == "" {
				wd = deps.WorkDir()
			}
			req := job.SpawnRequest{
				Prompt:          in.Prompt,
				Description:     in.Description,
				ParentID:        deps.ParentID(),
				ParentToolUseID: tooldef.ToolCallID(ctx),
				Depth:           0,
				Role:            role,
				WorkDir:         wd,
			}
			if in.TimeoutSec > 0 {
				req.Timeout = time.Duration(in.TimeoutSec) * time.Second
			}
			info, err := deps.Manager.Spawn(ctx, req)
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

// jobIDInput is the shared {job_id} payload of agent_wait and agent_cancel.
type jobIDInput struct {
	JobID string `json:"job_id"`
}

func jobIDDetail(in jobIDInput) string {
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

func agentWaitTool(deps AgentDeps) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "agent_wait",
			Description: `Block until a sub-agent job reaches a terminal status and return its result.md summary.

timeout_sec only limits how long this wait blocks — it does NOT cancel the job.
Use agent_cancel to stop a running job.`,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"job_id": llm.Object{
						"type":        "string",
						"description": "Job id from agent_spawn.",
					},
					"timeout_sec": llm.Object{
						"type":        "integer",
						"description": "Max seconds to wait (does not cancel the job).",
					},
				},
				Required: []string{"job_id"},
			},
		}),
		tooldef.WithDetail(jobIDDetail),
		tooldef.WithRun(func(ctx context.Context, input json.RawMessage) (tooldef.Result, error) {
			res, err := deps.Manager.HandleWait(ctx, input)
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

func agentCancelTool(deps AgentDeps) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name:        "agent_cancel",
			Description: `Cancel a running or starting sub-agent job and wait until it stops.`,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"job_id": llm.Object{
						"type": "string",
					},
				},
				Required: []string{"job_id"},
			},
		}),
		tooldef.WithDetail(jobIDDetail),
		tooldef.WithRun(func(ctx context.Context, input json.RawMessage) (tooldef.Result, error) {
			if err := deps.Manager.HandleCancel(ctx, input); err != nil {
				return tooldef.Result{}, err
			}
			body := mustJSON(map[string]any{"ok": true})
			return tooldef.Result{Content: body, Detail: "stopped", Output: body}, nil
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
