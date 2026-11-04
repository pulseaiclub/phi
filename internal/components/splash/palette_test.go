package splash

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestSpherePaletteKeepsDarkThemeTeal(t *testing.T) {
	require.Equal(t, defaultSphereStops, sphereStops(components.DarkTheme()),
		"product Dark theme keeps its curated gradient")
}

func TestSpherePaletteFollowsTheme(t *testing.T) {
	sphere := &Sphere{Width: 10, Height: 10}
	sphere.ensure()
	dark := slices.Clone(sphere.colors)

	sphere.Theme = components.PinkTheme()
	sphere.ensure()
	require.NotEqual(t, dark, sphere.colors, "theme switch rebuilds the palette")

	bright := sphere.colors[paletteLen-1]
	require.Greater(t, bright.R, bright.B, "pink identity is red dominant")

	sphere.Theme = components.TerminalTheme()
	sphere.ensure()
	bright = sphere.colors[paletteLen-1]
	require.Greater(t, bright.G, bright.R, "terminal cyan identity lifts green")
	require.Greater(t, bright.B, bright.R, "terminal cyan identity lifts blue")
}

func TestSpherePaletteCachedUntilThemeChanges(t *testing.T) {
	sphere := &Sphere{Width: 10, Height: 10}
	sphere.ensure()
	palette := sphere.colors

	sphere.ensure()
	require.Same(t, &palette[0], &sphere.colors[0], "unchanged theme keeps the cached palette")
}

func TestSpherePaletteFallsBackWithoutThemeColor(t *testing.T) {
	require.Equal(t, defaultSphereStops, sphereStops(components.Theme{}), "zero theme keeps product teal")

	_, ok := rgbOf(xui.IndexedColor(200))
	require.False(t, ok, "high indexed colors are not guessed")

	// Themes that only set body text still get a themed sphere.
	stops := sphereStops(components.Theme{Foreground: xui.Style{Fg: xui.RGBColor(0x80, 0x40, 0x20)}})
	require.NotEqual(t, defaultSphereStops, stops, "foreground is the last accent fallback")
}
