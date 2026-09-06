package editor

import (
	"fmt"
	"time"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/submit"
)

type commandBridge struct {
	bus       *controller.Bus
	composer  *composer.ComposerPane
	ctrl      *controller.EngineController
	submitter *submit.Submitter
	sessions  *commands.SessionCommands
	extCmds   *commands.ExtCommands

	modelNames []string
	skillPath  string
}

func newCommandBridge(
	bus *controller.Bus,
	composer *composer.ComposerPane,
	ctrl *controller.EngineController,
	submitter *submit.Submitter,
	sessions *commands.SessionCommands,
	extCmds *commands.ExtCommands,
	modelNames []string,
	skillPath string,
) *commandBridge {
	return &commandBridge{
		bus:        bus,
		composer:   composer,
		ctrl:       ctrl,
		submitter:  submitter,
		sessions:   sessions,
		extCmds:    extCmds,
		modelNames: append([]string(nil), modelNames...),
		skillPath:  skillPath,
	}
}

func (b *commandBridge) context() commands.CommandContext {
	if b == nil {
		return commands.CommandContext{}
	}
	return commands.CommandContext{
		Bus: b.bus,
		PushSubmenu: func(title string, cmds []palette.PaletteCommand) {
			b.composer.PushPalette(title, cmds)
		},
		ShowSessions:  b.sessions.Show,
		ResumeSession: b.sessions.Resume,
		ClearSession: func() {
			if b.submitter != nil && b.submitter.StreamActive() {
				b.toast("Cannot clear while a reply or command is running", toast.ToastWarning, 3*time.Second)
				return
			}
			b.sessions.Clear()
		},
		SetModel:            b.setModel,
		ApplyTheme:          b.applyTheme,
		SetThinkingExpanded: func(expanded bool) { b.bus.Publish(controller.ThinkingExpandedMsg{Expanded: expanded}) },
		SetPermissions:      b.setPermissions,
		SetAgents:           b.setAgents,
		ReloadExtensions:    b.reloadExtensions,
		ListExtensions:      b.listExtensions,
		AddSkill:            b.addSkill,
		ModelNames:          b.modelNames,
		SkillPath:           b.skillPath,
	}
}

func (b *commandBridge) toast(msg string, kind toast.ToastKind, d time.Duration) {
	if b == nil || b.bus == nil {
		return
	}
	b.bus.Publish(controller.ToastMsg{Message: msg, Kind: kind, Duration: d})
}

func (b *commandBridge) setModel(name string) {
	if b == nil || b.ctrl == nil {
		return
	}
	if err := b.ctrl.SetModel(name); err != nil {
		b.toast(err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	if b.composer != nil {
		b.composer.SetModelLabel(name)
	}
	b.toast("Model: "+name, toast.ToastSuccess, 2*time.Second)
}

func (b *commandBridge) applyTheme(name string) {
	if b == nil || b.bus == nil {
		return
	}
	b.bus.Publish(controller.ThemeMsg{Name: name})
}

func (b *commandBridge) setPermissions(bypass bool) {
	if b == nil || b.ctrl == nil {
		return
	}
	b.ctrl.SetAllowAll(bypass)
	kind := toast.ToastWarning
	msg := "Permissions: on (ask)"
	if bypass {
		kind = toast.ToastSuccess
		msg = "Permissions: off (allow all)"
	}
	b.toast(msg, kind, 3*time.Second)
}

func (b *commandBridge) setAgents(enabled bool) {
	if b == nil || b.ctrl == nil {
		return
	}
	b.ctrl.SetAgentsEnabled(enabled)
	msg := "Sub-agents: off"
	if enabled {
		msg = "Sub-agents: on"
	}
	b.toast(msg, toast.ToastSuccess, 2*time.Second)
}

func (b *commandBridge) addSkill(name string) {
	if b == nil || b.composer == nil {
		return
	}
	b.composer.AddPendingSkill(name)
}

func (b *commandBridge) reloadExtensions() {
	if b == nil || b.ctrl == nil {
		return
	}
	n, warns, err := b.ctrl.ReloadExtensions()
	if err != nil {
		b.toast("Extensions reload: "+err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	if b.extCmds != nil {
		b.extCmds.Sync()
	}
	msg := fmt.Sprintf("Extensions: reloaded %d", n)
	if len(warns) > 0 {
		b.toast(
			fmt.Sprintf("Extensions: reloaded %d (%d warning(s))", n, len(warns)),
			toast.ToastWarning,
			3*time.Second,
		)
		return
	}
	b.toast(msg, toast.ToastSuccess, 2*time.Second)
}

func (b *commandBridge) listExtensions() []palette.PaletteCommand {
	if b == nil || b.ctrl == nil {
		return commands.ExtensionListEntries(nil, nil, nil)
	}
	found, warns, err := b.ctrl.ListExtensions()
	return commands.ExtensionListEntries(found, warns, err)
}
