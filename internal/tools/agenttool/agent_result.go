package agenttool

import (
	"encoding/json"
	"strings"

	"github.com/pulseaiclub/phi/internal/job"
)

// AgentResult is the UI-facing parse of agent_spawn and agent_control wait output.
type AgentResult struct {
	JobID   string
	Status  string
	Label   string // human label ("explore: find auth"), never a job id
	Summary string
	Error   string
	OK      bool // true when output looked like agent tool JSON
}

// ParseAgentResult extracts job fields from tool Output/Content JSON.
// Non-JSON or unrelated payloads return OK=false.
func ParseAgentResult(output string) AgentResult {
	output = strings.TrimSpace(output)
	if output == "" || output[0] != '{' {
		return AgentResult{}
	}
	var raw struct {
		JobID   string `json:"job_id"`
		Status  string `json:"status"`
		Label   string `json:"label"`
		Summary string `json:"summary"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return AgentResult{}
	}
	if raw.JobID == "" && raw.Status == "" && raw.Summary == "" {
		return AgentResult{}
	}
	return AgentResult{
		JobID:   raw.JobID,
		Status:  raw.Status,
		Label:   raw.Label,
		Summary: raw.Summary,
		Error:   raw.Error,
		OK:      true,
	}
}

// Terminal reports whether Status is a finished job status.
func (r AgentResult) Terminal() bool {
	return job.Status(r.Status).Terminal()
}

// RenderableSummary is the markdown body to show when the job finished with a summary.
func (r AgentResult) RenderableSummary() string {
	if !r.OK || !r.Terminal() {
		return ""
	}
	return strings.TrimSpace(r.Summary)
}
