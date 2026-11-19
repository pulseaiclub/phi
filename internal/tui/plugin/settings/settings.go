package settings

import (
	"fmt"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// SettingsCommands owns settings-* palette commands (model, theme, agents).
type SettingsCommands struct {
	Ctrl       *controller.EngineController
	Bus        *controller.Bus
	Composer   settingsComposer
	ModelNames []string
	// CurrentTheme is the theme applied through the palette; empty means the
	// startup default, so the picker marks it before the first switch.
	CurrentTheme string
}

// settingsComposer is the subset of the composer pane needed by settings.
type settingsComposer interface {
	SetModelLabel(name, thinkLevel string)
}

// New builds the settings command domain and registers its palette entries.
func New(r *commands.CommandRegistry, deps SettingsCommands) *SettingsCommands {
	s := &deps
	s.Register(r)
	return s
}

// Register wires settings palette entries into r.
func (s *SettingsCommands) Register(r *commands.CommandRegistry) {
	r.Register(commands.Command{
		Name: "settings-model",
		Build: func(_ commands.Context) palette.PaletteCommand {
			return buildModelPalette(s.setModel, s.saveModel, s.ModelNames, s.modelName())
		},
	})
	r.Register(commands.Command{
		Name: "settings-theme",
		Build: func(_ commands.Context) palette.PaletteCommand {
			return buildThemePalette(s.applyTheme, s.currentTheme())
		},
	})
	r.Register(commands.Command{
		Name: "settings-agents",
		Build: func(_ commands.Context) palette.PaletteCommand {
			return buildAgentsPalette(s.setAgents, s.setRoleModel, s.ModelNames, s.agentsEnabled(), s.roleModel)
		},
	})
	r.Register(commands.Command{
		Name: "settings-think",
		Build: func(_ commands.Context) palette.PaletteCommand {
			return buildThinkPalette(s.Ctrl, s.setThinkLevel)
		},
	})
	r.Register(commands.Command{
		Name: "settings-debug",
		Build: func(_ commands.Context) palette.PaletteCommand {
			return buildDebugPalette(s.setDebug)
		},
	})
}

// modelName, agentsEnabled, and roleModel read the live state each
// picker marks as current. A nil controller (constructor-less tests) reads as
// the zero value, so the submenu just shows nothing marked.

func (s *SettingsCommands) modelName() string {
	if s.Ctrl == nil {
		return ""
	}
	return s.Ctrl.ModelName()
}

func (s *SettingsCommands) agentsEnabled() bool { return s.Ctrl != nil && s.Ctrl.AgentsEnabled() }

func (s *SettingsCommands) roleModel(role string) string {
	if s.Ctrl == nil {
		return ""
	}
	return s.Ctrl.RoleModel(role)
}

// currentTheme is the theme the UI is showing: the last palette pick, else the
// startup default.
func (s *SettingsCommands) currentTheme() string {
	if s.CurrentTheme != "" {
		return s.CurrentTheme
	}
	return components.DefaultThemeName()
}

func (s *SettingsCommands) setModel(name string) {
	if err := s.applyModel(name); err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	commands.PublishToast(s.Bus, "Model: "+name, toast.ToastSuccess, 2*time.Second)
}

func (s *SettingsCommands) saveModel(name string) bool {
	if err := s.Ctrl.SaveDefaultModel(name); err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 4*time.Second)
		return false
	}
	if err := s.applyModel(name); err != nil {
		commands.PublishToast(
			s.Bus,
			"Default saved; model switch failed: "+err.Error(),
			toast.ToastError,
			4*time.Second,
		)
		return false
	}
	commands.PublishToast(s.Bus, "Default model: "+name, toast.ToastSuccess, 3*time.Second)
	return true
}

func (s *SettingsCommands) applyModel(name string) error {
	if err := s.Ctrl.SetModel(name); err != nil {
		return err
	}
	if s.Composer != nil {
		s.Composer.SetModelLabel(name, string(s.Ctrl.ThinkLevel()))
	}
	return nil
}

func (s *SettingsCommands) applyTheme(name string) {
	s.CurrentTheme = name
	s.Bus.Publish(controller.ThemeMsg{Name: name})
}

func (s *SettingsCommands) setAgents(enabled bool) {
	if err := s.Ctrl.SetAgentsEnabled(enabled); err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	msg := "Sub-agents: off"
	if enabled {
		msg = "Sub-agents: on"
	}
	commands.PublishToast(s.Bus, msg, toast.ToastSuccess, 2*time.Second)
}

func (s *SettingsCommands) setRoleModel(role, name string) {
	if err := s.Ctrl.SetRoleModel(role, name); err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	msg := fmt.Sprintf("Sub-agent %s: inherit parent", role)
	if name != "" {
		msg = fmt.Sprintf("Sub-agent %s: %s", role, name)
	}
	commands.PublishToast(s.Bus, msg, toast.ToastSuccess, 2*time.Second)
}

func (s *SettingsCommands) setDebug(on bool) {
	debuglog.SetEnabled(on)
	msg := "Debug logging: off"
	if on {
		msg = "Debug logging: on → " + debuglog.FilePath()
	}
	commands.PublishToast(s.Bus, msg, toast.ToastSuccess, 3*time.Second)
}

