package components

import (
	"strings"

	"github.com/pulseaiclub/xui"
)

// Theme holds semantic colors for the TUI.
//
// One job per role — do not overload:
//
//	Foreground — body text
//	Muted      — secondary copy / hints (may Dim)
//	Border     — quiet box edges
//	Title      — panel titles, md headings (structure, never alarm)
//	Identity   — who/where: model, user rule, bash "$" (≠ Success)
//	ToolName   — cool action accent: tools, keybinds, busy, paths, inline code
//	Accent     — interactive links / "Show more" only
//	Success / Warning / Destructive — outcomes and risk only
//	Selection* / Command — palette selection chrome
type Theme struct {
	Foreground  xui.Style
	Muted       xui.Style
	Success     xui.Style
	Identity    xui.Style
	Title       xui.Style
	Accent      xui.Style
	Warning     xui.Style
	Destructive xui.Style
	Border      xui.Style
	ToolName    xui.Style
	SelectionBg xui.Style
	SelectionFg xui.Style
	Command     xui.Style // palette command verbs; equals Title
}

// ThemeNames lists builtin theme display names in picker order (default first).
func ThemeNames() []string {
	return []string{"Dark", "Darcula", "Pink", "Terminal"}
}

// DefaultTheme returns the curated Dark palette (product default).
func DefaultTheme() Theme { return DarkTheme() }

// DefaultThemeName is the display name of DefaultTheme. It is ThemeNames()[0]
// (default first), so DefaultTheme() round-trips through ThemeByName — the
// theme picker relies on it to mark the startup theme before any switch.
func DefaultThemeName() string { return ThemeNames()[0] }

// DarkTheme is Phi's curated dark palette — cool info, teal identity, mint success.
func DarkTheme() Theme {
	info := xui.Style{Fg: xui.RGBColor(0x7d, 0xc3, 0xff)}
	title := xui.Style{Fg: xui.RGBColor(0xd0, 0xd4, 0xdc)}
	return Theme{
		Foreground:  xui.Style{Fg: xui.DefaultColor()},
		Muted:       xui.Style{Fg: xui.IndexedColor(245), Dim: true},
		Success:     xui.Style{Fg: xui.RGBColor(0x7d, 0xc3, 0xa0), Bold: true},
		Identity:    xui.Style{Fg: xui.RGBColor(0x5e, 0xc4, 0xb4), Bold: true}, // teal ≠ success mint
		Title:       title,
		Accent:      xui.Style{Fg: xui.RGBColor(0xc4, 0x8a, 0xd9), Underline: true},
		Warning:     xui.Style{Fg: xui.RGBColor(0xe5, 0xc0, 0x7b)},
		Destructive: xui.Style{Fg: xui.RGBColor(0xe0, 0x6c, 0x75)},
		// Bright silver (250): the composer frame must read as a solid line,
		// not a hairline — it intentionally sits above the dimmed Muted(245)
		// labels it frames.
		Border:      xui.Style{Fg: xui.IndexedColor(250)},
		ToolName:    info,
		SelectionBg: xui.Style{Bg: xui.RGBColor(0x2a, 0x3f, 0x5f)},
		SelectionFg: xui.Style{Fg: xui.RGBColor(0xf0, 0xf3, 0xf8), Bold: true},
		Command:     title,
	}
}

// DarculaTheme follows IntelliJ Darcula temperatures with Phi role separation.
func DarculaTheme() Theme {
	info := xui.Style{Fg: xui.RGBColor(0x68, 0x97, 0xbb)}
	title := xui.Style{Fg: xui.RGBColor(0xbb, 0xbb, 0xbb)}
	return Theme{
		Foreground:  xui.Style{Fg: xui.RGBColor(0xa9, 0xb7, 0xc6)},
		Muted:       xui.Style{Fg: xui.RGBColor(0x80, 0x80, 0x80), Dim: true},
		Success:     xui.Style{Fg: xui.RGBColor(0x6a, 0x87, 0x59), Bold: true},
		Identity:    xui.Style{Fg: xui.RGBColor(0x52, 0x94, 0xe2), Bold: true}, // blue identity ≠ olive success
		Title:       title,
		Accent:      xui.Style{Fg: xui.RGBColor(0x58, 0x9d, 0xf6), Underline: true},
		Warning:     xui.Style{Fg: xui.RGBColor(0xcc, 0x78, 0x32)},
		Destructive: xui.Style{Fg: xui.RGBColor(0xff, 0x6b, 0x68)},
		// Same one-step gap under Muted(0x80) as DarkTheme: 0x55 was a hairline.
		Border:      xui.Style{Fg: xui.RGBColor(0x76, 0x76, 0x76)},
		ToolName:    info,
		SelectionBg: xui.Style{Bg: xui.RGBColor(0x21, 0x42, 0x83)},
		SelectionFg: xui.Style{Fg: xui.RGBColor(0xff, 0xff, 0xff), Bold: true},
		Command:     title,
	}
}

