// Package changelist maps git changes into the shared list picker.
package changelist

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

func Config() listpicker.ShowConfig {
	return listpicker.ShowConfig{
		Title: "Changes", FilterHint: "filter changed files…",
		Empty: "No changes", EmptyFilter: "No changed files match %q",
		Hint: chrome.ListHint("review"), LeadingWidth: 2, PrimaryWidth: 40,
	}
}

func Items(changes []gitx.Change) []listpicker.Item {
	items := make([]listpicker.Item, 0, len(changes))
	for _, change := range changes {
		detail := statusLabel(change.Status)
		if change.Status == 'U' {
			detail = "Unresolved conflict"
		} else if change.OldPath != "" {
			detail += " from " + displayPath(change.OldPath)
		}
		items = append(items, listpicker.Item{
			ID: change.Path, Primary: displayPath(change.Path),
			Detail: detail, Keywords: change.Path + " " + change.OldPath,
		})
	}
	return items
}

func displayPath(path string) string {
	if strings.ContainsFunc(path, unicode.IsControl) {
		return strconv.Quote(path)
	}
	return path
}

func statusLabel(status byte) string {
	switch status {
	case 'M':
		return "Modified"
	case 'A':
		return "Added"
	case 'D':
		return "Deleted"
	case 'R':
		return "Renamed"
	case 'C':
		return "Copied"
	case 'T':
		return "Type changed"
	case '?':
		return "New file"
	default:
		return "Changed"
	}
}
