package runtime

import (
	"strings"
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

	assert.True(t, rt.Msgs.Dispatch(controller.ToastMsg{Message: "hi"}))
	assert.Equal(t, []string{"first:hi", "second:hi"}, got)

	assert.True(t, rt.Msgs.Dispatch(controller.BranchLabelMsg{Text: "main"}))
	assert.Equal(t, []string{"first:hi", "second:hi", "branch:main"}, got)

	// No subscriber for this type: nothing ran, caller keeps ownership.
	assert.False(t, rt.Msgs.Dispatch(controller.SubmitMsg{Text: "hi"}))
	assert.Len(t, got, 3)
}

func TestNewWiresEveryComponent(t *testing.T) {
	rt := New()
	assert.NotNil(t, rt.Msgs)
	assert.NotNil(t, rt.Submit)
	assert.NotNil(t, rt.Layers)
	assert.NotNil(t, rt.Panes)
	assert.NotNil(t, rt.Clock)
	assert.NotNil(t, rt.Theme)
}

func TestDispatchBatchRunsPostludesOnlyWhenMarked(t *testing.T) {
	rt := New()
	var order []string
	Subscribe(rt, func(controller.SessionEventMsg) {
		order = append(order, "handler")
		rt.Msgs.MarkBatchEvent()
	})
	rt.Msgs.OnBatchStart(func() { order = append(order, "start") })
	rt.Msgs.OnBatchEnd(func() { order = append(order, "end") })

	rt.Msgs.DispatchBatch([]controller.Msg{controller.SessionEventMsg{}})
	assert.Equal(t, []string{"start", "handler", "end"}, order)

	// An unmarked batch runs the start hook but no postlude.
	order = nil
	rt.Msgs.DispatchBatch([]controller.Msg{controller.BranchLabelMsg{}})
	assert.Equal(t, []string{"start"}, order)

	// Empty batches are a no-op.
	rt.Msgs.DispatchBatch(nil)
	assert.Equal(t, []string{"start"}, order)
}

func TestSubmitChainFirstClaimWins(t *testing.T) {
	rt := New()
	var seen []string
	rt.Submit.Add(func(text string) bool { seen = append(seen, "a:"+text); return false })
	rt.Submit.Add(func(text string) bool { seen = append(seen, "b:"+text); return strings.HasPrefix(text, "!") })
	rt.Submit.Add(func(text string) bool { seen = append(seen, "c:"+text); return false })

	assert.True(t, rt.Submit.Filter("!ls"))
	assert.Equal(t, []string{"a:!ls", "b:!ls"}, seen)

	// Nobody claims this line: the chain reports false and every filter saw it.
	assert.False(t, rt.Submit.Filter("hello"))
	assert.Equal(t, []string{"a:!ls", "b:!ls", "a:hello", "b:hello", "c:hello"}, seen)
}

func TestFrameClockTicksInRegistrationOrder(t *testing.T) {
	rt := New()
	var got []string
	rt.Clock.On(func() { got = append(got, "first") })
	rt.Clock.On(func() { got = append(got, "second") })

	rt.Clock.Tick()
	assert.Equal(t, []string{"first", "second"}, got)
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
	assert.Nil(t, rt.Layers.Draw(ctx), "no overlays registered, nothing drawn")

	rt.Layers.Add(40, func(components.DrawContext) (components.Surface, bool) {
		return components.NewSurface(10, 2, nil), true
	})
	rt.Layers.Add(10, func(components.DrawContext) (components.Surface, bool) {
		return components.Surface{}, false
	})
	rt.Layers.Add(40, func(components.DrawContext) (components.Surface, bool) {
		return components.NewSurface(6, 1, nil), true
	})

	got := rt.Layers.Draw(ctx)
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
	rt.Theme.On(func(th components.Theme) { got = append(got, th) })
	rt.Theme.On(func(th components.Theme) { got = append(got, th) })

	want := components.DefaultTheme()
	rt.Theme.Apply(want)
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
