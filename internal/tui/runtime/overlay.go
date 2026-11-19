package runtime

import (
	"slices"

	"github.com/pulseaiclub/phi/internal/components"
)

// OverlayStack owns the z-ordered overlay surfaces layered above the root
// layout (the toast lives here).
type OverlayStack struct {
	entries []overlayEntry
}

// OverlayDraw contributes one full-surface layer above the root layout;
// ok=false skips the frame (nothing to draw).
type OverlayDraw func(ctx components.DrawContext) (components.Surface, bool)

type overlayEntry struct {
	z    int
	draw OverlayDraw
}

// Add registers a draw hook layered above the root layout. Higher z wins;
// ties keep registration order.
func (os *OverlayStack) Add(z int, draw OverlayDraw) {
	os.entries = append(os.entries, overlayEntry{z: z, draw: draw})
}

// Draw renders every active overlay contribution, ascending by z. The caller
// appends the returned subsurfaces to its root surface.
func (os *OverlayStack) Draw(ctx components.DrawContext) []components.SubSurface {
	if len(os.entries) == 0 {
		return nil
	}
	entries := slices.Clone(os.entries)
	slices.SortStableFunc(entries, func(a, b overlayEntry) int { return a.z - b.z })
	var out []components.SubSurface
	for _, e := range entries {
		if surf, ok := e.draw(ctx); ok {
			out = append(out, components.SubSurface{
				Origin:  components.Point{X: 0, Y: 0},
				Surface: surf,
				Z:       e.z,
			})
		}
	}
	return out
}
