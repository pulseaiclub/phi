// Package gitx runs git for TUI overlays that need repo state.
// Every call shells out with a deadline. Switch, Create, and Delete mutate the
// repo, and each takes a single ref name that ValidRef has already vetted.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pulseaiclub/phi/internal/util"
)

const (
	// defaultTimeout bounds one git call so a wedged repo cannot freeze the UI.
	defaultTimeout = 5 * time.Second
	// reflogLimit is how far back Recent looks for checkout history.
	reflogLimit = 40
	// maxErrRunes keeps git's stderr to something one status line can hold.
	maxErrRunes = 200
	// fieldSep is the tab git emits between format fields.
	fieldSep = "\t"
)

// Branch is one local or remote ref as the branch overlay lists it.
type Branch struct {
	Name      string // refname:short, e.g. "fix/one" or "origin/main"
	Current   bool   // HEAD points here
	Remote    bool   // refs/remotes/*
	Upstream  string // tracking branch, empty when none
	Gone      bool   // upstream known but deleted
	Ahead     int    // commits ahead of upstream
	Behind    int    // commits behind upstream
	Committed string // git's own relative date, e.g. "2 days ago"
	Subject   string // last commit subject
}

// Status is the pre-flight state a checkout has to respect.
type Status struct {
	Dirty int    // changed, staged, or untracked paths
	Op    string // in-progress operation: merge, rebase, cherry-pick, revert, bisect
}

// Clean reports whether switching is safe without touching local work.
func (s Status) Clean() bool { return s.Dirty == 0 && s.Op == "" }

// LocalName is the local branch a row stands for. A remote-tracking ref means
// "the local branch of that name, tracking this one": origin/feat is what
// `git switch feat` checks out. Non-remote branches return their own name.
func (b Branch) LocalName() string {
	if !b.Remote {
		return b.Name
	}
	if i := strings.IndexByte(b.Name, '/'); i >= 0 {
		return b.Name[i+1:]
	}
	return b.Name
}

// branchFormat asks for everything the overlay renders in a single call.
// %(HEAD) is "*" for the current branch; %(upstream:track) is "[gone]" or
// "[ahead 1, behind 2]". Older gits do not expand %x09, so the separator is a
// real tab.
const branchFormat = "%(HEAD)\t%(refname)\t%(refname:short)\t%(upstream:short)" +
	"\t%(upstream:track)\t%(committerdate:relative)\t%(contents:subject)"

// Branches lists local branches first-class and remote-tracking branches
// alongside them, newest commit first.
func Branches(ctx context.Context, dir string) ([]Branch, error) {
	out, err := run(ctx, dir,
		"for-each-ref", "--sort=-committerdate", "--format="+branchFormat,
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	return parseBranches(out), nil
}

// Recent returns branch names in most-recently-checked-out order, newest
// first. Detached checkouts (raw SHAs) are skipped: there is nothing to switch
// back to by name. A repo with no reflog yields an empty list, not an error.
func Recent(ctx context.Context, dir string) ([]string, error) {
	out, err := run(ctx, dir, "reflog", "-n", strconv.Itoa(reflogLimit), "--format=%gs")
	if err != nil {
		return nil, err
	}
	return parseRecent(out), nil
}

// Preflight reports uncommitted work and any in-progress operation.
func Preflight(ctx context.Context, dir string) (Status, error) {
	out, err := run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return Status{}, err
	}
	var st Status
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) != "" {
			st.Dirty++
		}
	}
	st.Op = inProgressOp(ctx, dir)
	return st, nil
}

// Switch checks out ref. It fails closed on anything that is not a plain ref
// name, so a ref can never turn into an option or a second command.
func Switch(ctx context.Context, dir, ref string) error {
	if !ValidRef(ref) {
		return fmt.Errorf("invalid branch name %q", ref)
	}
	_, err := run(ctx, dir, "switch", ref)
	return err
}

// Create makes name at HEAD and switches to it.
func Create(ctx context.Context, dir, name string) error {
	if !ValidRef(name) {
		return fmt.Errorf("invalid branch name %q", name)
	}
	_, err := run(ctx, dir, "switch", "-c", name)
	return err
}

// Delete removes local branch name. -d and never -D: git refuses to drop
// commits that are not merged, so a mistyped row cannot destroy work.
func Delete(ctx context.Context, dir, name string) error {
	if !ValidRef(name) {
		return fmt.Errorf("invalid branch name %q", name)
	}
	_, err := run(ctx, dir, "branch", "-d", name)
	return err
}

