package transcript

import (
	"strings"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
)

// Mapper converts session.Snapshot items into transcript widgets.
// It owns expand-state and has no dependency on Editor / xui / agent.
type Mapper struct {
	theme            components.Theme
	spinner          *status.Spinner
	expanded         map[string]bool
	thinkingDefault  bool
	thinkingExpanded map[string]bool
	onInvalidate     func() // e.g. MessageList.InvalidateHeights
	// Children returns nested sub-agent tool rows for a parent tool_use id.
	Children func(parentToolUseID string) []block.ChildTool
	// ChildrenByJob returns nested rows keyed by job id (fallback for spawn/task).
	ChildrenByJob func(jobID string) []block.ChildTool
}

// NewMapper builds a Mapper with the given theme, spinner, and invalidation callback.
func NewMapper(theme components.Theme, spinner *status.Spinner, onInvalidate func()) *Mapper {
	return &Mapper{
		theme:            theme,
		spinner:          spinner,
		expanded:         make(map[string]bool),
		thinkingExpanded: make(map[string]bool),
		onInvalidate:     onInvalidate,
	}
}

// SetTheme updates the theme used for newly built and patched widgets.
func (m *Mapper) SetTheme(theme components.Theme) {
	if m != nil {
		m.theme = theme
	}
}

// Sync rebuilds the widget list from snap, reusing widgets when patchable.
// dirty lists new-entry indices whose height-relevant content changed (or are new).
func (m *Mapper) Sync(
	entries []components.Widget,
	listIDs []string,
	snap session.Snapshot,
) (newEntries []components.Widget, newIDs []string, dirty []int) {
	items := session.Project(snap)
	n := len(items)
	byID := make(map[string]int, len(entries))
	for i, w := range entries {
		id := entryID(listIDs, i)
		if id == "" {
			continue
		}
		byID[id] = i
		switch b := w.(type) {
		case *block.ThinkingBlock:
			m.thinkingExpanded[id] = b.Expanded
		case *block.ToolBlock:
			m.expanded[id] = b.Expanded
		case *block.BashBlock:
			m.expanded[id] = b.Expanded
		case *block.AgentBlock:
			m.expanded[id] = b.Expanded
		}
	}

	newEntries = make([]components.Widget, 0, n)
	newIDs = make([]string, 0, n)
	for _, it := range items {
		idx := len(newEntries)
		newIDs = append(newIDs, it.ID)
		if oldIdx, ok := byID[it.ID]; ok {
			if ok, changed := m.patchItem(entries[oldIdx], it); ok {
				newEntries = append(newEntries, entries[oldIdx])
				if changed {
					dirty = append(dirty, idx)
				}
				continue
			}
		}
		newEntries = append(newEntries, m.widgetFor(it))
		dirty = append(dirty, idx)
	}
	return newEntries, newIDs, dirty
}

func entryID(listIDs []string, i int) string {
	if i >= 0 && i < len(listIDs) {
		return listIDs[i]
	}
	return ""
}

func (m *Mapper) patchItem(w components.Widget, it session.Item) (ok, dirty bool) {
	switch it.Kind {
	case session.ItemUser:
		u, ok := w.(*block.UserBlock)
		if !ok {
			return false, false
		}
		dirty = u.Text != it.Text
		u.Text = it.Text
		u.Theme = m.theme
		return true, dirty
	case session.ItemAssistant:
		a, ok := w.(*block.AssistantBlock)
		if !ok {
			return false, false
		}
		dirty = a.Text != it.Text || a.State != it.State
		a.Text = it.Text
		a.State = it.State
		a.Theme = m.theme
		return true, dirty
	case session.ItemThinking:
		t, ok := w.(*block.ThinkingBlock)
		if !ok {
			return false, false
		}
		prevExp := t.Expanded
		dirty = t.Text != it.Thinking || t.Streaming != it.Streaming || t.Interrupted != it.Interrupted
		t.Text = it.Thinking
		t.Streaming = it.Streaming
		t.Interrupted = it.Interrupted
		t.Theme = m.theme
		t.Spinner = m.spinner
		if exp, ok := m.thinkingExpanded[it.ID]; ok {
			t.Expanded = exp
		}
		if t.Expanded != prevExp {
			dirty = true
		}
		return true, dirty
	case session.ItemCompaction:
		c, ok := w.(*block.CompactionBlock)
		if !ok {
			return false, false
		}
		c.Theme = m.theme
		return true, false
	case session.ItemTool:
		return m.patchTool(w, it)
	}
	return false, false
}

