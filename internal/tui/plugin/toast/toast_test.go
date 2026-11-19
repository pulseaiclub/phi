package toast

import (
	"testing"
	"time"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	uistoast "github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
)

func newTestPlugin(t *testing.T, now func() time.Time) (*Plugin, *runtime.Runtime) {
	t.Helper()
	p := New(components.DefaultTheme(), WithClock(now))
	rt := runtime.New()
	rt.Register(p)
	return p, rt
}

func drawCtx() components.DrawContext {
	return components.DrawContext{
		Max:    components.Size{Width: 80, Height: 24},
		Method: xui.WidthUnicode,
	}
}

func TestToastMsgShowsToast(t *testing.T) {
	now := time.Unix(0, 0)
	p, rt := newTestPlugin(t, func() time.Time { return now })

	rt.Msgs.Dispatch(controller.ToastMsg{Message: "saved", Kind: uistoast.ToastSuccess, Duration: 3 * time.Second})

	cur := p.Current()
	assert.Equal(t, "saved", cur.Message)
	assert.Equal(t, uistoast.ToastSuccess, cur.Kind)
	assert.Equal(t, now.Add(3*time.Second), cur.Until)
}

func TestShowZeroDurationUsesDefault(t *testing.T) {
	now := time.Unix(0, 0)
	p, _ := newTestPlugin(t, func() time.Time { return now })

	p.Show("hi", uistoast.ToastWarning, 0)
	assert.Equal(t, now.Add(2*time.Second), p.Current().Until)
}

func TestVisibleHonorsInjectedClock(t *testing.T) {
	now := time.Unix(0, 0)
	clock := now
	p, _ := newTestPlugin(t, func() time.Time { return clock })

	assert.False(t, p.Visible(), "empty toast is never visible")
	p.Show("hi", uistoast.ToastSuccess, time.Second)
	assert.True(t, p.Visible())
	clock = now.Add(time.Second)
	assert.False(t, p.Visible(), "toast expires exactly when Until passes")
}

func TestDrawSkipsHiddenToastAndRendersVisible(t *testing.T) {
	now := time.Unix(0, 0)
	clock := now
	p, rt := newTestPlugin(t, func() time.Time { return clock })
	ctx := drawCtx()

	layers := rt.Layers.Draw(ctx)
	assert.Empty(t, layers, "hidden toast contributes no layer")

	p.Show("hello toast", uistoast.ToastSuccess, time.Second)
	layers = rt.Layers.Draw(ctx)
	require.Len(t, layers, 1)
	assert.Equal(t, zToast, layers[0].Z)
	assert.Contains(t, components.SurfaceText(layers[0].Surface), "hello toast")
}

func TestSetThemeAppliesToState(t *testing.T) {
	now := time.Unix(0, 0)
	p, rt := newTestPlugin(t, func() time.Time { return now })

	th := components.DefaultTheme()
	rt.Theme.Apply(th)
	assert.Equal(t, th, p.Current().Theme)
}