// ValidRef accepts names git can treat as a ref argument and nothing else.
func ValidRef(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\n") {
		return false
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func parseBranches(out string) []Branch {
	var branches []Branch
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.SplitN(line, fieldSep, 7)
		if len(f) < 7 {
			continue
		}
		ref, short := f[1], f[2]
		// refs/remotes/origin/HEAD is a symbolic ref pointing at another row.
		if short == "" || strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		b := Branch{
			Name:      short,
			Current:   f[0] == "*",
			Remote:    strings.HasPrefix(ref, "refs/remotes/"),
			Upstream:  f[3],
			Committed: f[5],
			Subject:   f[6],
		}
		b.Ahead, b.Behind, b.Gone = parseTrack(f[4])
		branches = append(branches, b)
	}
	return branches
}

var trackCount = regexp.MustCompile(`(ahead|behind) (\d+)`)

// parseTrack reads git's "[ahead 1, behind 2]" / "[gone]" summary.
func parseTrack(s string) (ahead, behind int, gone bool) {
	if strings.Contains(s, "gone") {
		gone = true
	}
	for _, m := range trackCount.FindAllStringSubmatch(s, -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		if m[1] == "ahead" {
			ahead = n
			continue
		}
		behind = n
	}
	return ahead, behind, gone
}

const reflogPrefix = "checkout: moving from "

func parseRecent(out string) []string {
	seen := make(map[string]bool)
	var names []string
	for line := range strings.SplitSeq(out, "\n") {
		msg, ok := strings.CutPrefix(strings.TrimSpace(line), reflogPrefix)
		if !ok {
			continue
		}
		i := strings.LastIndex(msg, " to ")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(msg[i+len(" to "):])
		if name == "" || isObjectID(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// isObjectID reports whether name is a raw commit SHA, i.e. a detached target.
func isObjectID(name string) bool {
	if len(name) != 40 {
		return false
	}
	for _, r := range name {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func inProgressOp(ctx context.Context, dir string) string {
	out, err := run(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSpace(out)
	if gitDir == "" {
		return ""
	}
	for _, op := range []struct{ file, name string }{
		{"MERGE_HEAD", "merge"},
		{"rebase-merge", "rebase"},
		{"rebase-apply", "rebase"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
	} {
		if _, err := os.Stat(filepath.Join(gitDir, op.file)); err == nil {
			return op.name
		}
	}
	return ""
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cctx, cancel := withTimeout(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", FormatError(append([]string{"git"}, args...), "", err)
	}
	return string(out), nil
}

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), defaultTimeout)
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultTimeout)
}

// FormatError renders a git failure as a single-line error with a hint.
// argv is the full command vector including "git"; text is combined output
// from the caller (may be empty); err is the exec error.
func FormatError(argv []string, text string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("git not found on PATH: %w", err)
	}
	msg := extractMessage(text, err)
	return fmt.Errorf("%s: %s%s", strings.Join(argv, " "), msg, Hint(msg))
}

// extractMessage pulls a human-readable line from whatever git gave back.
// Combined output (stdout+stderr mixed) is the primary source; ExitError's
// Stderr is a fallback for callers that used Output() instead.
func extractMessage(text string, err error) string {
	raw := text
	if strings.TrimSpace(raw) == "" {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			raw = string(exit.Stderr)
		}
	}
	if raw == "" {
		raw = err.Error()
	}
	if i := strings.Index(raw, "\nusage:"); i >= 0 {
		raw = raw[:i]
	}
	if msg := OneLine(raw); msg != "" {
		return Truncate(msg, maxErrRunes)
	}
	return Truncate(OneLine(err.Error()), maxErrRunes)
}

// Hint names a concrete next step for the git errors users actually hit.
func Hint(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not a git repository"):
		return " — this directory is not a git repository"
	case strings.Contains(lower, "your local changes"), strings.Contains(lower, "would be overwritten"):
		return " — commit or stash first"
	case strings.Contains(lower, "already used by worktree"):
		return " — checked out in another worktree"
	case strings.Contains(lower, "not fully merged"):
		return " — merge or rebase it first, or force with git branch -D"
	case strings.Contains(lower, "nothing to commit"):
		return ""
	case strings.Contains(lower, "no such branch"), strings.Contains(lower, "invalid reference"),
		strings.Contains(lower, "not a valid object name"), strings.Contains(lower, "did not match any"):
		return " — pick a branch from the list"
	case strings.Contains(lower, "unknown revision"), strings.Contains(lower, "ambiguous argument"):
		return " — fetch first"
	}
	return ""
}

// OneLine flattens git's multi-line stderr into a single status line.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Truncate cuts s to at most n runes, appending "…" when truncated.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(util.TruncateRunes(s, n, "")) + "…"
}