func (m *Mapper) patchTool(w components.Widget, it session.Item) (ok, dirty bool) {
	name := strings.ToLower(it.ToolName)
	if name == "bash" {
		b, ok := w.(*block.BashBlock)
		if !ok {
			return false, false
		}
		cmd := it.ToolInput
		if it.ToolRun.Detail != "" {
			cmd = it.ToolRun.Detail
		}
		st := bashStatus(it.ToolRun.Status)
		prevExp := b.Expanded
		dirty = b.Command != cmd || b.Output != it.ToolRun.Output || b.Status != st || b.ExitCode != it.ToolRun.ExitCode
		b.Command = cmd
		b.Output = it.ToolRun.Output
		b.Status = st
		b.ExitCode = it.ToolRun.ExitCode
		b.Theme = m.theme
		if exp, ok := m.expanded[it.ID]; ok {
			b.Expanded = exp
		} else if it.ToolRun.Local {
			// User "!cmd" results should stay open so output is visible.
			b.Expanded = true
		} else if b.Status == block.BashRunning && b.Output != "" {
			b.Expanded = true
		}
		if b.Expanded != prevExp {
			dirty = true
		}
		return true, dirty
	}
	if isAgentTreeTool(name) {
		a, ok := w.(*block.AgentBlock)
		if !ok {
			return false, false
		}
		prev := agentHeightSnap{
			Name:     a.Name,
			Detail:   a.Detail,
			Status:   a.Status,
			Error:    a.Error,
			Summary:  a.Summary,
			Expanded: a.Expanded,
			Children: a.Children,
		}
		m.fillAgentBlock(a, it)
		dirty = prev.Name != a.Name || prev.Detail != a.Detail || prev.Status != a.Status ||
			prev.Error != a.Error || prev.Summary != a.Summary || prev.Expanded != a.Expanded ||
			!childToolsEqual(prev.Children, a.Children)
		return true, dirty
	}
	t, ok := w.(*block.ToolBlock)
	if !ok {
		return false, false
	}
	detail := it.ToolInput
	if it.ToolRun.Detail != "" {
		detail = it.ToolRun.Detail
	}
	st := uiToolStatus(it.ToolRun.Status)
	prevExp := t.Expanded
	dirty = t.Name != it.ToolName || t.Detail != detail || t.Output != it.ToolRun.Output ||
		t.Error != it.ToolRun.Error || t.Status != st
	t.Name = it.ToolName
	t.Detail = detail
	t.Output = it.ToolRun.Output
	t.Error = it.ToolRun.Error
	t.Status = st
	t.Theme = m.theme
	t.Spinner = m.spinner
	if exp, ok := m.expanded[it.ID]; ok {
		t.Expanded = exp
	} else if t.Status == status.ToolRunning && t.Output != "" {
		t.Expanded = true
	}
	if t.Expanded != prevExp {
		dirty = true
	}
	return true, dirty
}

type agentHeightSnap struct {
	Name     string
	Detail   string
	Status   status.ToolStatus
	Error    string
	Summary  string
	Expanded bool
	Children []block.ChildTool
}

func childToolsEqual(a, b []block.ChildTool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *Mapper) widgetFor(it session.Item) components.Widget {
	exp := m.expanded[it.ID]
	id := it.ID
	switch it.Kind {
	case session.ItemUser:
		return &block.UserBlock{Text: it.Text, Theme: m.theme}
	case session.ItemThinking:
		exp = m.thinkingDefault
		if saved, ok := m.thinkingExpanded[it.ID]; ok {
			exp = saved
		}
		return &block.ThinkingBlock{
			Text:        it.Thinking,
			Streaming:   it.Streaming,
			Interrupted: it.Interrupted,
			Expanded:    exp,
			Theme:       m.theme,
			Spinner:     m.spinner,
			OnToggle: func(expanded bool) {
				m.thinkingExpanded[id] = expanded
				if m.onInvalidate != nil {
					m.onInvalidate()
				}
			},
		}
	case session.ItemCompaction:
		return &block.CompactionBlock{Theme: m.theme}
	case session.ItemTool:
		return m.toolWidget(it, exp)
	default:
		return &block.AssistantBlock{Text: it.Text, State: it.State, Theme: m.theme}
	}
}

