package pathutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HomePath abbreviates the home directory without folding the remaining path.
func HomePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	home = filepath.ToSlash(filepath.Clean(home))
	path := filepath.ToSlash(p)
	if path == home {
		return "~"
	}
	prefix := strings.TrimSuffix(home, "/") + "/"
	if strings.HasPrefix(path, prefix) {
		return "~" + p[len(prefix)-1:]
	}
	return p
}

// ShortPath abbreviates a filesystem path for display.
func ShortPath(p string) string {
	p = HomePath(p)
	parts := strings.Split(p, string(filepath.Separator))
	if len(parts) <= 5 {
		return p
	}
	return strings.Join(parts[:2], string(filepath.Separator)) +
		string(filepath.Separator) + ".." + string(filepath.Separator) +
		strings.Join(parts[len(parts)-2:], string(filepath.Separator))
}

// GitBranch returns the current git branch of dir, or "" when unavailable.
func GitBranch(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PathWithBranch renders the short path plus the git branch, e.g. "~/repo (main)".
func PathWithBranch(dir string) string {
	label := ShortPath(dir)
	if branch := GitBranch(context.Background(), dir); branch != "" {
		label += " (" + branch + ")"
	}
	return label
}

// WatchBranch polls the git HEAD file for changes and calls onChange with
// the updated path+branch label whenever the branch switches (e.g. from
// another terminal). The poll interval defaults to one second; dir must be a
// valid repository root.
func WatchBranch(dir string, onChange func(string)) {
	if dir == "" {
		return
	}
	stop := make(chan struct{})
	go (&branchWatch{dir: dir, interval: branchPollInterval}).run(stop, onChange)
}

type branchWatch struct {
	dir      string
	interval time.Duration
}

func (b *branchWatch) run(stop <-chan struct{}, onChange func(string)) {
	if b.interval <= 0 {
		b.interval = branchPollInterval
	}
	last := branchState(b.dir)
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		if cur := branchState(b.dir); cur != last {
			last = cur
			onChange(PathWithBranch(b.dir))
		}
	}
}

func branchState(dir string) string {
	gitDir := resolveGitDir(dir)
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "missing"
	}
	return strings.TrimSpace(string(data))
}

func resolveGitDir(dir string) string {
	dotGit := filepath.Join(dir, ".git")
	if data, err := os.ReadFile(dotGit); err == nil {
		if target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:"); ok {
			target = strings.TrimSpace(target)
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return target
		}
	}
	return dotGit
}

const branchPollInterval = time.Second
