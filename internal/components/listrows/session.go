package listrows

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/session"
)

// SessionConfig returns chrome copy for the sessions picker.
func SessionConfig() listpicker.ShowConfig {
	return listpicker.ShowConfig{
		Title:          "Sessions",
		FilterHint:     "filter id or preview…",
		Empty:          "No sessions in this directory",
		EmptyFilter:    "No sessions match %q",
		Hint:           chrome.ListHintExtra("resume", "ctrl+x delete"),
		ConfirmMessage: "Delete this session?",
	}
}

// SessionItems maps session metas into generic list rows, in the order the
// listing hands them over (newest first). A fork keeps showing where it came
// from, so its source id rides on the primary column.
func SessionItems(list []session.SessionMeta, currentID string, now time.Time) []listpicker.Item {
	if now.IsZero() {
		now = time.Now()
	}
	out := make([]listpicker.Item, 0, len(list))
	for _, m := range list {
		detail := m.Preview
		if detail == "" {
			detail = "(no preview)"
		}
		primary := session.ShortID(m.ID)
		if m.Parent != "" {
			primary += " ↳ " + session.ShortID(m.Parent)
		}
		item := listpicker.Item{
			ID:       m.ID,
			Leading:  FormatRelative(m.Mtime, now),
			Primary:  primary,
			Detail:   detail,
			Keywords: m.ID + " " + m.Parent + " " + m.Preview,
		}
		if m.ID == currentID {
			item.Badge = "current"
		}
		out = append(out, item)
	}
	return out
}
