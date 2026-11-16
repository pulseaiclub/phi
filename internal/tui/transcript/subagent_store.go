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
	childIdx        map[childKey]int // never nil; created with the view
}

// childKey identifies a nested tool row: the tool_use id when the child
// reported one, else a name+detail fallback. The parts stay separate fields
// rather than a joined string so a field containing the separator cannot make
// the two branches collide.
type childKey struct {
	tool   string
	name   string
	detail string
}

// NewSubagentStore creates an empty store.
func NewSubagentStore() *SubagentStore {
	return &SubagentStore{
		byParent: make(map[string]*subagentView),
		byJob:    make(map[string]*subagentView),
	}
}

func (s *SubagentStore) viewLocked(jobID, parentToolUseID string) *subagentView {
	v := s.lookupLocked(jobID, parentToolUseID)
	if v == nil {
		v = &subagentView{childIdx: make(map[childKey]int)}
	}
	if jobID != "" {
		v.JobID = jobID
	}
	if parentToolUseID != "" {
		v.ParentToolUseID = parentToolUseID
	}
	if v.ParentToolUseID != "" {
		s.byParent[v.ParentToolUseID] = v
	}
	if v.JobID != "" {
		s.byJob[v.JobID] = v
	}
	return v
}

// lookupLocked finds an existing view by parent tool_use id first, then job id.
func (s *SubagentStore) lookupLocked(jobID, parentToolUseID string) *subagentView {
	if parentToolUseID != "" {
		if v := s.byParent[parentToolUseID]; v != nil {
			return v
		}
	}
	if jobID != "" {
		return s.byJob[jobID]
	}
	return nil
}

// ApplyProgress upserts a child tool row under the parent agent tool.
// Returns true when the nested tree view actually changed.
func (s *SubagentStore) ApplyProgress(p job.Progress) bool {
	if p.JobID == "" && p.ParentToolUseID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.viewLocked(p.JobID, p.ParentToolUseID)
	key := childKey{tool: p.ToolUseID}
	if p.ToolUseID == "" {
		key.name, key.detail = p.Name, p.Detail
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

// ApplyResult links a job to its parent tool row and records its label.
func (s *SubagentStore) ApplyResult(parentToolUseID string, r agenttool.AgentResult) {
	if !r.OK {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.viewLocked(r.JobID, parentToolUseID)
	if r.Label != "" {
		v.Label = r.Label
	}
}

// Label returns the human label bound to a job id, or "" when unknown.
// agent_wait / agent_cancel rows carry only a job id in their args; this is
// what lets the transcript show the job's name instead.
func (s *SubagentStore) Label(jobID string) string {
	if jobID == "" {
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
	if parentToolUseID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyChildren(s.byParent[parentToolUseID])
}

// ChildrenByJob returns a copy of nested tools for a job id.
func (s *SubagentStore) ChildrenByJob(jobID string) []block.ChildTool {
	if jobID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyChildren(s.byJob[jobID])
}

// copyChildren snapshots the child rows so callers never alias the store.
func copyChildren(v *subagentView) []block.ChildTool {
	if v == nil {
		return nil
	}
	return append([]block.ChildTool(nil), v.Children...)
}
