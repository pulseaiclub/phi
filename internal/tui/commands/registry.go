package commands

import (
	"strings"
	"sync"
	"time"

	"github.com/pulseaiclub/phi/internal/components/mention"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// CommandContext is the capability surface passed to command Run / palette
// builders. Callers fill only what they need; nil funcs are no-ops and a
// nil Bus swallows publishes.
// It must not hold *Editor (keeps commands free of the root widget).
type CommandContext struct {
	Args []string // slash args after the command name

	Bus         *controller.Bus
	PushSubmenu func(title string, cmds []palette.PaletteCommand)

	ShowSessions  func()
	ResumeSession func(id string)
	ClearSession  func() // may toast internally if busy

	SetModel            func(name string)
	ApplyTheme          func(name string)
	SetThinkingExpanded func(expanded bool)
	SetPermissions      func(bypass bool)
	SetAgents           func(enabled bool)
	ReloadExtensions    func()
	ListExtensions      func() []palette.PaletteCommand
	AddSkill            func(name string)

	ModelNames []string
	SkillPath  string
}

func (ctx CommandContext) toast(msg string, kind toast.ToastKind, d time.Duration) {
	ctx.Bus.Publish(controller.ToastMsg{Message: msg, Kind: kind, Duration: d})
}

// Command is one registered slash and/or palette entry.
type Command struct {
	Name        string // without leading slash, e.g. "resume"
	Description string
	Slash       bool
	// Insert is written into the composer on slash-picker accept.
	// Empty defaults to "/"+Name (or "/"+Name+" " when NeedsArgs).
	// A trailing space means "fill composer, do not auto-submit".
	Insert string
	// NeedsArgs means bare "/name" (picker accept or submit) should leave
	// Insert in the composer so the user can type arguments.
	NeedsArgs bool

	// Run handles slash dispatch (and may be unused for palette-only trees).
	Run func(ctx CommandContext) error

	// PaletteRoot builds a Ctrl+K root row when non-nil.
	PaletteRoot func(ctx CommandContext) palette.PaletteCommand

	fromExt bool // dropped on extensions reload; cannot replace builtins
}

// CommandRegistry is the single catalog for composer `/` and Ctrl+K palette.
type CommandRegistry struct {
	mu   sync.RWMutex
	cmds []Command
	by   map[string]int // lower(name) → index in cmds
}

// NewCommandRegistry returns an empty registry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{by: make(map[string]int)}
}

func finalizeSlashInsert(cmd *Command) {
	if !cmd.Slash {
		return
	}
	if cmd.Insert == "" {
		cmd.Insert = "/" + cmd.Name
		if cmd.NeedsArgs {
			cmd.Insert += " "
		}
	} else if cmd.NeedsArgs && !strings.HasSuffix(cmd.Insert, " ") {
		cmd.Insert += " "
	}
	if strings.HasSuffix(cmd.Insert, " ") {
		cmd.NeedsArgs = true
	}
}

// Register adds cmd. Duplicate names (case-insensitive) replace the prior entry.
func (r *CommandRegistry) Register(cmd Command) {
	name := strings.ToLower(strings.TrimSpace(cmd.Name))
	if name == "" {
		return
	}
	cmd.Name = strings.TrimSpace(cmd.Name)
	finalizeSlashInsert(&cmd)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.by == nil {
		r.by = make(map[string]int)
	}
	if i, ok := r.by[name]; ok {
		r.cmds[i] = cmd
		return
	}
	r.by[name] = len(r.cmds)
	r.cmds = append(r.cmds, cmd)
}

// registerExt adds a slash command from an extension.
// Returns false if name is empty or already taken by a builtin.
func (r *CommandRegistry) registerExt(cmd Command) bool {
	name := strings.ToLower(strings.TrimSpace(cmd.Name))
	if name == "" {
		return false
	}
	cmd.Name = strings.TrimSpace(cmd.Name)
	cmd.fromExt = true
	finalizeSlashInsert(&cmd)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.by == nil {
		r.by = make(map[string]int)
	}
	if i, ok := r.by[name]; ok {
		if !r.cmds[i].fromExt {
			return false
		}
		r.cmds[i] = cmd
		return true
	}
	r.by[name] = len(r.cmds)
	r.cmds = append(r.cmds, cmd)
	return true
}

// clearExtCommands removes every command registered via registerExt.
func (r *CommandRegistry) clearExtCommands() {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make([]Command, 0, len(r.cmds))
	r.by = make(map[string]int, len(r.cmds))
	for _, c := range r.cmds {
		if c.fromExt {
			continue
		}
		r.by[strings.ToLower(c.Name)] = len(kept)
		kept = append(kept, c)
	}
	r.cmds = kept
}

// DispatchSlash runs a `/name …` line. Returns false if not a known slash command.
func (r *CommandRegistry) DispatchSlash(text string, ctx CommandContext) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	name := strings.TrimPrefix(fields[0], "/")
	cmd, ok := r.lookup(name)
	if !ok || !cmd.Slash || cmd.Run == nil {
		return false
	}
	ctx.Args = fields[1:]
	_ = cmd.Run(ctx)
	return true
}

// FilterSlash returns mention items for the slash picker (name prefix match).
func (r *CommandRegistry) FilterSlash(query string) []mention.Item {
	q := strings.ToLower(strings.TrimSpace(query))
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]mention.Item, 0, len(r.cmds))
	for _, c := range r.cmds {
		if !c.Slash {
			continue
		}
		if q != "" && !strings.HasPrefix(strings.ToLower(c.Name), q) {
			continue
		}
		out = append(out, mention.Item{
			Path:        c.Name,
			Description: c.Description,
		})
	}
	return out
}

// LookupInsert returns the Insert string for a slash command name, or empty.
func (r *CommandRegistry) LookupInsert(name string) string {
	cmd, ok := r.lookup(name)
	if !ok || !cmd.Slash {
		return ""
	}
	return cmd.Insert
}

// IncompleteSlash reports whether text is a known NeedsArgs slash with no
// arguments. insert is the composer value to leave for the user to finish.
func (r *CommandRegistry) IncompleteSlash(text string) (insert string, ok bool) {
	fields := strings.Fields(text)
	if len(fields) != 1 {
		return "", false
	}
	name := strings.TrimPrefix(fields[0], "/")
	if name == "" || name == fields[0] {
		return "", false
	}
	cmd, found := r.lookup(name)
	if !found || !cmd.Slash || !cmd.NeedsArgs {
		return "", false
	}
	return cmd.Insert, true
}

// BuildPalette returns Ctrl+K root commands in registration order.
func (r *CommandRegistry) BuildPalette(ctx CommandContext) []palette.PaletteCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]palette.PaletteCommand, 0, len(r.cmds))
	for _, c := range r.cmds {
		if c.PaletteRoot == nil {
			continue
		}
		out = append(out, c.PaletteRoot(ctx))
	}
	return out
}

func (r *CommandRegistry) lookup(name string) (Command, bool) {
	key := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.by[key]
	if !ok {
		return Command{}, false
	}
	return r.cmds[i], true
}

// SlashCommands returns slash catalog entries (for tests / introspection).
func (r *CommandRegistry) SlashCommands() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Command, 0, len(r.cmds))
	for _, c := range r.cmds {
		if c.Slash {
			out = append(out, c)
		}
	}
	return out
}
