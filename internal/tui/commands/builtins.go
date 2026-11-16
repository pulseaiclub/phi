package commands

import (
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/pathutil"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

// BuiltinComposer is the composer surface builtin domains need.
// *composer.ComposerPane satisfies this without importing composer here.
type BuiltinComposer interface {
	SetModelLabel(name, thinkLevel string)
	AddPendingSkill(name string)
	SetPaletteCommands([]palette.PaletteCommand)
}

// Builtin is the assembled command surface owned by the TUI shell.
type Builtin struct {
	Registry *CommandRegistry
	Sessions *SessionCommands
	Branches *BranchCommands
	Skills   *SkillsCommands
	Ext      *ExtCommands
}

type builtinConfig struct {
	registry             *CommandRegistry
	bus                  *controller.Bus
	ctrl                 *controller.EngineController
	composer             BuiltinComposer
	transcript           *transcript.TranscriptPane
	modelNames           []string
	skillPaths           []string
	openDiff             func([]string)
	openCode             func([]string)
	submitter            extSubmitter
	commandCtx           Context
	openSessionPicker    func([]session.SessionMeta, string)
	refreshSessionPicker func([]session.SessionMeta, string)
	openBranchPicker     func([]gitx.Branch, []string, func(string), func(string))
	refreshBranchPicker  func([]gitx.Branch, []string)
	openSkillPicker      func([]listpicker.Item, listpicker.ShowConfig, func(listpicker.Item))
	cwd                  string
	streamActive         func() bool
	resetActivity        func()
	setStatus            func(string)
}

// BuiltinOption configures builtin command dependencies before registration.
type BuiltinOption func(*builtinConfig)

// WithBuiltinCommandRegistry allows a submitter to share the registry before builtin assembly.
func WithBuiltinCommandRegistry(r *CommandRegistry) BuiltinOption {
	return func(c *builtinConfig) { c.registry = r }
}

func WithBuiltinBus(bus *controller.Bus) BuiltinOption {
	return func(c *builtinConfig) { c.bus = bus }
}

func WithBuiltinController(ctrl *controller.EngineController) BuiltinOption {
	return func(c *builtinConfig) { c.ctrl = ctrl }
}

func WithBuiltinComposer(composer BuiltinComposer) BuiltinOption {
	return func(c *builtinConfig) { c.composer = composer }
}

func WithBuiltinTranscript(tr *transcript.TranscriptPane) BuiltinOption {
	return func(c *builtinConfig) { c.transcript = tr }
}

func WithBuiltinModelNames(names []string) BuiltinOption {
	return func(c *builtinConfig) { c.modelNames = names }
}

func WithBuiltinSkillPaths(paths []string) BuiltinOption {
	return func(c *builtinConfig) { c.skillPaths = paths }
}

func WithBuiltinOpenDiff(open func([]string)) BuiltinOption {
	return func(c *builtinConfig) { c.openDiff = open }
}

func WithBuiltinOpenCode(open func([]string)) BuiltinOption {
	return func(c *builtinConfig) { c.openCode = open }
}

func WithBuiltinSubmitter(submitter extSubmitter) BuiltinOption {
	return func(c *builtinConfig) { c.submitter = submitter }
}

func WithBuiltinCommandContext(ctx Context) BuiltinOption {
	return func(c *builtinConfig) { c.commandCtx = ctx }
}

func WithBuiltinOpenSessionPicker(open func([]session.SessionMeta, string)) BuiltinOption {
	return func(c *builtinConfig) { c.openSessionPicker = open }
}

func WithBuiltinRefreshSessionPicker(refresh func([]session.SessionMeta, string)) BuiltinOption {
	return func(c *builtinConfig) { c.refreshSessionPicker = refresh }
}

func WithBuiltinOpenBranchPicker(open func([]gitx.Branch, []string, func(string), func(string))) BuiltinOption {
	return func(c *builtinConfig) { c.openBranchPicker = open }
}

func WithBuiltinRefreshBranchPicker(refresh func([]gitx.Branch, []string)) BuiltinOption {
	return func(c *builtinConfig) { c.refreshBranchPicker = refresh }
}

func WithBuiltinOpenSkillPicker(
	open func([]listpicker.Item, listpicker.ShowConfig, func(listpicker.Item)),
) BuiltinOption {
	return func(c *builtinConfig) { c.openSkillPicker = open }
}

func WithBuiltinCwd(cwd string) BuiltinOption {
	return func(c *builtinConfig) { c.cwd = cwd }
}

func WithBuiltinStreamActive(active func() bool) BuiltinOption {
	return func(c *builtinConfig) { c.streamActive = active }
}

func WithBuiltinResetActivity(reset func()) BuiltinOption {
	return func(c *builtinConfig) { c.resetActivity = reset }
}

func WithBuiltinSetStatus(set func(string)) BuiltinOption {
	return func(c *builtinConfig) { c.setStatus = set }
}

// NewBuiltinRegistry constructs every builtin handler before registering commands.
// It creates a registry unless WithBuiltinCommandRegistry supplies one.
func NewBuiltinRegistry(opts ...BuiltinOption) *Builtin {
	var c builtinConfig
	for _, opt := range opts {
		opt(&c)
	}
	r := c.registry
	if r == nil {
		r = NewCommandRegistry()
	}
	ext := &ExtCommands{
		Registry:   r,
		Ctrl:       c.ctrl,
		Composer:   c.composer,
		Bus:        c.bus,
		Submitter:  c.submitter,
		CommandCtx: c.commandCtx,
		SetStatus:  c.setStatus,
	}
	sessions := &SessionCommands{
		Ctrl:          c.ctrl,
		Composer:      c.composer,
		Transcript:    c.transcript,
		Bus:           c.bus,
		SyncHooks:     ext.Sync,
		ResetActivity: c.resetActivity,
		OpenPicker:    c.openSessionPicker,
		RefreshPicker: c.refreshSessionPicker,
		StreamActive:  c.streamActive,
	}
	branches := &BranchCommands{
		Bus:            c.bus,
		Dir:            c.cwd,
		OpenOverlay:    c.openBranchPicker,
		RefreshOverlay: c.refreshBranchPicker,
		StreamActive:   c.streamActive,
		CurrentBranch:  pathutil.GitBranch,
		Branches:       gitx.Branches,
		Recent:         gitx.Recent,
		Preflight:      gitx.Preflight,
		Switch:         gitx.Switch,
		Create:         gitx.Create,
		CreateAt:       gitx.CreateAt,
		Remove:         gitx.Delete,
	}
	settings := &SettingsCommands{
		Ctrl:       c.ctrl,
		Bus:        c.bus,
		Composer:   c.composer,
		ModelNames: append([]string(nil), c.modelNames...),
	}
	skills := &SkillsCommands{
		Bus:        c.bus,
		SkillPaths: c.skillPaths,
		Add:        c.composer,
		OpenPicker: c.openSkillPicker,
	}
	diff := &DiffCommands{Open: c.openDiff}
	code := &CodeCommands{Open: c.openCode}

	sessions.Register(r)
	branches.Register(r)
	settings.Register(r)
	ext.Register(r)
	skills.Register(r)
	diff.Register(r)
	code.Register(r)

	return &Builtin{Registry: r, Sessions: sessions, Branches: branches, Skills: skills, Ext: ext}
}
