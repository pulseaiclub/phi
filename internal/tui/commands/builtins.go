package commands

import (
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

// BuiltinComposer is the composer surface builtin domains need.
// *composer.ComposerPane satisfies this without importing composer here.
type BuiltinComposer interface {
	SetModelLabel(name, thinkLevel string)
	AddPendingSkill(name string)
	SetPaletteCommands([]palette.PaletteCommand)
	ShowList(items []listpicker.Item, cfg listpicker.ShowConfig, onAccept, onDelete func(listpicker.Item))
	ShowSessionList(items []session.SessionMeta, currentID string, onAccept, onDelete func(id string))
	RefreshSessionList(items []session.SessionMeta)
	ShowBranchList(branches []gitx.Branch, recent []string, onAccept, onDelete func(name string))
	RefreshBranchList(branches []gitx.Branch, recent []string)
}

// HostUI is the editor shell surface builtin domains call back into: the
// full-screen viewers, footer state, and the stream guard. *editor.Editor
// implements it as one aggregate; picker plumbing rides BuiltinComposer. A
// domain holds it whole and calls its slice, so wiring never scatters into
// per-callback func fields.
type HostUI interface {
	OpenDiff(args []string)
	OpenCode(args []string)
	ResetActivity()
	SetExtensionStatus(status string)
	StreamActive() bool
}

// Builtin is the assembled command surface owned by the TUI shell.
type Builtin struct {
	Registry *CommandRegistry
	Sessions *SessionCommands
	Branches *BranchCommands
	Skills   *SkillsCommands
	Ext      *ExtCommands
}

// NewBuiltinRegistry registers every builtin domain on r and wires their
// collaborators. All collaborators arrive through this constructor: the
// registry is built first (it has no domain deps), then the submitter, then
// the domains — so every handler is complete at return, with no late Bind.
func NewBuiltinRegistry(
	r *CommandRegistry,
	commandCtx Context,
	bus *controller.Bus,
	ctrl *controller.EngineController,
	composer BuiltinComposer,
	tr *transcript.TranscriptPane,
	cwd string,
	modelNames []string,
	skillPaths []string,
	ui HostUI,
	submitter extSubmitter,
) *Builtin {
	ext := &ExtCommands{
		Registry:   r,
		Ctrl:       ctrl,
		Composer:   composer,
		Bus:        bus,
		CommandCtx: commandCtx,
		Submitter:  submitter,
		ui:         ui,
	}
	sessions := NewSessionCommands(ctrl, tr, bus, ext, composer, ui)
	branches := NewBranchCommands(bus, cwd, composer, ui)
	settings := &SettingsCommands{
		Ctrl:       ctrl,
		Bus:        bus,
		Composer:   composer,
		ModelNames: append([]string(nil), modelNames...),
	}
	skills := &SkillsCommands{
		Bus:        bus,
		SkillPaths: skillPaths,
		composer:   composer,
	}
	diff := &DiffCommands{ui: ui}
	code := &CodeCommands{ui: ui}

	sessions.Register(r)
	branches.Register(r)
	settings.Register(r)
	ext.Register(r)
	skills.Register(r)
	diff.Register(r)
	code.Register(r)

	return &Builtin{Registry: r, Sessions: sessions, Branches: branches, Skills: skills, Ext: ext}
}
