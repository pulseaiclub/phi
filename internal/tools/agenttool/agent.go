package agenttool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/util"
)

const agentLaunchGuidance = `Launch a specialized sub-agent. Pick a role:

- explore (default): no write/edit; full bash except hard denies. Multi-hop recon / unknown location — keep tool noise out of this context.
- review: same tools as explore; diffs, checks, quality/security report — do not edit.
- worker: read/write/edit + bash; implement a scoped, self-contained change and verify. Not for open-ended exploration.

When NOT to use any sub-agent:
- Exact file path already known — use read yourself
- Exact symbol like "class Foo" — use grep yourself
- Tiny local edit — edit/write yourself

How to use:
1. Spawn jobs first, do any independent work, then use agent_wait with only job_id when you need a result. It wakes on completion; do not guess a timeout or poll. For parallel jobs, spawn all first, then wait each.
2. Stateless: put a highly detailed, self-contained prompt and say what the final summary must include.
3. You only receive the final summary. Summarize for the user if needed.
4. Sub-agents cannot spawn further agents. Do not put secrets in the prompt.
5. Verify before relying on a worker's edits in follow-up work.`

// Binding is the job context used by the agent tools.
type Binding struct {
	manager *job.Manager
	workDir string
}

// BindForSession binds tools to a working directory without changing the manager's active parent.
// A nil manager disables sub-agent tools.
func BindForSession(manager *job.Manager, workDir string) *Binding {
	if manager == nil {
		return nil
	}
	return &Binding{manager: manager, workDir: workDir}
}

// Tools returns agent_spawn and agent_wait for this binding.
func (b *Binding) Tools() []tooldef.Tool {
	if b == nil {
		return nil
	}
	return []tooldef.Tool{
		agentSpawnTool(b),
		agentWaitTool(b),
	}
}

// MaxConcurrent returns the manager's sub-agent concurrency limit.
func (b *Binding) MaxConcurrent() int {
	if b == nil {
		return 0
	}
	return b.manager.MaxConcurrent()
}

func agentSpawnTool(binding *Binding) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "agent_spawn",
			Description: fmt.Sprintf(agentLaunchGuidance+`

Starts asynchronously and returns job_id immediately. Use agent_wait with only job_id for the summary. Best for parallel jobs.

Concurrency cap: at most %d sub-agents run concurrently; spawning more fails (jobs are not queued).`, binding.MaxConcurrent()),
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
			var wd string
			if strings.TrimSpace(in.WorkDir) != "" {
				wd, err = absCleanAt(in.WorkDir, binding.workDir)
				if err != nil {
					return tooldef.Result{}, fmt.Errorf("agent_spawn workdir: %w", err)
				}
			}
			req := job.SpawnRequest{
				Prompt:          in.Prompt,
				Description:     in.Description,
				ParentToolUseID: tooldef.ToolCallID(ctx),
				Role:            role,
				WorkDir:         wd,
			}
			if in.TimeoutSec > 0 {
				req.Timeout = time.Duration(in.TimeoutSec) * time.Second
			}
			info, err := binding.manager.Spawn(ctx, req)
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

// jobIDInput is the {job_id} payload of agent_wait.
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

func agentWaitTool(binding *Binding) tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name: "agent_wait",
			Description: `Wait for a sub-agent to finish and return its complete result.md summary.

Normally call with only job_id: {"job_id":"..."}. With timeout_sec omitted or 0, this single call waits until the job finishes and returns immediately on completion. You do not need to predict how long the job will take. Do not poll with repeated short waits.

Do independent work before waiting. For parallel jobs, spawn all first, then wait for each result you need.
Set timeout_sec only when you need control back before completion. If it returns a non-terminal status, the job is still running; omit timeout_sec on the next wait to receive the result.
A wait timeout or interrupted wait does NOT cancel the job; it keeps running until the session ends.`,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"job_id": llm.Object{
						"type":        "string",
						"description": "Job id from agent_spawn.",
					},
					"timeout_sec": llm.Object{
						"type":        "integer",
						"description": "Omit or use 0 to wait until completion (recommended). Positive seconds limit only this wait, not the job. Avoid short polling timeouts.",
					},
				},
				Required: []string{"job_id"},
			},
		}),
		tooldef.WithDetail(jobIDDetail),
		tooldef.WithRun(func(ctx context.Context, input json.RawMessage) (tooldef.Result, error) {
			res, err := binding.manager.HandleWait(ctx, input)
			if err != nil {
				return tooldef.Result{}, err
			}
			label := roleDetail(string(res.Info.Role), jobLabel(res.Info.Description, res.Info.Prompt))
			body := mustJSON(map[string]any{
				"job_id":      res.Info.ID,
				"status":      res.Info.Status,
				"role":        res.Info.Role,
				"label":       label,
				"error":       res.Info.Error,
				"result_path": res.Info.ResultPath,
				"summary":     res.Summary,
			})
			return tooldef.Result{
				Content: body,
				Detail:  label,
				Output:  body,
			}, nil
		}),
	)
}

func absCleanAt(path, cwd string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	} else {
		var err error
		cwd, err = filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
	}
	return filepath.Clean(filepath.Join(cwd, path)), nil
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
