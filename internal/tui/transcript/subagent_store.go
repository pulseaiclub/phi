package transcript

import (
	"sync"

	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
)

// SubagentStore holds UI-only nested tool trees for agent_* rows.
// It is not part of the session / LLM transcript.
type SubagentStore struct {
	mu       sync.Mutex
	byParent map[string]*subagentView
	byJob    map[string]*subagentView
}

type subagentView struct {
	JobID           string
	ParentToolUseID string
	Label           string // "explore: find auth"; never a job id
	Children        []block.ChildTool
	childIdx        map[string]int
	Summary         string
}

// NewSubagentStore creates an empty store.
func NewSubagentStore() *SubagentStore {
	return &SubagentStore{
		byParent: make(map[string]*subagentView),
		byJob:    make(map[string]*subagentView),
	}
}

// Bind links a job id to the parent agent tool_use id.
func (s *SubagentStore) Bind(jobID, parentToolUseID string) {
	if s == nil || jobID == "" || parentToolUseID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.viewLocked(jobID, parentToolUseID)
	v.JobID = jobID
	v.ParentToolUseID = parentToolUseID
}

func (s *SubagentStore) viewLocked(jobID, parentToolUseID string) *subagentView {
	var v *subagentView
	if parentToolUseID != "" {
		v = s.byParent[parentToolUseID]
	}
	if v == nil && jobID != "" {
		v = s.byJob[jobID]
	}
	if v == nil {
		v = &subagentView{
			JobID:           jobID,
			ParentToolUseID: parentToolUseID,
			childIdx:        make(map[string]int),
		}
	} else {
		if jobID != "" {
			v.JobID = jobID
		}
		if parentToolUseID != "" {
			v.ParentToolUseID = parentToolUseID
		}
		if v.childIdx == nil {
			v.childIdx = make(map[string]int)
		}
	}
	if v.ParentToolUseID != "" {
		s.byParent[v.ParentToolUseID] = v
	}
	if v.JobID != "" {
		s.byJob[v.JobID] = v
	}
	return v
}

// ApplyProgress upserts a child tool row under the parent agent tool.
// Returns true when the nested tree view actually changed.
func (s *SubagentStore) ApplyProgress(p job.Progress) bool {
	if s == nil || (p.JobID == "" && p.ParentToolUseID == "") {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.viewLocked(p.JobID, p.ParentToolUseID)
	key := p.ToolUseID
	if key == "" {
		key = p.Name + "\x00" + p.Detail
	}
	child := block.ChildTool{
		Name:   p.Name,
		Detail: p.Detail,
		Status: uiToolStatus(session.ParseToolStatus(p.Status)),
	}
	if i, ok := v.childIdx[key]; ok {
		if v.Children[i] == child {
			return false
		}
		v.Children[i] = child
		return true
	}
	v.childIdx[key] = len(v.Children)
	v.Children = append(v.Children, child)
	return true
}

// ApplyResult records label / terminal summary from agent tool JSON output.
func (s *SubagentStore) ApplyResult(parentToolUseID string, r agenttool.AgentResult) {
	if s == nil || !r.OK {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.viewLocked(r.JobID, parentToolUseID)
	if r.Label != "" {
		v.Label = r.Label
	}
	if sum := r.RenderableSummary(); sum != "" {
		v.Summary = sum
	}
}

// Label returns the human label bound to a job id, or "" when unknown.
// agent_control rows identify jobs by id in their args; this is
// what lets the transcript show the job's name instead.
func (s *SubagentStore) Label(jobID string) string {
	if s == nil || jobID == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.byJob[jobID]; v != nil {
		return v.Label
	}
	return ""
}

// Children returns a copy of nested tools for a parent tool_use id.
func (s *SubagentStore) Children(parentToolUseID string) []block.ChildTool {
	if s == nil || parentToolUseID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.byParent[parentToolUseID]
	if v == nil {
		return nil
	}
	return append([]block.ChildTool(nil), v.Children...)
}

// ChildrenByJob returns a copy of nested tools for a job id.
func (s *SubagentStore) ChildrenByJob(jobID string) []block.ChildTool {
	if s == nil || jobID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.byJob[jobID]
	if v == nil {
		return nil
	}
	return append([]block.ChildTool(nil), v.Children...)
}
