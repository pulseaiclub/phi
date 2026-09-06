package commands

import "github.com/pulseaiclub/phi/internal/components/palette"

func ThinkingCommand(set func(bool)) palette.PaletteCommand {
	return palette.PaletteCommand{
		ID: "settings-thinking", Noun: "settings", Verb: "thinking",
		Keywords:     []string{"thinking", "reasoning", "collapse", "expand"},
		SubmenuTitle: "Thinking visibility",
		Submenu: []palette.PaletteCommand{
			{ID: "thinking-collapsed", Verb: "collapsed — hide reasoning by default", Run: func() {
				if set != nil {
					set(false)
				}
			}},
			{ID: "thinking-expanded", Verb: "expanded — show reasoning by default", Run: func() {
				if set != nil {
					set(true)
				}
			}},
		},
	}
}
