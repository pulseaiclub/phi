package composer

import (
	"strconv"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func (c *ComposerPane) ApplyExtensionSelect(req ext.SelectRequest, msg controller.ExtHostRequestMsg) {
	if msg.Dismiss {
		if c.extSelectReply == msg.Reply {
			c.listPicker.Hide()
		}
		return
	}
	items := make([]listpicker.Item, len(req.Items))
	for i, item := range req.Items {
		items[i] = listpicker.Item{ID: strconv.Itoa(i), Primary: item.Label, Detail: item.Detail}
	}
	resolved := false
	resolve := func(reply pxb.HostResult) {
		if !resolved {
			msg.Reply <- reply
			resolved = true
		}
	}
	c.ShowList(items, listpicker.ShowConfig{Title: req.Title, PrimaryWidth: 40}, func(item listpicker.Item) {
		resolve(pxb.HostResult{Body: item.ID, OK: true})
	}, nil)
	c.extSelectReply = msg.Reply
	c.listPicker.OnClose = func() {
		resolve(pxb.HostResult{})
		c.extSelectReply = nil
		c.listPicker.OnClose = nil
	}
}
