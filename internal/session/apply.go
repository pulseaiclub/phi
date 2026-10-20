package session

import (
	"fmt"
	"maps"
	"slices"

	"github.com/pulseaiclub/phi/internal/llm"
)

// Apply returns a new snapshot with ev applied (immutable reducer).
func Apply(s Snapshot, ev Event) Snapshot {
	out := Snapshot{
		Messages:   append([]Message(nil), s.Messages...),
		Tools:      maps.Clone(s.Tools),
		Compacting: s.Compacting,
	}
	switch e := ev.(type) {
	case UserAppend:
		id := e.ID
		if id == "" {
			id = fmt.Sprintf("user-%d", len(out.Messages)+1)
		}
		out.Messages = append(out.Messages, Message{
			ID:     id,
			Role:   RoleUser,
			Text:   e.Text,
			Images: append([]llm.Image(nil), e.Images...),
		})
	case LocalBashStart:
		id := e.ID
		if id == "" {
			id = fmt.Sprintf("bash-%d", len(out.Messages)+1)
		}
		out.Messages = append(out.Messages, Message{
			ID:   id,
			Role: RoleLocalBash,
			Text: e.Command,
		})
		if out.Tools == nil {
			out.Tools = make(map[string]ToolRun)
		}
		out.Tools[id] = ToolRun{
			ToolUseID: id,
			Name:      "bash",
			Status:    ToolInProgress,
			Detail:    e.Command,
			Local:     true,
			InContext: e.InContext,
		}
	case AssistantMessageUpdate:
		m := e.Message
		m.Role = RoleAssistant
		if m.Text == "" {
			m.Text = m.FlatText()
		}
		if i, ok := assistantReplaceIndex(out.Messages, m); ok {
			if m.ID == "" {
				m.ID = out.Messages[i].ID
			}
			// Keep last known usage when a streaming delta omits it.
			if !m.Usage.Reported() && out.Messages[i].Usage.Reported() {
				m.Usage = out.Messages[i].Usage
			}
			out.Messages[i] = m
		} else {
			if m.ID == "" {
				m.ID = fmt.Sprintf("assistant-%d", len(out.Messages)+1)
			}
			out.Messages = append(out.Messages, m)
		}
		for _, b := range m.Content {
			if b.Type != BlockToolUse || b.ID == "" {
				continue
			}
			if _, exists := out.Tools[b.ID]; exists {
				continue
			}
			if out.Tools == nil {
				out.Tools = make(map[string]ToolRun)
			}
			out.Tools[b.ID] = ToolRun{
				ToolUseID: b.ID,
				Name:      b.Name,
				Status:    ToolInProgress,
				Detail:    b.Input,
			}
		}
	case ToolData:
		if out.Tools == nil {
			out.Tools = make(map[string]ToolRun)
		}
		run := e.Run
		if prev, ok := out.Tools[run.ToolUseID]; ok {
			if run.Name == "" {
				run.Name = prev.Name
			}
			if run.Detail == "" {
				run.Detail = prev.Detail
			}
			if run.Status == ToolInProgress && run.Output == "" && prev.Output != "" {
				run.Output = prev.Output
			}
			if prev.Local {
				run.Local = true
			}
			if prev.InContext {
				run.InContext = true
			}
			if prev.Expanded {
				run.Expanded = true
			}
		}
		out.Tools[run.ToolUseID] = run
	case CancelStreaming:
		if i := lastAssistantIndex(out.Messages); i >= 0 && out.Messages[i].State == StateStreaming {
			out.Messages[i].State = StateCancelled
		}
		for id, run := range out.Tools {
			if run.Local {
				continue // Esc during agent must not cancel user "!cmd" bash
			}
			switch run.Status {
			case ToolInProgress, ToolQueued:
				run.Status = ToolCancelled
				out.Tools[id] = run
			}
		}
		out.Compacting = false
	case CompactionStarted:
		out.Compacting = true
	case CompactionComplete:
		out.Compacting = false
		if !e.Failed {
			id := e.ID
			if id == "" {
				id = fmt.Sprintf("compaction-%d", len(out.Messages)+1)
			}
			out.Messages = append(out.Messages, Message{
				ID:           id,
				Role:         RoleCompaction,
				TokensBefore: e.TokensBefore,
			})
		}
	}
	return out
}

// assistantReplaceIndex finds the assistant row to replace for message-update.
// Streaming turns always replace the last assistant. Otherwise same ID replaces.
func assistantReplaceIndex(msgs []Message, update Message) (int, bool) {
	if len(msgs) == 0 {
		return -1, false
	}
	last := len(msgs) - 1
	if msgs[last].Role != RoleAssistant {
		return -1, false
	}
	if msgs[last].State == StateStreaming {
		return last, true
	}
	if update.ID != "" && update.ID == msgs[last].ID {
		return last, true
	}
	return -1, false
}

func lastAssistantIndex(msgs []Message) int {
	for i := range slices.Backward(msgs) {
		if msgs[i].Role == RoleAssistant {
			return i
		}
	}
	return -1
}

// IsStreaming reports whether inference, tools, or compaction are still active.
func IsStreaming(s Snapshot) bool {
	if s.Compacting {
		return true
	}
	if i := lastAssistantIndex(s.Messages); i >= 0 && s.Messages[i].State == StateStreaming {
		return true
	}
	return HasRunningTools(s)
}

// HasRunningTools reports in-progress or queued agent tool runs
// (excludes user-initiated local bash).
func HasRunningTools(s Snapshot) bool {
	for _, run := range s.Tools {
		if run.Local {
			continue
		}
		switch run.Status {
		case ToolInProgress, ToolQueued:
			return true
		}
	}
	return false
}

// RunningToolCount returns how many agent tools are in-progress/queued.
func RunningToolCount(s Snapshot) int {
	n := 0
	for _, run := range s.Tools {
		if run.Local {
			continue
		}
		switch run.Status {
		case ToolInProgress, ToolQueued:
			n++
		}
	}
	return n
}
