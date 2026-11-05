package splash

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// ansi16 holds the xterm value of palette indices 0-15. Themes that stay on
// ANSI colors (Terminal) must still light the sphere, and indices above 15 are
// the terminal's own business — we fall back rather than guess.
var ansi16 = [16]rgb{
	{0x00, 0x00, 0x00},
	{0x80, 0x00, 0x00},
	{0x00, 0x80, 0x00},
	{0x80, 0x80, 0x00},
	{0x00, 0x00, 0x80},
	{0x80, 0x00, 0x80},
	{0x00, 0x80, 0x80},
	{0xc0, 0xc0, 0xc0},
	{0x80, 0x80, 0x80},
	{0xff, 0x00, 0x00},
	{0x00, 0xff, 0x00},
	{0xff, 0xff, 0x00},
	{0x00, 0x00, 0xff},
	{0xff, 0x00, 0xff},
	{0x00, 0xff, 0xff},
	{0xff, 0xff, 0xff},
}

// rgbOf resolves a theme color to RGB. Default and high indexed colors report
// false so callers can fall back instead of inventing a hue.
func rgbOf(c xui.Color) (rgb, bool) {
	switch c.Kind {
	case xui.ColorRGB:
		return rgb{r: c.R, g: c.G, b: c.B}, true
	case xui.ColorIndex:
		if int(c.Index) < len(ansi16) {
			return ansi16[c.Index], true
		}
	}
	return rgb{}, false
}

// sphereAccent picks the color the sphere is made of: Identity (who/where, the
// role the brand line leans on), then ToolName, then body text.
func sphereAccent(th components.Theme) (rgb, bool) {
	for _, st := range []xui.Style{th.Identity, th.ToolName, th.Foreground} {
		if c, ok := rgbOf(st.Fg); ok {
			return c, true
		}
	}
	return rgb{}, false
}

// darkSphereAccent is DarkTheme's identity teal. That gradient was hand-tuned,
// so deriving it again from the same hue would only dull it.
var darkSphereAccent = func() rgb {
	c, _ := rgbOf(components.DarkTheme().Identity.Fg)
	return c
}()

// sphereStops picks the sphere gradient: the curated teal for the product Dark
// theme (and for themes with no usable color), otherwise three stops derived
// from the theme accent — shadowed → accent → highlight — so the noise still
// reads as a lit sphere in every palette. Read-only: callers must not mutate
// the result.
func sphereStops(th components.Theme) []rgb {
	accent, ok := sphereAccent(th)
	if !ok || accent == darkSphereAccent {
		return defaultSphereStops
	}
	return []rgb{
		lerpRGB(accent, rgb{}, 0.78),
		lerpRGB(accent, rgb{0xff, 0xff, 0xff}, 0.10),
		lerpRGB(accent, rgb{0xff, 0xff, 0xff}, 0.62),
	}
}
