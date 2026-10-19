// Package branchlist maps gitx.Branch rows into listpicker items.
// A row is two columns — branch name, last commit — plus a dot for the branch
// HEAD is on. Ordering is the product decision here: where you are, where you
// came from, the local rest, then remotes.
package branchlist

import (
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

// branchColumn leaves room for the commit subject on an 80-column terminal.
// Longer names elide; typing three characters filters them back into view.
const branchColumn = 30

// Config returns chrome copy for the branch picker.
func Config() listpicker.ShowConfig {
	return listpicker.ShowConfig{
		Title:          "Branches",
		FilterHint:     "filter branches…",
		Empty:          "No branches yet — commit something first",
		EmptyFilter:    "No branches match %q",
		Hint:           chrome.ListHintExtra("switch", "ctrl+x delete"),
		ConfirmMessage: "Delete this branch?",
		LeadingWidth:   2,
		PrimaryWidth:   branchColumn,
	}
}

// Items orders branches for display and maps each one to a row.
// recent holds branch names in most-recently-checked-out order; names not in
// branches are ignored.
func Items(branches []gitx.Branch, recent []string) []listpicker.Item {
	byName := make(map[string]int, len(branches))
	for i, b := range branches {
		byName[b.Name] = i
	}
	out := make([]listpicker.Item, 0, len(branches))
	seen := make(map[string]bool, len(branches))
	add := func(b gitx.Branch) {
		if seen[b.Name] {
			return
		}
		seen[b.Name] = true
		out = append(out, row(b))
	}
	for _, b := range branches {
		if b.Current {
			add(b)
		}
	}
	for _, name := range recent {
		if i, ok := byName[name]; ok {
			add(branches[i])
		}
	}
	for _, b := range branches {
		if !b.Remote {
			add(b)
		}
	}
	for _, b := range branches {
		if b.Remote {
			add(b)
		}
	}
	return out
}

func row(b gitx.Branch) listpicker.Item {
	return listpicker.Item{
		// The row's own ref is its identity: origin/feat and feat are different
		// branches, and a delete must never land on the local namesake.
		ID:       b.Name,
		Leading:  currentMarker(b),
		Primary:  b.Name,
		Detail:   b.Subject,
		Keywords: b.Name + " " + b.Subject,
	}
}

// currentMarker keeps the one thing the list cannot be read without: which
// branch HEAD is on. Upstream state, ahead/behind counts, and badges stay out of
// the row — the list is for picking, not for auditing.
func currentMarker(b gitx.Branch) string {
	if b.Current {
		return "●"
	}
	return ""
}
