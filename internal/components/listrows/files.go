package listrows

import (
	"path"
	"slices"
	"strings"

	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
)

// FileItems maps slash-separated relative paths to rows, ordered by full path.
func FileItems(paths []string) []listpicker.Item {
	items := make([]listpicker.Item, 0, len(paths))
	for _, name := range paths {
		items = append(items, listpicker.Item{
			ID:      name,
			Primary: path.Base(name),
			Detail:  path.Dir(name),
		})
	}
	slices.SortFunc(items, func(a, b listpicker.Item) int { return strings.Compare(a.ID, b.ID) })
	return items
}

// FileConfig returns the file picker's chrome and filename/directory layout.
func FileConfig() listpicker.ShowConfig {
	return listpicker.ShowConfig{
		Title:        "Files",
		FilterHint:   "filename or path",
		Empty:        "No files found",
		EmptyFilter:  "No files match %q",
		Hint:         chrome.ListHint("open"),
		PrimaryWidth: 30,
	}
}
