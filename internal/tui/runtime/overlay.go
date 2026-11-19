package runtime

import (
	"slices"

	"github.com/pulseaiclub/phi/internal/components"
)

// OverlayDraw contributes one full-surface layer above the root layout;
// ok=false skips the frame (nothing to draw).
type OverlayDraw func(ctx components.DrawContext) (components.Surface, bool)

type overlayEntry struct {
	z    int
	draw OverlayDraw
}

// AddOverlay registers a draw hook layered above the root layout. Higher z
// wins; ties keep registration order.
func (rt *Runtime) AddOverlay(z int, draw OverlayDraw) {
	rt.overlays = append(rt.overlays, overlayEntry{z: z, draw: draw})
}

// Overlays renders every active overlay contribution, ascending by z. The
// caller appends the returned subsurfaces to its root surface.
func (rt *Runtime) Overlays(ctx components.DrawContext) []components.SubSurface {
	if len(rt.overlays) == 0 {
		return nil
	}
	entries := slices.Clone(rt.overlays)
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
