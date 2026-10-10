package editor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// pickerEditor is the smallest shell that owns a composer and a bus.
func pickerEditor(t *testing.T) *Editor {
	t.Helper()
	bus := controller.NewBus(nil)
	e := &Editor{
		composer: composer.NewComposerPane(components.DefaultTheme(), "m", "/repo"),
		bus:      bus,
	}
	e.composer.Wire(nil, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	return e
}

func TestExtPickerOpensOverlayAndAnswersAccept(t *testing.T) {
	e := pickerEditor(t)
	reply := make(chan controller.ExtPickerReply, 1)
	e.Update(controller.ExtPickerMsg{
		Request: ext.PickerRequest{
			Title: "Models",
			Items: []ext.PickerItem{
				{ID: "opus", Label: "opus", Detail: "smart"},
				{ID: "haiku", Label: "haiku", Detail: "fast"},
			},
		},
		Reply: reply,
	})

	overlay, ok := e.composer.ListOverlay(components.DrawContext{
		Max:    components.Size{Width: 100, Height: 30},
		Method: xui.WidthUnicode,
	})
	require.True(t, ok, "an extension picker must reach the list overlay")
	text := components.SurfaceText(overlay.Surface)
	assert.Contains(t, text, "Models")
	assert.Contains(t, text, "opus")

	e.composer.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyEnter})
	assert.Equal(t, controller.ExtPickerReply{OK: true, ID: "opus"}, <-reply)
}

func TestExtPickerReportsDismissal(t *testing.T) {
	e := pickerEditor(t)
	reply := make(chan controller.ExtPickerReply, 1)
	e.Update(controller.ExtPickerMsg{
		Request: ext.PickerRequest{Items: []ext.PickerItem{{ID: "a", Label: "a"}}},
		Reply:   reply,
	})

	e.composer.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	assert.Equal(t, controller.ExtPickerReply{}, <-reply, "escaping answers nothing")
}

func TestExtPickerDismissClosesWithoutAnswering(t *testing.T) {
	e := pickerEditor(t)
	reply := make(chan controller.ExtPickerReply, 1)
	e.Update(controller.ExtPickerMsg{
		Request: ext.PickerRequest{Items: []ext.PickerItem{{ID: "a", Label: "a"}}},
		Reply:   reply,
	})
	e.Update(controller.ExtPickerMsg{Dismiss: true})

	_, ok := e.composer.ListOverlay(components.DrawContext{Max: components.Size{Width: 100, Height: 30}})
	assert.False(t, ok, "the timed-out asker must not leave a picker behind")
	// The take-down lands in the abandoned buffer and is read by nobody.
	assert.Equal(t, controller.ExtPickerReply{}, <-reply)
}
