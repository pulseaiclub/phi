package commands

import (
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/llm/skills"
)

// SkillsCommands owns the skills palette command.
type SkillsCommands struct {
	SkillPaths []string
	Add        skillAdder
}

// skillAdder adds a pending skill to the composer.
type skillAdder interface {
	AddPendingSkill(name string)
}

// Register wires the skills palette entry into r.
func (s *SkillsCommands) Register(r *CommandRegistry) {
	if s == nil || r == nil {
		return
	}
	r.Register(Command{
		Name: "skills",
		Build: func(_ Context) palette.PaletteCommand {
			return buildSkillsPalette(s.SkillPaths, s.add)
		},
	})
}

func (s *SkillsCommands) add(name string) {
	if s == nil || s.Add == nil {
		return
	}
	s.Add.AddPendingSkill(name)
}

func buildSkillsPalette(skillPaths []string, add func(string)) palette.PaletteCommand {
	submenu := skillSubcommands(skillPaths, add)
	return palette.PaletteCommand{
		ID:           "skills",
		Noun:         "skills",
		Verb:         "invoke",
		Keywords:     []string{"skill", "use skill", "load skill", "pending"},
		SubmenuTitle: "Select skill",
		Submenu:      submenu,
	}
}

func skillSubcommands(skillPaths []string, add func(string)) []palette.PaletteCommand {
	list := skills.LoadAll(skillPaths)
	if len(list) == 0 {
		return []palette.PaletteCommand{{
			ID:       "skills-empty",
			Verb:     "No skills found",
			Disabled: true,
		}}
	}

	out := make([]palette.PaletteCommand, 0, len(list))
	for _, sk := range list {
		name := skills.DisplayName(sk)
		out = append(out, palette.PaletteCommand{
			ID:       "skill-" + name,
			Verb:     name,
			Keywords: []string{sk.Description, "skill"},
			Run: func() {
				if add != nil {
					add(name)
				}
			},
		})
	}
	return out
}