func (m *Mapper) toolWidget(it session.Item, exp bool) components.Widget {
	detail := it.ToolInput
	if it.ToolRun.Detail != "" {
		detail = it.ToolRun.Detail
	}
	autoExp := exp
	if !exp {
		if it.ToolRun.Local {
			autoExp = true
		} else if it.ToolRun.Status == session.ToolInProgress && it.ToolRun.Output != "" {
			autoExp = true
		}
	}
	id := it.ID
	if strings.EqualFold(it.ToolName, "bash") {
		return &block.BashBlock{
			Command:  detail,
			Output:   it.ToolRun.Output,
			Status:   bashStatus(it.ToolRun.Status),
			ExitCode: it.ToolRun.ExitCode,
			Expanded: autoExp,
			Theme:    m.theme,
			OnToggle: func(expanded bool) {
				m.expanded[id] = expanded
				if m.onInvalidate != nil {
					m.onInvalidate()
				}
			},
		}
	}
	if isAgentTreeTool(it.ToolName) {
		a := &block.AgentBlock{
			Theme:   m.theme,
			Spinner: m.spinner,
			OnToggle: func(expanded bool) {
				m.expanded[id] = expanded
				if m.onInvalidate != nil {
					m.onInvalidate()
				}
			},
		}
		m.fillAgentBlock(a, it)
		return a
	}
	return &block.ToolBlock{
		Name:     it.ToolName,
		Detail:   detail,
		Output:   it.ToolRun.Output,
		Error:    it.ToolRun.Error,
		Status:   uiToolStatus(it.ToolRun.Status),
		Expanded: autoExp,
		Theme:    m.theme,
		Spinner:  m.spinner,
		OnToggle: func(expanded bool) {
			m.expanded[id] = expanded
			if m.onInvalidate != nil {
				m.onInvalidate()
			}
		},
	}
}

func isAgentTreeTool(name string) bool {
	switch strings.ToLower(name) {
	case "agent_spawn", "agent_wait":
		return true
	default:
		return false
	}
}

func (m *Mapper) fillAgentBlock(a *block.AgentBlock, it session.Item) {
	detail := it.ToolInput
	if it.ToolRun.Detail != "" {
		detail = it.ToolRun.Detail
	}
	a.Name = it.ToolName
	a.Detail = detail
	a.Status = uiToolStatus(it.ToolRun.Status)
	a.Theme = m.theme
	a.Spinner = m.spinner
	a.Error = it.ToolRun.Error

	parsed := tools.ParseAgentResult(it.ToolRun.Output)
	if sum := parsed.RenderableSummary(); sum != "" {
		a.Summary = sum
	} else {
		a.Summary = ""
	}

	// agent_wait: summary only — the live tree already lives on agent_spawn.
	// agent_spawn: nested child tools from SubagentStore.
	a.Children = nil
	if !strings.EqualFold(it.ToolName, "agent_wait") && m.Children != nil {
		a.Children = m.Children(it.ToolUseID)
		if len(a.Children) == 0 && parsed.JobID != "" && m.ChildrenByJob != nil {
			a.Children = m.ChildrenByJob(parsed.JobID)
		}
	}

	if exp, ok := m.expanded[it.ID]; ok {
		a.Expanded = exp
	} else if a.Status == status.ToolRunning || len(a.Children) > 0 || a.Summary != "" {
		a.Expanded = true
	}
}

func bashStatus(s session.ToolStatus) block.BashStatus {
	switch s {
	case session.ToolDone:
		return block.BashDone
	case session.ToolError:
		return block.BashError
	case session.ToolCancelled:
		return block.BashCancelled
	case session.ToolRejected:
		return block.BashRejected
	default:
		return block.BashRunning
	}
}

func uiToolStatus(s session.ToolStatus) status.ToolStatus {
	switch s {
	case session.ToolDone:
		return status.ToolDone
	case session.ToolError:
		return status.ToolError
	case session.ToolCancelled:
		return status.ToolCancelled
	case session.ToolRejected:
		return status.ToolRejected
	case session.ToolQueued:
		return status.ToolQueued
	default:
		return status.ToolRunning
	}
}
