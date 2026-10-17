package commands

import (
	"context"
	"strconv"
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/pathutil"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

// BranchCommands owns the /branch working-context switcher.
type BranchCommands struct {
	Bus *controller.Bus
	// Dir is the directory git runs in (the agent's working directory).
	Dir func() string
	// OpenOverlay opens the branch picker (wired by Builtin.Bind).
	OpenOverlay func(branches []gitx.Branch, recent []string, onAccept, onDelete func(name string))
	// RefreshOverlay swaps rows into the open picker (wired by Builtin.Bind).
	RefreshOverlay func(branches []gitx.Branch, recent []string)
	// StreamActive reports whether a checkout or delete must be refused.
	StreamActive func() bool

	// CurrentBranch reports the checked-out branch name ("" when unavailable).
	CurrentBranch func(ctx context.Context, dir string) string

	// Git indirections, so tests need no repository.
	Branches  func(ctx context.Context, dir string) ([]gitx.Branch, error)
	Recent    func(ctx context.Context, dir string) ([]string, error)
	Preflight func(ctx context.Context, dir string) (gitx.Status, error)
	Switch    func(ctx context.Context, dir, ref string) error
	Create    func(ctx context.Context, dir, name string) error
	Remove    func(ctx context.Context, dir, name string) error
}

// NewBranchCommands builds the /branch handler over the real git.
func NewBranchCommands(bus *controller.Bus) *BranchCommands {
	return &BranchCommands{
		Bus:           bus,
		CurrentBranch: pathutil.GitBranch,
		Branches:      gitx.Branches,
		Recent:        gitx.Recent,
		Preflight:     gitx.Preflight,
		Switch:        gitx.Switch,
		Create:        gitx.Create,
		Remove:        gitx.Delete,
	}
}

// Register wires /branch into r.
func (b *BranchCommands) Register(r *CommandRegistry) {
	r.Register(Command{
		Name:        "branch",
		Description: "Switch the working branch — /branch [name] creates",
		Slash:       true,
		Insert:      "/branch ",
		Run: func(_ Context, args []string) error {
			b.Run(args)
			return nil
		},
	})
}

// Show lists branches and opens the picker. The listing is one for-each-ref
// call, so it runs inline like /sessions does; the checkout it leads to does
// not.
func (b *BranchCommands) Show() {
	dir := b.dir()
	ctx := context.Background()
	branches, err := b.Branches(ctx, dir)
	if err != nil {
		b.toast(err.Error(), toast.ToastError)
		return
	}
	if len(branches) == 0 {
		b.toast("No branches yet — commit something first", toast.ToastWarning)
		return
	}
	// Reflog only decides row order; a missing one must not block switching.
	recent, err := b.Recent(ctx, dir)
	if err != nil {
		recent = nil
	}
	b.OpenOverlay(branches, recent, b.SwitchTo, b.Delete)
}

// Run handles /branch and /branch <name>. A name that is not a branch yet is
// created from HEAD, which is how a new workstream starts without leaving phi.
func (b *BranchCommands) Run(args []string) {
	switch len(args) {
	case 0:
		b.Show()
	case 1:
		b.SwitchOrCreate(args[0])
	default:
		b.toast("Usage: /branch [name]", toast.ToastWarning)
	}
}

// SwitchOrCreate switches to name, creating it from HEAD when no branch carries
// it. A remote-tracking name means the local branch that tracks it.
func (b *BranchCommands) SwitchOrCreate(name string) {
	if !gitx.ValidRef(name) {
		b.toast("Not a branch name: "+strconv.Quote(name), toast.ToastWarning)
		return
	}
	dir := b.dir()
	branches, err := b.Branches(context.Background(), dir)
	if err != nil {
		b.toast(err.Error(), toast.ToastError)
		return
	}
	target, exists := localTarget(branches, name)
	if !exists {
		b.CreateBranch(name)
		return
	}
	b.SwitchTo(target)
}

// localTarget resolves a typed name against the listed branches. Checking out
// origin/feat means "feat", which is what git resolves the short name to.
func localTarget(branches []gitx.Branch, name string) (string, bool) {
	for _, b := range branches {
		if b.Name == name {
			return b.LocalName(), true
		}
	}
	for _, b := range branches {
		if b.Remote && b.LocalName() == name {
			return name, true
		}
	}
	return "", false
}

// SwitchTo checks out name. The checkout runs off the UI goroutine: a cold or
// large worktree can take seconds, and git refuses rather than lose work, so
// the failure is reported instead of pre-empted.
func (b *BranchCommands) SwitchTo(name string) {
	if b.refuse() {
		return
	}
	dir := b.dir()
	go func() {
		// git reports "Switched to branch 'main'" even when HEAD is already
		// there, so answer that case ourselves instead of claiming a move.
		if b.CurrentBranch != nil && b.CurrentBranch(context.Background(), dir) == name {
			b.toast("Already on "+name, toast.ToastWarning)
			return
		}
		if err := b.Switch(context.Background(), dir, name); err != nil {
			b.toast(err.Error(), toast.ToastError)
			return
		}
		b.done("Switched to "+name, dir)
	}()
}

// CreateBranch branches name off HEAD. Creating does not touch the working
// tree, so git allows it dirty; only work already in flight holds it back.
func (b *BranchCommands) CreateBranch(name string) {
	if b.refuse() {
		return
	}
	dir := b.dir()
	go func() {
		if err := b.Create(context.Background(), dir, name); err != nil {
			b.toast(err.Error(), toast.ToastError)
			return
		}
		b.done("Created "+name, dir)
	}()
}

// Delete removes the branch chosen in the picker (Ctrl+X, then Y) and refreshes
// the rows in place, so several branches can go without reopening. It only
// rewrites a ref — no worktree checkout — so it runs inline like the session
// delete it mirrors.
func (b *BranchCommands) Delete(name string) {
	if b.StreamActive != nil && b.StreamActive() {
		b.toast("Cannot delete a branch while a reply or command is running", toast.ToastWarning)
		return
	}
	dir := b.dir()
	ctx := context.Background()
	branches, err := b.Branches(ctx, dir)
	if err != nil {
		b.toast(err.Error(), toast.ToastError)
		return
	}
	row, ok := findBranch(branches, name)
	if !ok {
		b.toast("No branch named "+name, toast.ToastWarning)
		return
	}
	// Both cases would be git errors with a worse message: -d on a remote ref
	// drops the remote-tracking branch, and the checked-out branch is an index
	// git will not let us leave.
	if row.Remote {
		b.toast("Cannot delete remote-tracking "+name+" — delete it on the remote instead", toast.ToastWarning)
		return
	}
	if row.Current {
		b.toast("Cannot delete "+name+" — switch to another branch first", toast.ToastWarning)
		return
	}
	if err := b.Remove(ctx, dir, name); err != nil {
		b.toast(err.Error(), toast.ToastError)
		return
	}
	b.toast("Deleted "+name, toast.ToastSuccess)
	b.refreshOverlay()
}

// findBranch resolves a picker row to its branch by exact ref name, so
// "origin/feat" never collapses onto the local "feat".
func findBranch(branches []gitx.Branch, name string) (gitx.Branch, bool) {
	for _, b := range branches {
		if b.Name == name {
			return b, true
		}
	}
	return gitx.Branch{}, false
}

// refreshOverlay re-lists and pushes the rows into the open picker in place.
// A failing listing keeps the rows already on screen: they were just valid.
func (b *BranchCommands) refreshOverlay() {
	if b.RefreshOverlay == nil {
		return
	}
	dir := b.dir()
	ctx := context.Background()
	branches, err := b.Branches(ctx, dir)
	if err != nil {
		return
	}
	recent, err := b.Recent(ctx, dir)
	if err != nil {
		recent = nil
	}
	b.RefreshOverlay(branches, recent)
}

// refuse reports whether a git mutation has to wait: a running reply would be
// repointed at another branch, and an unfinished merge or rebase has an index
// git will not let us leave.
func (b *BranchCommands) refuse() bool {
	if b.StreamActive != nil && b.StreamActive() {
		b.toast("Cannot switch branches while a reply or command is running", toast.ToastWarning)
		return true
	}
	st, err := b.Preflight(context.Background(), b.dir())
	if err != nil || st.Op == "" {
		return false
	}
	b.toast("Finish or abort the in-progress "+st.Op+" first", toast.ToastWarning)
	return true
}

func (b *BranchCommands) done(msg, dir string) {
	// WatchBranch would pick this up within a second; publishing now keeps the
	// label honest the moment git returns.
	b.publish(controller.BranchLabelMsg{Text: pathutil.PathWithBranch(dir)})
	b.toast(msg, toast.ToastSuccess)
}

func (b *BranchCommands) dir() string {
	if b.Dir != nil {
		return b.Dir()
	}
	return ""
}

func (b *BranchCommands) publish(m controller.Msg) {
	if b.Bus != nil {
		b.Bus.Publish(m)
	}
}

func (b *BranchCommands) toast(msg string, kind toast.ToastKind) {
	b.publish(controller.ToastMsg{Message: msg, Kind: kind, Duration: 4 * time.Second})
}
