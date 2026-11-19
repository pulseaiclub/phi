package runtime

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

type stubFullPane struct{ active bool }

func (p *stubFullPane) Active() bool                             { return p.active }
func (*stubFullPane) Handle(*components.EventContext, xui.Event) {}
func (*stubFullPane) Draw(components.DrawContext) components.Surface {
	return components.NewSurface(1, 1, nil)
}

type stubBottomPane struct {
	active   bool
	handles  bool
	keyCalls int
}

func (p *stubBottomPane) PreferredHeight(int, xui.WidthMethod) (int, bool) {
	return 8, p.active
}

func (p *stubBottomPane) DrawBottom(components.DrawContext, int, int) (components.Surface, bool) {
	return components.NewSurface(4, 4, nil), p.active
}
func (p *stubBottomPane) BlocksComposer() bool { return p.active }
func (p *stubBottomPane) HandleKey(*components.EventContext, xui.KeyEvent) bool {
	p.keyCalls++
	return p.handles
}

func TestActiveFullPaneFirstActiveWins(t *testing.T) {
	rt := New()
	first, second := &stubFullPane{}, &stubFullPane{active: true}
	rt.AddFullPane(first)
	rt.AddFullPane(second)

	got, ok := rt.ActiveFullPane()
	require.True(t, ok)
	assert.Same(t, second, got, "first pane is inactive, second wins")

	second.active = false
	_, ok = rt.ActiveFullPane()
	assert.False(t, ok)
}

func TestBottomPaneHeightDrawAndKeys(t *testing.T) {
	rt := New()
	inactive := &stubBottomPane{}
	active := &stubBottomPane{active: true, handles: true}
	rt.AddBottomPane(inactive)
	rt.AddBottomPane(active)
	ctx := &components.EventContext{}

	h, ok := rt.BottomHeight(80, xui.WidthUnicode)
	assert.True(t, ok)
	assert.Equal(t, 8, h)

	_, ok = rt.DrawBottom(components.DrawContext{}, 80, 8)
	assert.True(t, ok)

	assert.True(t, rt.BottomKey(ctx, xui.KeyEvent{Press: true}))
	assert.Equal(t, 1, active.keyCalls)
	assert.Equal(t, 1, inactive.keyCalls, "inactive pane is consulted, then skipped")
}

func TestBlocksComposerCoversBothPaneKinds(t *testing.T) {
	rt := New()
	assert.False(t, rt.BlocksComposer())

	rt.AddFullPane(&stubFullPane{active: true})
	assert.True(t, rt.BlocksComposer())

	rt = New()
	rt.AddBottomPane(&stubBottomPane{active: true})
	assert.True(t, rt.BlocksComposer())
}
