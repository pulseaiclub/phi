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
	theme        components.Theme
	spinner      *status.Spinner
	expanded     map[string]bool
	onInvalidate func() // e.g. MessageList.InvalidateHeights
	// Children returns nested sub-agent tool rows for a parent tool_use id.
	Children func(parentToolUseID string) []block.ChildTool
	// ChildrenByJob returns nested rows keyed by job id (fallback for spawn/task).
	ChildrenByJob func(jobID string) []block.ChildTool
}

// NewMapper builds a Mapper with the given theme, spinner, and invalidation callback.
func NewMapper(theme components.Theme, spinner *status.Spinner, onInvalidate func()) *Mapper {
	return &Mapper{
		theme:        theme,
		spinner:      spinner,
		expanded:     make(map[string]bool),
		onInvalidate: onInvalidate,
	}
}

// SetTheme updates the theme used for newly built and patched widgets.
func (m *Mapper) SetTheme(theme components.Theme) {
	if m != nil {
		m.theme = theme
	}
}

// onToggle returns a toggle callback that updates the expanded state for the given entry and triggers a layout invalidation.
func (m *Mapper) onToggle(id string) func(bool) {
	return func(expanded bool) {
		m.expanded[id] = expanded
		if m.onInvalidate != nil {
			m.onInvalidate()
		}
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
		m.expanded[id] = expandedState(w)
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
		if exp, ok := m.expanded[it.ID]; ok {
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
		dirty := c.TokensBefore != it.TokensBefore
		c.TokensBefore = it.TokensBefore
		return true, dirty
	case session.ItemTool:
		return m.patchTool(w, it)
	}
	return false, false
}

func (m *Mapper) patchTool(w components.Widget, it session.Item) (ok, dirty bool) {
	run := it.ToolRun
	name := strings.ToLower(run.Name)
	if name == "bash" {
		b, ok := w.(*block.BashBlock)
		if !ok {
			return false, false
		}
		st := uiToolStatus(run.Status)
		prevExp := b.Expanded
		dirty = b.Command != run.Detail || b.Output != run.Output || b.Status != st || b.ExitCode != run.ExitCode
		b.Command = run.Detail
		b.Output = run.Output
		b.Status = st
		b.ExitCode = run.ExitCode
		b.Theme = m.theme
		if exp, ok := m.expanded[it.ID]; ok {
			b.Expanded = exp
		} else if run.Local || run.Expanded {
			// User "!cmd" / plugin Expanded: keep body visible.
			b.Expanded = true
		} else if b.Status == status.ToolRunning && b.Output != "" {
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
	st := uiToolStatus(run.Status)
	prevExp := t.Expanded
	dirty = t.Name != run.Name || t.Detail != run.Detail || t.Output != run.Output ||
		t.Error != run.Error || t.Status != st
	t.Name = run.Name
	t.Detail = run.Detail
	t.Output = run.Output
	t.Error = run.Error
	t.Status = st
	t.Theme = m.theme
	t.Spinner = m.spinner
	if exp, ok := m.expanded[it.ID]; ok {
		t.Expanded = exp
	} else if run.Expanded {
		t.Expanded = true
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

// expandedState reads the expanded flag from a known widget type, returning
// false for widget types that do not carry expand state.
func expandedState(w components.Widget) bool {
	switch b := w.(type) {
	case *block.ThinkingBlock:
		return b.Expanded
	case *block.ToolBlock:
		return b.Expanded
	case *block.BashBlock:
		return b.Expanded
	case *block.AgentBlock:
		return b.Expanded
	}
	return false
}

func (m *Mapper) widgetFor(it session.Item) components.Widget {
	exp := m.expanded[it.ID]
	id := it.ID
	switch it.Kind {
	case session.ItemUser:
		return &block.UserBlock{Text: it.Text, Theme: m.theme}
	case session.ItemThinking:
		return &block.ThinkingBlock{
			Text:        it.Thinking,
			Streaming:   it.Streaming,
			Interrupted: it.Interrupted,
			Expanded:    exp || it.Streaming,
			Theme:       m.theme,
			Spinner:     m.spinner,
			OnToggle:    m.onToggle(id),
		}
	case session.ItemCompaction:
		return &block.CompactionBlock{Theme: m.theme, TokensBefore: it.TokensBefore}
	case session.ItemTool:
		return m.toolWidget(it, exp)
	default:
		return &block.AssistantBlock{Text: it.Text, State: it.State, Theme: m.theme}
	}
}

func (m *Mapper) toolWidget(it session.Item, exp bool) components.Widget {
	run := it.ToolRun
	autoExp := exp
	if !exp {
		if run.Local || run.Expanded {
			autoExp = true
		} else if run.Status == session.ToolInProgress && run.Output != "" {
			autoExp = true
		}
	}
	id := it.ID
	if strings.EqualFold(run.Name, "bash") {
		return &block.BashBlock{
			Command:  run.Detail,
			Output:   run.Output,
			Status:   uiToolStatus(run.Status),
			ExitCode: run.ExitCode,
			Expanded: autoExp,
			Theme:    m.theme,
			OnToggle: m.onToggle(id),
		}
	}
	if isAgentTreeTool(run.Name) {
		a := &block.AgentBlock{
			Theme:    m.theme,
			Spinner:  m.spinner,
			OnToggle: m.onToggle(id),
		}
		m.fillAgentBlock(a, it)
		return a
	}
	return &block.ToolBlock{
		Name:     run.Name,
		Detail:   run.Detail,
		Output:   run.Output,
		Error:    run.Error,
		Status:   uiToolStatus(run.Status),
		Expanded: autoExp,
		Theme:    m.theme,
		Spinner:  m.spinner,
		OnToggle: m.onToggle(id),
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
	run := it.ToolRun
	a.Name = run.Name
	a.Detail = run.Detail
	a.Status = uiToolStatus(run.Status)
	a.Theme = m.theme
	a.Spinner = m.spinner
	a.Error = run.Error

	parsed := tools.ParseAgentResult(run.Output)
	if sum := parsed.RenderableSummary(); sum != "" {
		a.Summary = sum
	} else {
		a.Summary = ""
	}

	// agent_wait: summary only — the live tree already lives on agent_spawn.
	// agent_spawn: nested child tools from SubagentStore.
	a.Children = nil
	if !strings.EqualFold(run.Name, "agent_wait") && m.Children != nil {
		a.Children = m.Children(run.ToolUseID)
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
