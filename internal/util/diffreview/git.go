package diffreview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/util/gitx"
)

// LabelForSpec is the overlay title suffix for a /diff argument list.
func LabelForSpec(spec []string) string {
	parts := make([]string, 0, len(spec))
	for _, tok := range spec {
		switch strings.ToLower(tok) {
		case "--staged", "--cached":
			tok = "staged"
		case "--":
			continue
		}
		parts = append(parts, strings.TrimPrefix(tok, "@"))
	}
	if len(parts) == 0 {
		return "working tree"
	}
	return strings.Join(parts, " ")
}

// NormalizeSpec turns /diff arguments into arguments for `git diff`. The
// composer completes files as `@<path> `, so an `@` prefix marks a path; every
// path moves behind a `--` separator, because without it a file named like a
// branch (`main`) reaches git as a revision. `/diff -- <argv>` is an escape
// hatch, not a filter, and passes through untouched.
func NormalizeSpec(cwd string, args []string) ([]string, error) {
	if len(args) == 0 || args[0] == "--" {
		return append([]string(nil), args...), nil
	}
	head, rest := "", args
	switch strings.ToLower(args[0]) {
	case "staged", "--staged", "--cached", "head":
		head, rest = args[0], args[1:]
	}
	refs := make([]string, 0, len(rest))
	paths := make([]string, 0, len(rest))
	for _, tok := range rest {
		path, isPath, err := splitToken(cwd, tok)
		if err != nil {
			return nil, err
		}
		if isPath {
			paths = append(paths, path)
			continue
		}
		refs = append(refs, tok)
	}
	if len(paths) == 0 {
		return append([]string(nil), args...), nil
	}
	out := make([]string, 0, len(refs)+len(paths)+2)
	if head != "" {
		out = append(out, head)
	}
	out = append(out, refs...)
	out = append(out, "--")
	return append(out, paths...), nil
}

// splitToken decides whether one /diff token is a path. Only an `@` prefix, an
// existing file, or a glob counts: branch names may contain slashes too, so an
// unknown bare name stays a revision and git gets to explain it.
func splitToken(cwd, tok string) (string, bool, error) {
	if strings.HasPrefix(tok, "@") && len(tok) > 1 {
		path := tok[1:]
		if !pathExists(cwd, path) {
			return "", false, fmt.Errorf("no such path: %s", path)
		}
		return path, true, nil
	}
	if strings.HasPrefix(tok, "-") {
		return "", false, nil
	}
	if pathExists(cwd, tok) || strings.ContainsAny(tok, "*?[") {
		return tok, true, nil
	}
	return "", false, nil
}

func pathExists(cwd, path string) bool {
	if cwd != "" && !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	_, err := os.Stat(path)
	return err == nil
}

// EmptyNote explains an empty diff for spec. Untracked files never appear in
// `git diff` output, which is the usual reason a reviewer sees nothing.
func EmptyNote(cwd string, spec []string) string {
	// The untracked hint shells out to git, so callers cache the result instead
	// of asking on every redraw.
	for _, path := range requestedPaths(cwd, spec) {
		if !tracked(cwd, path) {
			return fmt.Sprintf(
				"%s is untracked — git diff does not show it; run git add -N %s, then /diff staged",
				path, path,
			)
		}
	}
	if len(spec) == 0 {
		return "No unstaged changes. Untracked files are not shown — run git status."
	}
	switch strings.ToLower(spec[0]) {
	case "staged", "--staged", "--cached":
		return "No staged changes. Index matches HEAD; untracked files are not shown."
	case "head":
		return "HEAD has no changes to show."
	default:
		return fmt.Sprintf("No changes vs %s. Untracked files are not shown.", LabelForSpec(spec))
	}
}

// requestedPaths reports the paths of a raw /diff argument list. It reads the
// tokens the way NormalizeSpec does, so it works on what the user typed;
// `/diff -- <argv>` asks for no paths at all.
func requestedPaths(cwd string, spec []string) []string {
	if len(spec) == 0 || spec[0] == "--" {
		return nil
	}
	rest := spec
	switch strings.ToLower(spec[0]) {
	case "staged", "--staged", "--cached", "head":
		rest = spec[1:]
	}
	paths := make([]string, 0, len(rest))
	for _, tok := range rest {
		if path, isPath, err := splitToken(cwd, tok); err == nil && isPath {
			paths = append(paths, path)
		}
	}
	return paths
}

func tracked(cwd, path string) bool {
	// Bounded: this runs while the overlay opens, and a stuck git must not take
	// the UI with it.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--error-unmatch", "--", path) //nolint:gosec // G204: fixed argv
	if cwd != "" {
		cmd.Dir = cwd
	}
	return cmd.Run() == nil
}

// GitArgv is the git command that produces a unified diff for spec.
func GitArgv(cwd string, spec []string) ([]string, error) {
	argv := []string{"git", "-c", "color.ui=never"}
	if len(spec) > 0 && spec[0] == "--" {
		if len(spec) == 1 {
			return append(argv, "diff"), nil
		}
		return append([]string(nil), spec[1:]...), nil
	}
	norm, err := NormalizeSpec(cwd, spec)
	if err != nil {
		return nil, err
	}
	if len(norm) == 0 {
		return append(argv, "diff"), nil
	}
	switch strings.ToLower(norm[0]) {
	case "staged", "--staged", "--cached":
		return append(argv, append([]string{"diff", "--staged"}, norm[1:]...)...), nil
	case "head":
		if len(norm) == 1 {
			return append(argv, "show", "--pretty=medium", "-p", "HEAD"), nil
		}
		// `HEAD <paths>` used to land on the git show above, which dropped the
		// paths: the reviewer asked for one file and saw the whole commit.
		return append(argv, append([]string{"diff", "HEAD"}, norm[1:]...)...), nil
	default:
		return append(argv, append([]string{"diff"}, norm...)...), nil
	}
}

// LoadGit runs git in cwd and returns unified-diff text. Errors are formatted
// via gitx.FormatError so the status bar sees one line plus a hint.
func LoadGit(ctx context.Context, cwd string, spec []string) (string, error) {
	argv, err := GitArgv(cwd, spec)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // G204: git from GitArgv, or /diff -- argv
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err == nil || looksLikeDiff(text) {
		return text, nil
	}
	return "", gitx.FormatError(argv, text, err)
}

func looksLikeDiff(text string) bool {
	return strings.Contains(text, "\ndiff --git ") || strings.HasPrefix(text, "diff --git ") ||
		strings.HasPrefix(text, "commit ")
}
