package status

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// Spinner — animated activity indicator. Advance with Tick().
//
// Tool/thinking rows use a 1-cell braille glyph. The composer status
// slot animates a highlight traveling through the activity text so
// older terminals that lack block glyphs still render cleanly.
type Spinner struct {
	Frame  int
	Style  xui.Style
	frames []string
}

const flowTrail = 3

// NewSpinner returns a spinner with a 1-cell braille glyph.
func NewSpinner(style xui.Style) *Spinner {
	return &Spinner{
		Style:  style,
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	}
}

// Tick advances the spinner to the next frame.
func (s *Spinner) Tick() {
	s.Frame++
}

// Handle is a no-op; spinners do not take input.
func (*Spinner) Handle(_ *components.EventContext, _ xui.Event) {}

// Draw renders the current spinner frame as a single cell.
func (s *Spinner) Draw(_ components.DrawContext) components.Surface {
	ch := "⋯"
	if len(s.frames) > 0 {
		ch = s.frames[s.Frame%len(s.frames)]
	}
	st := s.Style
	if st == (xui.Style{}) {
		st = components.DefaultTheme().ToolName
	}
	surf := components.NewSurface(1, 1, s)
	surf.SetCell(0, 0, xui.Cell{Char: ch, Width: 1, Style: st})
	return surf
}

// Glyph returns the current 1-cell spinner character (tool / thinking rows).
func (s *Spinner) Glyph() string {
	if len(s.frames) == 0 {
		return "⋯"
	}
	return s.frames[s.Frame%len(s.frames)]
}

// ForEachFlowCell walks text with a traveling highlight for the status slot.
// lit marks the head/trail graphemes; the rest stay off-style.
func (s *Spinner) ForEachFlowCell(text string, fn func(ch string, lit bool)) {
	clusters := graphemeClusters(text)
	if len(clusters) == 0 {
		return
	}
	head := 0
	if s != nil {
		head = s.Frame % len(clusters)
	}
	for i, ch := range clusters {
		behind := head - i
		if behind < 0 {
			behind += len(clusters)
		}
		fn(ch, behind < flowTrail)
	}
}

func graphemeClusters(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, len(s))
	rest := s
	for rest != "" {
		cluster, _, next := xui.FirstGrapheme(rest, xui.WidthUnicode)
		if cluster == "" {
			break
		}
		out = append(out, cluster)
		rest = next
	}
	return out
}

// ToolStatus mirrors tool status icons in transcript blocks.
type ToolStatus int

// Tool status values for tool rows.
const (
	ToolDone ToolStatus = iota
	ToolRunning
	ToolError
	ToolCancelled
	ToolQueued
	ToolRejected
)
