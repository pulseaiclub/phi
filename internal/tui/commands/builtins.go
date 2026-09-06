package commands

import (
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/llm/skills"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// NewBuiltinRegistry returns the built-in slash + palette catalog.
func NewBuiltinRegistry() *CommandRegistry {
	r := NewCommandRegistry()
	registerBuiltinCommands(r)
	return r
}

func registerBuiltinCommands(r *CommandRegistry) {
	r.Register(Command{
		Name: "tree", Description: "Navigate branches in the current session", Slash: true, Insert: "/tree",
		Run: func(ctx CommandContext) error { ctx.Bus.Publish(controller.TreeOpenMsg{}); return nil },
		PaletteRoot: func(ctx CommandContext) palette.PaletteCommand {
			return palette.PaletteCommand{
				ID:       "tree",
				Noun:     "session",
				Verb:     "tree",
				Keywords: []string{"branch", "history", "navigate"},
				Run:      func() { ctx.Bus.Publish(controller.TreeOpenMsg{}) },
			}
		},
	})
	r.Register(Command{
		Name:        "sessions",
		Description: "Browse and resume sessions for this directory",
		Slash:       true,
		Insert:      "/sessions",
		Run: func(ctx CommandContext) error {
			if ctx.ShowSessions != nil {
				ctx.ShowSessions()
			}
			return nil
		},
	})
	r.Register(Command{
		Name:        "resume",
		Description: "Resume a session in this directory — /resume <id>",
		Slash:       true,
		NeedsArgs:   true,
		Insert:      "/resume ",
		Run: func(ctx CommandContext) error {
			if len(ctx.Args) < 1 {
				return nil
			}
			if ctx.ResumeSession != nil {
				ctx.ResumeSession(ctx.Args[0])
			}
			return nil
		},
	})
	r.Register(Command{
		Name:        "clear",
		Description: "Start a new empty session",
		Slash:       true,
		Insert:      "/clear",
		Run: func(ctx CommandContext) error {
			if ctx.ClearSession != nil {
				ctx.ClearSession()
			}
			return nil
		},
	})

	r.Register(Command{
		Name: "settings-model",
		PaletteRoot: func(ctx CommandContext) palette.PaletteCommand {
			return modelSettingsCommand(ctx.SetModel, ctx.ModelNames)
		},
	})
	r.Register(Command{
		Name: "settings-theme",
		PaletteRoot: func(ctx CommandContext) palette.PaletteCommand {
			return ThemeCommand(ctx.ApplyTheme)
		},
	})
	r.Register(Command{
		Name: "settings-thinking",
		PaletteRoot: func(ctx CommandContext) palette.PaletteCommand {
			return ThinkingCommand(ctx.SetThinkingExpanded)
		},
	})
	r.Register(Command{
		Name: "settings-permissions",
		PaletteRoot: func(ctx CommandContext) palette.PaletteCommand {
			return PermissionsCommand(ctx.SetPermissions)
		},
	})
	r.Register(Command{
		Name: "settings-agents",
		PaletteRoot: func(ctx CommandContext) palette.PaletteCommand {
			return AgentsCommand(ctx.SetAgents)
		},
	})
	r.Register(Command{
		Name: "extensions",
		PaletteRoot: func(ctx CommandContext) palette.PaletteCommand {
			return ExtensionsCommand(ctx.ListExtensions, ctx.ReloadExtensions, ctx.PushSubmenu)
		},
	})
	r.Register(Command{
		Name: "skills",
		PaletteRoot: func(ctx CommandContext) palette.PaletteCommand {
			return SkillsCommand(ctx.SkillPath, ctx.AddSkill)
		},
	})
}

// modelSettingsCommand returns settings → model submenu.
func modelSettingsCommand(onModel func(name string), modelNames []string) palette.PaletteCommand {
	models := make([]palette.PaletteCommand, 0, len(modelNames))
	for _, name := range modelNames {
		models = append(models, palette.PaletteCommand{
			ID:   "model-" + name,
			Verb: name,
			Run: func() {
				if onModel != nil {
					onModel(name)
				}
			},
		})
	}
	return palette.PaletteCommand{
		ID:           "settings-model",
		Noun:         "settings",
		Verb:         "model",
		Keywords:     []string{"model"},
		SubmenuTitle: "Select Model",
		Submenu:      models,
	}
}

// ThemeCommand returns a settings → theme submenu listing builtin palettes.
func ThemeCommand(apply func(name string)) palette.PaletteCommand {
	names := components.ThemeNames()
	submenu := make([]palette.PaletteCommand, 0, len(names))
	for _, name := range names {
		submenu = append(submenu, palette.PaletteCommand{
			ID:       "theme-" + strings.ToLower(name),
			Verb:     name + " (builtin)",
			Keywords: []string{name, "theme", "color"},
			Run: func() {
				if apply != nil {
					apply(name)
				}
			},
		})
	}
	return palette.PaletteCommand{
		ID:           "settings-theme",
		Noun:         "settings",
		Verb:         "theme",
		Keywords:     []string{"theme", "color", "appearance", "dark", "darcula", "pink"},
		SubmenuTitle: "Select Theme",
		Submenu:      submenu,
	}
}

// PermissionsCommand returns settings → permissions to toggle session bypass.
// bypass=true means no permission prompts (allow all).
func PermissionsCommand(set func(bypass bool)) palette.PaletteCommand {
	return palette.PaletteCommand{
		ID:           "settings-permissions",
		Noun:         "settings",
		Verb:         "permissions",
		Keywords:     []string{"permission", "bypass", "allow all", "ask", "gate", "security"},
		SubmenuTitle: "Permissions",
		Submenu: []palette.PaletteCommand{
			{
				ID:       "permissions-off",
				Verb:     "off — allow all (no prompts)",
				Keywords: []string{"bypass", "disable", "off"},
				Run: func() {
					if set != nil {
						set(true)
					}
				},
			},
			{
				ID:       "permissions-on",
				Verb:     "on — ask before gated tools",
				Keywords: []string{"enable", "ask", "on", "interactive"},
				Run: func() {
					if set != nil {
						set(false)
					}
				},
			},
		},
	}
}

// AgentsCommand returns settings → agents to toggle sub-agent tools.
func AgentsCommand(set func(enabled bool)) palette.PaletteCommand {
	return palette.PaletteCommand{
		ID:           "settings-agents",
		Noun:         "settings",
		Verb:         "agents",
		Keywords:     []string{"agent", "subagent", "spawn", "jobs", "parallel"},
		SubmenuTitle: "Sub-agents",
		Submenu: []palette.PaletteCommand{
			{
				ID:       "agents-on",
				Verb:     "on — register agent_* tools",
				Keywords: []string{"enable", "on", "spawn"},
				Run: func() {
					if set != nil {
						set(true)
					}
				},
			},
			{
				ID:       "agents-off",
				Verb:     "off — no sub-agents (fewer tools)",
				Keywords: []string{"disable", "off"},
				Run: func() {
					if set != nil {
						set(false)
					}
				},
			},
		},
	}
}

// ExtensionsCommand returns extensions → list / reload for the command palette.
func ExtensionsCommand(
	listFn func() []palette.PaletteCommand,
	reload func(),
	push func(title string, cmds []palette.PaletteCommand),
) palette.PaletteCommand {
	return palette.PaletteCommand{
		ID:           "extensions",
		Noun:         "extensions",
		Verb:         "manage",
		Keywords:     []string{"extension", "plugin", "pxb", "reload", "list"},
		SubmenuTitle: "Extensions",
		Submenu: []palette.PaletteCommand{
			{
				ID:       "extensions-list",
				Verb:     "list",
				Keywords: []string{"show", "status", "loaded"},
				Run: func() {
					cmds := []palette.PaletteCommand{{
						ID:       "extensions-list-empty",
						Verb:     "No extensions found",
						Disabled: true,
					}}
					if listFn != nil {
						if built := listFn(); len(built) > 0 {
							cmds = built
						}
					}
					if push != nil {
						push("Extensions on disk", cmds)
					}
				},
			},
			{
				ID:       "extensions-reload",
				Verb:     "reload",
				Keywords: []string{"refresh", "rescan", "discover"},
				Run: func() {
					if reload != nil {
						reload()
					}
				},
			},
		},
	}
}

// ExtensionListEntries builds disabled palette rows from discovery results + warnings.
func ExtensionListEntries(found []extension.Discovered, warns []extension.Warning, err error) []palette.PaletteCommand {
	if err != nil {
		return []palette.PaletteCommand{{
			ID:       "extensions-list-err",
			Verb:     "error: " + err.Error(),
			Disabled: true,
		}}
	}
	out := make([]palette.PaletteCommand, 0, len(found)+len(warns)+1)
	if len(found) == 0 && len(warns) == 0 {
		out = append(out, palette.PaletteCommand{
			ID:       "extensions-list-empty",
			Verb:     "No extensions found",
			Disabled: true,
		})
		return out
	}
	for _, d := range found {
		out = append(out, palette.PaletteCommand{
			ID:       "ext-" + d.ID,
			Verb:     extension.FormatDiscovered(d),
			Keywords: []string{d.ID, d.Source},
			Disabled: true,
		})
	}
	for i, w := range warns {
		out = append(out, palette.PaletteCommand{
			ID:       fmt.Sprintf("extensions-warn-%d", i),
			Verb:     "warn: " + w.String(),
			Keywords: []string{"warning", "error"},
			Disabled: true,
		})
	}
	return out
}

// SkillsCommand returns a top-level "skills" palette entry whose submenu lists
// every skill discovered under skillPath. Selecting one adds it as a pending skill.
func SkillsCommand(skillPath string, add func(name string)) palette.PaletteCommand {
	submenu := skillSubcommands(skillPath, add)
	return palette.PaletteCommand{
		ID:           "skills",
		Noun:         "skills",
		Verb:         "invoke",
		Keywords:     []string{"skill", "use skill", "load skill", "pending"},
		SubmenuTitle: "Select skill",
		Submenu:      submenu,
	}
}

func skillSubcommands(skillPath string, add func(name string)) []palette.PaletteCommand {
	list, err := skills.LoadSkills(skillPath)
	if err != nil || len(list) == 0 {
		return []palette.PaletteCommand{{
			ID:       "skills-empty",
			Verb:     "No skills found",
			Disabled: true,
		}}
	}

	out := make([]palette.PaletteCommand, 0, len(list))
	for _, s := range list {
		name := s.Name
		if strings.TrimSpace(name) == "" {
			name = strings.TrimSpace(s.Path)
		}
		out = append(out, palette.PaletteCommand{
			ID:       "skill-" + name,
			Verb:     name,
			Keywords: []string{s.Description, "skill"},
			Run: func() {
				if add != nil {
					add(name)
				}
			},
		})
	}
	return out
}
