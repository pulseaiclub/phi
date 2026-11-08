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

// SessionItems maps session metas into generic list rows. A fork is listed
// directly under its source, so a forked conversation reads as one family
// instead of unrelated rows.
func SessionItems(list []session.SessionMeta, currentID string, now time.Time) []listpicker.Item {
	if now.IsZero() {
		now = time.Now()
	}
	inList := make(map[string]bool, len(list))
	for _, m := range list {
		inList[m.ID] = true
	}
	children := make(map[string][]session.SessionMeta, len(list))
	roots := make([]session.SessionMeta, 0, len(list))
	for _, m := range list {
		if m.Parent != "" && inList[m.Parent] {
			children[m.Parent] = append(children[m.Parent], m)
			continue
		}
		roots = append(roots, m)
	}

	out := make([]listpicker.Item, 0, len(list))
	var emit func(m session.SessionMeta)
	emit = func(m session.SessionMeta) {
		detail := m.Preview
		if detail == "" {
			detail = "(no preview)"
		}
		// A fork keeps showing where it came from: the picker is the only place
		// lineage is visible, so ↳ source id rides on the primary column.
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
		for _, child := range children[m.ID] {
			emit(child)
		}
	}
	for _, m := range roots {
		emit(m)
	}
	return out
}
