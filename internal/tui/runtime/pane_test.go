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
func (p *stubFullPane) Close()                                   { p.active = false }
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
	rt.Panes.AddFull(first)
	rt.Panes.AddFull(second)

	got, ok := rt.Panes.Active()
	require.True(t, ok)
	assert.Same(t, second, got, "first pane is inactive, second wins")

	rt.Panes.CloseOthers(first)
	_, ok = rt.Panes.Active()
	assert.False(t, ok, "CloseFullPanesExcept hides every other pane")
}

func TestBottomPaneHeightDrawAndKeys(t *testing.T) {
	rt := New()
	inactive := &stubBottomPane{}
	active := &stubBottomPane{active: true, handles: true}
	rt.Panes.AddBottom(inactive)
	rt.Panes.AddBottom(active)
	ctx := &components.EventContext{}

	h, ok := rt.Panes.Height(80, xui.WidthUnicode)
	assert.True(t, ok)
	assert.Equal(t, 8, h)

	_, ok = rt.Panes.Draw(components.DrawContext{}, 80, 8)
	assert.True(t, ok)

	assert.True(t, rt.Panes.Key(ctx, xui.KeyEvent{Press: true}))
	assert.Equal(t, 1, active.keyCalls)
	assert.Equal(t, 1, inactive.keyCalls, "inactive pane is consulted, then skipped")
}

func TestBlocksComposerCoversBothPaneKinds(t *testing.T) {
	rt := New()
	assert.False(t, rt.Panes.BlocksComposer())

	rt.Panes.AddFull(&stubFullPane{active: true})
	assert.True(t, rt.Panes.BlocksComposer())

	rt = New()
	rt.Panes.AddBottom(&stubBottomPane{active: true})
	assert.True(t, rt.Panes.BlocksComposer())
}
