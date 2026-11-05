package splash

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestSphereDrawFillsEllipse(t *testing.T) {
	sphere := &Sphere{Width: 20, Height: 20, Time: 0.5}
	surf := sphere.Draw(components.DrawContext{
		Max:    components.Size{Width: 20, Height: 20},
		Method: xui.WidthUnicode,
	})
	require.Equal(t, 20, surf.Size.Width, "surface width")
	require.Equal(t, 20, surf.Size.Height, "surface height")
	nonEmpty := 0
	for _, c := range surf.Buffer {
		if c.Char != "" && c.Char != " " {
			nonEmpty++
		}
	}
	require.GreaterOrEqual(t, nonEmpty, 40, "expected sphere cells")
}

func TestGradient(t *testing.T) {
	stops := []rgb{{0, 0, 0}, {100, 200, 50}, {200, 100, 0}}
	require.Equal(t, rgb{0, 0, 0}, gradient(stops, -1), "clamps below zero")
	require.Equal(t, rgb{200, 100, 0}, gradient(stops, 1.5), "clamps above one")
	require.Equal(t, rgb{50, 100, 25}, gradient(stops, 0.25), "first segment midpoint")
	require.Equal(t, rgb{150, 150, 25}, gradient(stops, 0.75), "second segment midpoint")
}

func TestSpherePalette(t *testing.T) {
	sphere := &Sphere{Width: 10, Height: 10, Theme: components.DefaultTheme()}
	sphere.ensure()
	require.Len(t, sphere.colors, paletteLen, "gradient is sampled into full length")

	stops := sphereStops(components.DefaultTheme())
	first, last := stops[0], stops[len(stops)-1]
	require.Equal(t, xui.RGBColor(first.r, first.g, first.b), sphere.colors[0], "dim end")
	require.Equal(t, xui.RGBColor(last.r, last.g, last.b), sphere.colors[paletteLen-1], "bright end")
}