// PinkTheme is a sakura blush palette — pink identity, cool tools, mint success.
func PinkTheme() Theme {
	info := xui.Style{Fg: xui.RGBColor(0xb8, 0xa0, 0xe8)} // soft lavender for tools
	title := xui.Style{Fg: xui.RGBColor(0xe8, 0xc8, 0xd8)}
	return Theme{
		Foreground:  xui.Style{Fg: xui.DefaultColor()},
		Muted:       xui.Style{Fg: xui.RGBColor(0xc8, 0xa0, 0xb4), Dim: true},
		Success:     xui.Style{Fg: xui.RGBColor(0x9e, 0xd4, 0xb8), Bold: true},
		Identity:    xui.Style{Fg: xui.RGBColor(0xf0, 0xa8, 0xd0), Bold: true},
		Title:       title,
		Accent:      xui.Style{Fg: xui.RGBColor(0xff, 0x9e, 0xc8), Underline: true},
		Warning:     xui.Style{Fg: xui.RGBColor(0xff, 0xb8, 0x9a)},
		Destructive: xui.Style{Fg: xui.RGBColor(0xf0, 0x6a, 0x8a)},
		Border:      xui.Style{Fg: xui.RGBColor(0x8a, 0x5a, 0x70)},
		ToolName:    info,
		SelectionBg: xui.Style{Bg: xui.RGBColor(0xff, 0x9e, 0xc0)},
		SelectionFg: xui.Style{Fg: xui.RGBColor(0x2a, 0x10, 0x1c), Bold: true},
		Command:     title,
	}
}

// TerminalTheme follows ANSI / default terminal colors (compatibility theme).
func TerminalTheme() Theme {
	info := xui.Style{Fg: xui.IndexedColor(4)} // blue
	title := xui.Style{Fg: xui.DefaultColor()} // bold applied at call sites
	return Theme{
		Foreground:  xui.Style{Fg: xui.DefaultColor()},
		Muted:       xui.Style{Fg: xui.IndexedColor(8), Dim: true},
		Success:     xui.Style{Fg: xui.IndexedColor(2), Bold: true},
		Identity:    xui.Style{Fg: xui.IndexedColor(6), Bold: true}, // cyan ≠ green success
		Title:       title,
		Accent:      xui.Style{Fg: xui.IndexedColor(5), Underline: true},
		Warning:     xui.Style{Fg: xui.IndexedColor(3)},
		Destructive: xui.Style{Fg: xui.IndexedColor(1)},
		Border:      xui.Style{Fg: xui.IndexedColor(8)},
		ToolName:    info,
		SelectionBg: xui.Style{Bg: xui.IndexedColor(4)},
		SelectionFg: xui.Style{Fg: xui.IndexedColor(0), Bold: true},
		Command:     title,
	}
}

// ThemeByName resolves a theme by display name (case-insensitive).
func ThemeByName(name string) (Theme, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "dark":
		return DarkTheme(), true
	case "darcula", "dura":
		return DarculaTheme(), true
	case "pink", "sakura":
		return PinkTheme(), true
	case "terminal":
		return TerminalTheme(), true
	default:
		return Theme{}, false
	}
}

// IdentityOrSuccess returns Identity when set, else Success (legacy / zero Theme).
func (th Theme) IdentityOrSuccess() xui.Style {
	if th.Identity.Fg.Kind != 0 {
		return th.Identity
	}
	return th.Success
}

// TitleOrForeground returns Title when set, else bold Foreground.
func (th Theme) TitleOrForeground() xui.Style {
	if th.Title.Fg.Kind != 0 {
		st := th.Title
		st.Bold = true
		return st
	}
	st := th.Foreground
	st.Bold = true
	return st
}
