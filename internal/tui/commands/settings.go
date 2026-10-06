package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// SettingsCommands owns settings-* palette commands (model, theme, permissions, agents).
type SettingsCommands struct {
	Ctrl       *controller.EngineController
	Bus        *controller.Bus
	Composer   settingsComposer
	Footer     settingsFooter
	ModelNames []string
}

// settingsComposer is the subset of the composer pane needed by settings.
type settingsComposer interface {
	SetModelLabel(name, thinkLevel string)
}

// settingsFooter is the subset of the footer needed by settings: the context
// window follows the active model, so a switch must refresh it.
type settingsFooter interface {
	SetContextWindow(window int)
}

// Register wires settings palette entries into r.
func (s *SettingsCommands) Register(r *CommandRegistry) {
	r.Register(Command{
		Name: "settings-model",
		Build: func(_ Context) palette.PaletteCommand {
			return buildModelPalette(s.setModel, s.ModelNames)
		},
	})
	r.Register(Command{
		Name: "settings-theme",
		Build: func(_ Context) palette.PaletteCommand {
			return buildThemePalette(s.applyTheme)
		},
	})
	r.Register(Command{
		Name: "settings-permissions",
		Build: func(_ Context) palette.PaletteCommand {
			return buildPermissionsPalette(s.setPermissions)
		},
	})
	r.Register(Command{
		Name: "settings-agents",
		Build: func(_ Context) palette.PaletteCommand {
			return buildAgentsPalette(s.setAgents, s.setRoleModel, s.ModelNames)
		},
	})
	r.Register(Command{
		Name: "settings-think",
		Build: func(_ Context) palette.PaletteCommand {
			return buildThinkPalette(s.Ctrl, s.setThinkLevel)
		},
	})
}

func (s *SettingsCommands) setModel(name string) {
	if err := s.Ctrl.SetModel(name); err != nil {
		publishToast(s.Bus, err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	if s.Composer != nil {
		s.Composer.SetModelLabel(name, string(s.Ctrl.ThinkLevel()))
	}
	if s.Footer != nil {
		s.Footer.SetContextWindow(s.Ctrl.ContextWindow())
	}
	publishToast(s.Bus, "Model: "+name, toast.ToastSuccess, 2*time.Second)
}

func (s *SettingsCommands) applyTheme(name string) {
	s.Bus.Publish(controller.ThemeMsg{Name: name})
}

func (s *SettingsCommands) setPermissions(bypass bool) {
	s.Ctrl.SetAllowAll(bypass)
	kind := toast.ToastWarning
	msg := "Permissions: on (ask)"
	if bypass {
		kind = toast.ToastSuccess
		msg = "Permissions: off (allow all)"
	}
	publishToast(s.Bus, msg, kind, 3*time.Second)
}

func (s *SettingsCommands) setAgents(enabled bool) {
	s.Ctrl.SetAgentsEnabled(enabled)
	msg := "Sub-agents: off"
	if enabled {
		msg = "Sub-agents: on"
	}
	publishToast(s.Bus, msg, toast.ToastSuccess, 2*time.Second)
}

func (s *SettingsCommands) setRoleModel(role, name string) {
	if err := s.Ctrl.SetRoleModel(role, name); err != nil {
		publishToast(s.Bus, err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	msg := fmt.Sprintf("Sub-agent %s: inherit parent", role)
	if name != "" {
		msg = fmt.Sprintf("Sub-agent %s: %s", role, name)
	}
	publishToast(s.Bus, msg, toast.ToastSuccess, 2*time.Second)
}

func (s *SettingsCommands) setThinkLevel(level llm.ThinkMode) {
	s.Ctrl.SetThinkLevel(level)
	label := string(level)
	if level == llm.Off {
		label = "off"
	}
	if s.Composer != nil {
		s.Composer.SetModelLabel(s.Ctrl.ModelName(), label)
	}
	publishToast(s.Bus, "Thinking: "+label, toast.ToastSuccess, 2*time.Second)
}

func buildThinkPalette(ctrl *controller.EngineController, set func(llm.ThinkMode)) palette.PaletteCommand {
	levels := []llm.ThinkMode{
		llm.Off, llm.Minimal, llm.Low, llm.Medium, llm.High, llm.XHigh, llm.Max,
	}
	current := llm.Off
	if ctrl != nil {
		current = ctrl.ThinkLevel()
	}
	cmds := make([]palette.PaletteCommand, 0, len(levels))
	for _, lv := range levels {
		mark := "  "
		if lv == current {
			mark = "✓ "
		}
		cmds = append(cmds, palette.PaletteCommand{
			ID:       "think-" + string(lv),
			Verb:     mark + string(lv),
			Keywords: []string{string(lv), "thinking", "reasoning", "effort"},
			Run: func() {
				if set != nil {
					set(lv)
				}
			},
		})
	}
	return palette.PaletteCommand{
		ID:           "settings-think",
		Noun:         "settings",
		Verb:         "think",
		Keywords:     []string{"thinking", "reasoning", "effort", "budget"},
		SubmenuTitle: "Thinking Level",
		Submenu:      cmds,
	}
}

func buildModelPalette(onModel func(string), modelNames []string) palette.PaletteCommand {
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

func buildThemePalette(apply func(string)) palette.PaletteCommand {
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

func buildPermissionsPalette(set func(bool)) palette.PaletteCommand {
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

func buildAgentsPalette(
	set func(bool),
	setRoleModel func(string, string),
	modelNames []string,
) palette.PaletteCommand {
	roles := []string{"explore", "review", "worker"}
	roleCmds := make([]palette.PaletteCommand, 0, len(roles))
	for _, role := range roles {
		models := make([]palette.PaletteCommand, 0, len(modelNames)+1)
		models = append(models, palette.PaletteCommand{
			ID:   "agents-model-" + role + "-inherit",
			Verb: "(inherit parent)",
			Run: func() {
				if setRoleModel != nil {
					setRoleModel(role, "")
				}
			},
		})
		for _, name := range modelNames {
			models = append(models, palette.PaletteCommand{
				ID:   "agents-model-" + role + "-" + name,
				Verb: name,
				Run: func() {
					if setRoleModel != nil {
						setRoleModel(role, name)
					}
				},
			})
		}
		roleCmds = append(roleCmds, palette.PaletteCommand{
			ID:           "agents-models-" + role,
			Verb:         role,
			Keywords:     []string{role, "model"},
			SubmenuTitle: "Model for " + role,
			Submenu:      models,
		})
	}
	return palette.PaletteCommand{
		ID:           "settings-agents",
		Noun:         "settings",
		Verb:         "agents",
		Keywords:     []string{"agent", "subagent", "spawn", "jobs", "parallel", "model"},
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
			{
				ID:           "agents-models",
				Verb:         "models",
				Keywords:     []string{"model", "explore", "review", "worker"},
				SubmenuTitle: "Sub-agent Models",
				Submenu:      roleCmds,
			},
		},
	}
}
