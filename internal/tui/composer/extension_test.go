package composer

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestExtensionPickerSelectionCancellationAndReplacement(t *testing.T) {
	for _, name := range []string{"selection", "cancellation", "replacement", "timeout", "ordinary list"} {
		t.Run(name, func(t *testing.T) {
			c := NewComposerPane(components.DefaultTheme(), "", t.TempDir())
			open := func() chan pxb.HostResult {
				reply := make(chan pxb.HostResult, 1)
				c.ApplyExtensionSelect(
					ext.SelectRequest{Title: "pick", Items: []ext.SelectItem{{Label: "first"}, {Label: "second"}}},
					controller.ExtHostRequestMsg{Reply: reply},
				)
				return reply
			}
			key := func(code xui.KeyCode) {
				c.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: code})
			}
			reply := open()
			switch name {
			case "selection":
				key(xui.KeyDown)
				key(xui.KeyEnter)
				assert.Equal(t, pxb.HostResult{Body: "1", OK: true}, <-reply)
				assert.Empty(t, reply, "close must not send a second response")
			case "cancellation":
				key(xui.KeyEscape)
				assert.False(t, (<-reply).OK)
			case "replacement":
				old := reply
				reply = open()
				assert.False(t, (<-old).OK, "replacement cancels the old request")
				c.ApplyExtensionSelect(ext.SelectRequest{}, controller.ExtHostRequestMsg{Reply: old, Dismiss: true})
				require.True(t, c.listPicker.Open, "stale timeout must not close a new picker")
				key(xui.KeyEscape)
				assert.False(t, (<-reply).OK)
			case "timeout":
				c.ApplyExtensionSelect(ext.SelectRequest{}, controller.ExtHostRequestMsg{Reply: reply, Dismiss: true})
				assert.False(t, (<-reply).OK)
			case "ordinary list":
				key(xui.KeyEscape)
				assert.False(t, (<-reply).OK)
				c.ShowList([]listpicker.Item{{ID: "session"}}, listpicker.ShowConfig{}, nil, nil)
				key(xui.KeyEscape)
				assert.Empty(t, reply, "ordinary lists must not retain extension callbacks")
			}
		})
	}
}
