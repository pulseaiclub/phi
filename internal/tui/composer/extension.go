package composer

import (
	"strconv"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
)

// ShowExtensionSelect opens the shared picker for an extension request.
// onResult runs once on close, with OK=false unless an item was accepted.
func (c *ComposerPane) ShowExtensionSelect(req ext.SelectRequest, onResult func(ext.SelectReply)) {
	items := make([]listpicker.Item, len(req.Items))
	for i, item := range req.Items {
		items[i] = listpicker.Item{ID: strconv.Itoa(i), Primary: item.Label, Detail: item.Detail}
	}
	var result ext.SelectReply
	c.ShowList(items, listpicker.ShowConfig{Title: req.Title, PrimaryWidth: 40}, func(item listpicker.Item) {
		if index, err := strconv.Atoi(item.ID); err == nil {
			result = ext.SelectReply{Index: index, OK: true}
		}
	}, nil)
	c.listPicker.OnClose = func() {
		c.listPicker.OnClose = nil
		onResult(result)
	}
}