func (s *SettingsCommands) setThinkLevel(level llm.ThinkMode) {
	if err := s.Ctrl.SetThinkLevel(level); err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	label := string(level)
	if level == llm.Off {
		label = "off"
	}
	if s.Composer != nil {
		s.Composer.SetModelLabel(s.Ctrl.ModelName(), label)
	}
	commands.PublishToast(s.Bus, "Thinking: "+label, toast.ToastSuccess, 2*time.Second)
}

func buildThinkPalette(ctrl *controller.EngineController, set func(llm.ThinkMode)) palette.PaletteCommand {
	levels := llm.AllThinkModes()
	current := llm.Off
	if ctrl != nil {
		current = ctrl.ThinkLevel()
		levels = ctrl.ThinkingModes()
	}
	cmds := make([]palette.PaletteCommand, 0, len(levels))
	for _, lv := range levels {
		mark := chrome.CurrentMarker(lv == current)
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
		Description:  "Set the thinking effort",
		Keywords:     []string{"thinking", "reasoning", "effort", "budget"},
		SubmenuTitle: "Thinking Level",
		Submenu:      cmds,
	}
}

func buildDebugPalette(set func(bool)) palette.PaletteCommand {
	current := debuglog.Enabled()
	mark := func(on bool) string { return chrome.CurrentMarker(on == current) }
	return palette.PaletteCommand{
		ID:           "settings-debug",
		Noun:         "settings",
		Verb:         "debug",
		Description:  "Enable or disable debug logging",
		Keywords:     []string{"debug", "log", "logging", "diagnostics", "troubleshoot", "trace"},
		SubmenuTitle: "Debug Logging",
		Submenu: []palette.PaletteCommand{
			{
				ID:       "debug-on",
				Verb:     mark(true) + "on — write to " + debuglog.FilePath(),
				Keywords: []string{"enable", "on", "start", "record"},
				Run: func() {
					if set != nil {
						set(true)
					}
				},
			},
			{
				ID:       "debug-off",
				Verb:     mark(false) + "off — stop writing",
				Keywords: []string{"disable", "off", "stop"},
				Run: func() {
					if set != nil {
						set(false)
					}
				},
			},
		},
	}
}

func buildModelPalette(
	onModel func(string),
	onSave func(string) bool,
	modelNames []string,
	current string,
) palette.PaletteCommand {
	models := make([]palette.PaletteCommand, 0, len(modelNames))
	for _, name := range modelNames {
		cmd := palette.PaletteCommand{
			ID:   "model-" + name,
			Verb: chrome.CurrentMarker(name == current) + name,
			Run: func() {
				if onModel != nil {
					onModel(name)
				}
			},
		}
		if onSave != nil {
			cmd.OnSave = func() bool { return onSave(name) }
		}
		models = append(models, cmd)
	}
	return palette.PaletteCommand{
		ID:           "settings-model",
		Noun:         "settings",
		Verb:         "model",
		Description:  "Switch the active model",
		Keywords:     []string{"model"},
		SubmenuTitle: "Select Model",
		Submenu:      models,
	}
}

func buildThemePalette(apply func(string), current string) palette.PaletteCommand {
	names := components.ThemeNames()
	submenu := make([]palette.PaletteCommand, 0, len(names))
	for _, name := range names {
		submenu = append(submenu, palette.PaletteCommand{
			ID:       "theme-" + strings.ToLower(name),
			Verb:     chrome.CurrentMarker(strings.EqualFold(name, current)) + name + " (builtin)",
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
		Description:  "Change the color theme",
		Keywords:     []string{"theme", "color", "appearance", "dark", "darcula", "pink"},
		SubmenuTitle: "Select Theme",
		Submenu:      submenu,
	}
}

func buildAgentsPalette(
	set func(bool),
	setRoleModel func(string, string),
	modelNames []string,
	enabled bool,
	roleModel func(string) string,
) palette.PaletteCommand {
	roles := []string{"explore", "review", "worker"}
	roleCmds := make([]palette.PaletteCommand, 0, len(roles))
	for _, role := range roles {
		current := ""
		if roleModel != nil {
			current = roleModel(role)
		}
		models := make([]palette.PaletteCommand, 0, len(modelNames)+1)
		// The synthetic row needs an ID no configured model can produce, or a
		// model literally named "inherit" would collide and the palette's
		// first-match lookup would clear the override instead of setting it.
		models = append(models, palette.PaletteCommand{
			ID:   "agents-inherit-" + role,
			Verb: chrome.CurrentMarker(current == "") + "(inherit parent)",
			Run: func() {
				if setRoleModel != nil {
					setRoleModel(role, "")
				}
			},
		})
		for _, name := range modelNames {
			models = append(models, palette.PaletteCommand{
				ID:   "agents-model-" + role + "-" + name,
				Verb: chrome.CurrentMarker(name == current) + name,
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
		Description:  "Configure sub-agents and their models",
		Keywords:     []string{"agent", "subagent", "spawn", "jobs", "parallel", "model"},
		SubmenuTitle: "Sub-agents",
		Submenu: []palette.PaletteCommand{
			{
				ID:       "agents-on",
				Verb:     chrome.CurrentMarker(enabled) + "on — register agent_* tools",
				Keywords: []string{"enable", "on", "spawn"},
				Run: func() {
					if set != nil {
						set(true)
					}
				},
			},
			{
				ID:       "agents-off",
				Verb:     chrome.CurrentMarker(!enabled) + "off — no sub-agents (fewer tools)",
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
