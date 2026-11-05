package splash

import (
	"math"
	"slices"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// Classic sphere charset.
const sphereCharset = " .:-=+*#%@"

// paletteLen is how many color steps a gradient is sampled into.
const paletteLen = 64

// defaultSphereStops is the fallback gradient when the theme carries no usable
// color: dim end first, bright end last.
var defaultSphereStops = []rgb{{0, 45, 55}, {0, 190, 175}, {150, 255, 235}}

type rgb struct{ r, g, b uint8 }

func lerpRGB(a, b rgb, t float64) rgb {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return rgb{
		r: uint8(math.Round(float64(a.r) + (float64(b.r)-float64(a.r))*t)),
		g: uint8(math.Round(float64(a.g) + (float64(b.g)-float64(a.g))*t)),
		b: uint8(math.Round(float64(a.b) + (float64(b.b)-float64(a.b))*t)),
	}
}

// gradient samples a multi-stop linear gradient at t ∈ [0,1].
func gradient(stops []rgb, t float64) rgb {
	if t <= 0 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	seg := t * float64(len(stops)-1)
	i := int(seg)
	return lerpRGB(stops[i], stops[i+1], seg-float64(i))
}

// buildPalette samples a gradient into paletteLen color steps.
func buildPalette(stops []rgb) []xui.Color {
	colors := make([]xui.Color, paletteLen)
	for i := range colors {
		c := gradient(stops, float64(i)/float64(paletteLen-1))
		colors[i] = xui.RGBColor(c.r, c.g, c.b)
	}
	return colors
}

// Sphere is an ASCII sphere for the splash screen.
// Drive animation by advancing Time (seconds) each frame with App.Anim.
type Sphere struct {
	Width  int // default 40
	Height int // default 40
	Time   float64
	// Fast speeds noise scroll (1.8x when true, else 1x).
	Fast bool
	// Theme tints the gradient; the zero Theme and Dark keep the curated teal.
	Theme components.Theme

	noise  glowNoise
	stops  []rgb
	colors []xui.Color
}

func (o *Sphere) ensure() {
	if o.Width <= 0 {
		o.Width = 40
	}
	if o.Height <= 0 {
		o.Height = 40
	}
	if o.noise.seed == 0 {
		o.noise = newGlowNoise(2654435761)
	}
	// Rebuild only on an actual theme change: the palette is 64 colors and
	// Draw runs every animation frame.
	if stops := sphereStops(o.Theme); !slices.Equal(stops, o.stops) {
		o.stops = stops
		o.colors = buildPalette(stops)
	}
}

// Handle is a no-op; the sphere animates via Time, not input.
func (*Sphere) Handle(_ *components.EventContext, _ xui.Event) {}

// Draw renders the noise-lit ASCII sphere into a surface.
func (o *Sphere) Draw(ctx components.DrawContext) components.Surface {
	o.ensure()
	w, h := o.Width, o.Height
	if maxW := ctx.Max.Width; maxW > 0 && w > maxW {
		w = maxW
	}
	if maxH := ctx.Max.Height; maxH > 0 && h > maxH {
		h = maxH
	}
	if w < 3 {
		w = 3
	}
	if h < 3 {
		h = 3
	}

	s := components.NewSurface(w, h, o)

	// Aspect correction: terminal cells are roughly twice as tall as wide.
	const kx = 0.5
	cx := float64(w) / 2
	cy := float64(h) / 2
	rx := math.Max(1, float64(w)/2-1)
	ry := math.Max(1, float64(h)/(2*kx)-1)
	radius := math.Min(rx, ry)
	r2 := radius * radius
	invR2 := 1 / r2
	invKx := 1 / kx

	speed := 1.0
	if o.Fast {
		speed = 1.8
	}
	chars := []rune(sphereCharset)
	nChars := len(chars)
	nPal := len(o.colors)

	for row := 0; row < h; row++ {
		py := (float64(row) - cy) * invKx
		py2 := py * py
		if py2 >= r2 {
			continue
		}
		half := math.Sqrt(r2 - py2)
		x0 := int(math.Max(0, math.Floor(cx-half)))
		x1 := int(math.Min(float64(w-1), math.Ceil(cx+half)))
		for col := x0; col <= x1; col++ {
			px := float64(col) - cx
			d2 := px*px + py2
			if d2 >= r2 {
				continue
			}
			falloff := 1 - d2*invR2
			g := o.noise.sample(float64(col), float64(row), o.Time, speed) * falloff
			if g <= 0 {
				continue
			}
			gi := int(math.Min(float64(nChars-1), math.Floor(g*float64(nChars))))
			ci := int(math.Min(float64(nPal-1), math.Floor(g*float64(nPal))))
			s.SetCell(col, row, xui.Cell{
				Char:  string(chars[gi]),
				Width: 1,
				Style: xui.Style{Fg: o.colors[ci]},
			})
		}
	}
	return s
}
