package composer

import (
	"strings"

	"github.com/pulseaiclub/phi/internal/components/mention"
)

// questionShortcutItems lists `?` help rows. promptRecall adds the Up/Down
// recall keys, which do nothing unless prompt history is enabled.
func questionShortcutItems(promptRecall bool) []mention.Item {
	items := []mention.Item{
		{Path: "/", Description: "slash commands"},
		{Path: "!", Description: "shell commands"},
		{Path: "@", Description: "mention files"},
		{Path: "?", Description: "shortcut help"},
		{Path: "Esc", Description: "cancel / stop"},
		{Path: "Ctrl+K", Description: "command palette"},
		{Path: "Ctrl+A", Description: "start of line"},
		{Path: "Ctrl+V", Description: "paste images or text"},
		{Path: "Ctrl+U", Description: "clear input"},
		{Path: "Ctrl+E", Description: "end of line"},
		{Path: "Shift+Enter", Description: "newline"},
		{Path: "Ctrl+Shift+C", Description: "copy message"},
	}
	if promptRecall {
		items = append(items,
			mention.Item{Path: "Up/Down, Ctrl+P/Ctrl+N", Description: "recall recent prompts"},
		)
	}
	return items
}

func filterQuestionItems(query string, all []mention.Item) []mention.Item {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]mention.Item, len(all))
		copy(out, all)
		return out
	}
	var out []mention.Item
	for _, it := range all {
		if strings.Contains(strings.ToLower(it.Path), q) ||
			strings.Contains(strings.ToLower(it.Description), q) {
			out = append(out, it)
		}
	}
	return out
}
