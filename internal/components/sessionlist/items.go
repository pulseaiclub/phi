// Package sessionlist maps session.SessionMeta rows into listpicker items.
package sessionlist

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/session"
)

// Config returns chrome copy for the sessions picker.
func Config() listpicker.ShowConfig {
	return listpicker.ShowConfig{
		Title:       "Sessions",
		FilterHint:  "filter id or preview…",
		Empty:       "No sessions in this directory",
		EmptyFilter: "No sessions match %q",
		Hint:        chrome.ListHint("resume"),
	}
}

// Items maps session metas into generic list rows.
func Items(list []session.SessionMeta, currentID string, now time.Time) []listpicker.Item {
	if now.IsZero() {
		now = time.Now()
	}
	out := make([]listpicker.Item, 0, len(list))
	for _, m := range list {
		detail := m.Title
		if detail == "" {
			detail = m.Preview
		}
		if detail == "" {
			detail = "(no preview)"
		}
		item := listpicker.Item{
			ID:       m.ID,
			Leading:  FormatRelative(m.Mtime, now),
			Primary:  shortID(m.ID),
			Detail:   detail,
			Keywords: m.ID + " " + m.Title + " " + m.Preview,
		}
		if m.ID == currentID {
			item.Badge = "current"
		}
		out = append(out, item)
	}
	return out
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
