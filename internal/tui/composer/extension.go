package composer

import (
	"strconv"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
)

// ShowExtensionSelect opens the shared picker for an extension request.
// onAccept reports the chosen item; onClose fires when the picker closes
// (accept, Esc, replacement, or timeout), owned by ShowList like OnDelete.
func (c *ComposerPane) ShowExtensionSelect(req ext.SelectRequest, onAccept func(index int), onClose func()) {
	items := make([]listpicker.Item, len(req.Items))
	for i, item := range req.Items {
		items[i] = listpicker.Item{ID: strconv.Itoa(i), Primary: item.Label, Detail: item.Detail}
	}
	c.ShowList(items, listpicker.ShowConfig{Title: req.Title, PrimaryWidth: 40}, func(item listpicker.Item) {
		if index, err := strconv.Atoi(item.ID); err == nil {
			onAccept(index)
		}
	}, nil, onClose)
}
