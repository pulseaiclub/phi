package session

import (
	"fmt"
	"strings"
)

// ItemKind classifies a projected transcript row.
type ItemKind int

// ItemKind values for projected transcript rows.
const (
	ItemUser ItemKind = iota
	ItemThinking
	ItemAssistant
	ItemTool
	ItemCompaction
)

// Item is one list row projected from Snapshot.
type Item struct {
	ID   string
	Kind ItemKind

	Text  string
	State State

	Thinking    string
	Streaming   bool
	Interrupted bool

	ToolRun ToolRun

	// TokensBefore is the pre-cut context size on ItemCompaction rows.
	TokensBefore int
	// Summary is the compaction handoff text on ItemCompaction rows.
	Summary string
}

// Project flattens Snapshot into list items in content order:
// user → thinking / assistant text / tool rows.
func Project(s Snapshot) []Item {
	var items []Item
	for _, m := range s.Messages {
		switch m.Role {
		case RoleUser:
			if strings.TrimSpace(m.Text) != "" || len(m.Images) > 0 {
				items = append(items, Item{
					ID:   m.ID,
					Kind: ItemUser,
					Text: m.Text,
				})
			}
		case RoleAssistant:
			items = append(items, projectAssistant(m, s.Tools)...)
		case RoleCompaction:
			items = append(items, Item{
				ID:           m.ID,
				Kind:         ItemCompaction,
				TokensBefore: m.TokensBefore,
				Summary:      m.Summary,
			})
		case RoleLocalBash:
			items = append(items, Item{
				ID:   "bash-" + m.ID,
				Kind: ItemTool,
				ToolRun: mergeToolRun(ToolRun{
					ToolUseID: m.ID,
					Name:      "bash",
					Status:    ToolInProgress,
					Detail:    m.Text,
					Local:     true,
				}, s.Tools),
			})
		}
	}
	return items
}

func projectAssistant(m Message, tools map[string]ToolRun) []Item {
	var items []Item
	textSeg := 0

	emitText := func(text string, streamingTail bool) {
		if text == "" && !streamingTail {
			return
		}
		st := m.State
		if !streamingTail && m.State == StateStreaming {
			st = StateComplete
		}
		if m.State == StateCancelled {
			st = StateCancelled
		}
		items = append(items, Item{
			ID:    fmt.Sprintf("%s-text-%d", m.ID, textSeg),
			Kind:  ItemAssistant,
			Text:  text,
			State: st,
		})
		textSeg++
	}

	var textBuf strings.Builder
	for i, b := range m.Content {
		switch b.Type {
		case BlockThinking:
			if strings.TrimSpace(b.Text) == "" && m.State != StateStreaming {
				continue
			}
			thinkStreaming := m.State == StateStreaming && isTrailingThinking(m.Content, i)
			items = append(items, Item{
				ID:          fmt.Sprintf("%s-thinking-%d", m.ID, i),
				Kind:        ItemThinking,
				Thinking:    b.Text,
				Streaming:   thinkStreaming,
				Interrupted: m.State == StateCancelled,
			})
		case BlockText:
			textBuf.WriteString(b.Text)
		case BlockToolUse:
			emitText(textBuf.String(), false)
			textBuf.Reset()
			items = append(items, Item{
				ID:   "tool-" + b.ID,
				Kind: ItemTool,
				ToolRun: mergeToolRun(ToolRun{
					ToolUseID: b.ID,
					Name:      b.Name,
					Status:    ToolInProgress,
					Detail:    b.Input,
				}, tools),
			})
		}
	}

	tail := textBuf.String()
	emptyWait := m.State == StateStreaming && len(items) == 0 && tail == ""
	emitText(tail, emptyWait || (m.State == StateStreaming && tail != ""))

	if m.State == StateCancelled {
		for i := range items {
			if items[i].Kind == ItemThinking {
				items[i].Streaming = false
				items[i].Interrupted = true
			}
			if items[i].Kind == ItemAssistant {
				items[i].State = StateCancelled
			}
		}
	}
	return items
}

// mergeToolRun overlays live execution state onto the content-block fallback
// so the projected row always has Name / ToolUseID / Detail even when the
// live run omitted them.
func mergeToolRun(fallback ToolRun, live map[string]ToolRun) ToolRun {
	if live == nil {
		return fallback
	}
	tr, ok := live[fallback.ToolUseID]
	if !ok {
		return fallback
	}
	if tr.Name == "" {
		tr.Name = fallback.Name
	}
	if tr.Detail == "" {
		tr.Detail = fallback.Detail
	}
	if tr.ToolUseID == "" {
		tr.ToolUseID = fallback.ToolUseID
	}
	if fallback.Local {
		tr.Local = true
	}
	return tr
}

func isTrailingThinking(blocks []ContentBlock, i int) bool {
	for j := i + 1; j < len(blocks); j++ {
		switch blocks[j].Type {
		case BlockThinking:
			return false
		case BlockText:
			if blocks[j].Text != "" {
				return false
			}
		case BlockToolUse:
			return false
		}
	}
	return true
}
