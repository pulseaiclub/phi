package transcript

import (
	"strings"

	"github.com/pulseaiclub/phi/internal/components/sessiontree"
	"github.com/pulseaiclub/phi/internal/session"
)

func TreeItems(snap session.TreeSnapshot) []sessiontree.Item {
	items := make([]sessiontree.Item, 0, len(snap.Entries))
	for _, entry := range snap.Entries {
		item := sessiontree.Item{ID: entry.GetID()}
		if parent := entry.GetParent(); parent != nil {
			item.ParentID = *parent
		}
		switch e := entry.(type) {
		case session.SessionMessageEntry:
			item.Kind, item.Text, item.Time = string(e.Message.Role), e.Message.Content, e.Timestamp
			if item.Kind == "tool" {
				item.Text = e.Message.ToolCallID + " " + e.Message.Content
			}
			// Keep tool-call text copyable without changing Pi's base visibility rule.
			item.TextlessAssistant = item.Kind == "assistant" && strings.TrimSpace(item.Text) == ""
			if len(e.Message.ToolCalls) > 0 {
				var calls []string
				for _, call := range e.Message.ToolCalls {
					calls = append(calls, call.Function.Name+" "+call.Function.Arguments)
				}
				item.Text += "\n" + strings.Join(calls, "\n")
			}
		case session.CompactionEntry:
			item.Kind, item.Text, item.Time = "compaction", e.Compaction.Summary, e.Timestamp
		case session.BranchSummaryEntry:
			item.Kind, item.Text, item.Time = "summary", e.Summary, e.Timestamp
		case session.HistoryEntry:
			item.Kind, item.Text, item.Time = e.Kind, e.Text, e.Timestamp
			if e.Kind == "bash" {
				item.Text += "\n" + e.Run.Output
			}
		default:
			continue
		}
		label := snap.Labels[item.ID]
		item.Label, item.LabelTime = label.Label, label.Timestamp
		items = append(items, item)
	}
	return items
}
