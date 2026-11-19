package runtime

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestDispatchRoutesByTypeInRegistrationOrder(t *testing.T) {
	rt := New()
	var got []string
	Subscribe(rt, func(m controller.ToastMsg) { got = append(got, "first:"+m.Message) })
	Subscribe(rt, func(m controller.ToastMsg) { got = append(got, "second:"+m.Message) })
	Subscribe(rt, func(m controller.BranchLabelMsg) { got = append(got, "branch:"+m.Text) })

	assert.True(t, rt.Dispatch(controller.ToastMsg{Message: "hi"}))
	assert.Equal(t, []string{"first:hi", "second:hi"}, got)

	assert.True(t, rt.Dispatch(controller.BranchLabelMsg{Text: "main"}))
	assert.Equal(t, []string{"first:hi", "second:hi", "branch:main"}, got)

	// No subscriber for this type: nothing ran, caller keeps ownership.
	assert.False(t, rt.Dispatch(controller.SubmitMsg{Text: "hi"}))
	assert.Len(t, got, 3)
}

func TestSubscribeRejectsInterfaceType(t *testing.T) {
	rt := New()
	assert.PanicsWithValue(t,
		"runtime: Subscribe needs a concrete msg type, not an interface",
		func() { Subscribe[controller.Msg](rt, func(controller.Msg) {}) },
	)
}

func TestOverlaysOrderByZAndSkipInactive(t *testing.T) {
	rt := New()
	ctx := components.DrawContext{
		Max:    components.Size{Width: 80, Height: 24},
		Method: xui.WidthUnicode,
	}
	assert.Nil(t, rt.Overlays(ctx), "no overlays registered, nothing drawn")

	rt.AddOverlay(40, func(components.DrawContext) (components.Surface, bool) {
		return components.NewSurface(10, 2, nil), true
	})
	rt.AddOverlay(10, func(components.DrawContext) (components.Surface, bool) {
		return components.Surface{}, false
	})
	rt.AddOverlay(40, func(components.DrawContext) (components.Surface, bool) {
		return components.NewSurface(6, 1, nil), true
	})

	got := rt.Overlays(ctx)
	require.Len(t, got, 2)
	// Same z keeps registration order (stable sort); z=10 was inactive and skipped.
	assert.Equal(t, 40, got[0].Z)
	assert.Equal(t, components.Size{Width: 10, Height: 2}, got[0].Surface.Size)
	assert.Equal(t, 40, got[1].Z)
	assert.Equal(t, components.Size{Width: 6, Height: 1}, got[1].Surface.Size)
	assert.Equal(t, components.Point{X: 0, Y: 0}, got[0].Origin)
}

func TestThemeFanOutInRegistrationOrder(t *testing.T) {
	rt := New()
	var got []components.Theme
	rt.OnTheme(func(th components.Theme) { got = append(got, th) })
	rt.OnTheme(func(th components.Theme) { got = append(got, th) })

	want := components.DefaultTheme()
	rt.ApplyTheme(want)
	assert.Equal(t, []components.Theme{want, want}, got)
}

type stubPlugin struct{ registered *Runtime }

func (p *stubPlugin) Register(rt *Runtime) { p.registered = rt }

func TestRegisterInvokesPlugin(t *testing.T) {
	rt := New()
	p := &stubPlugin{}
	rt.Register(p)
	assert.Same(t, rt, p.registered)
}
