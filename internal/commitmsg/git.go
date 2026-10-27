package commitmsg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	diffLimit    = 24 * 1024
	historyLimit = 2 * 1024
)

// cappedOutput bounds memory while Git runs, including stderr on failure.
// Continue draining so a truncated patch does not turn into a broken-pipe error.
type cappedOutput struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := min(len(p), b.limit-len(b.data))
	b.data = append(b.data, p[:n]...)
	b.truncated = b.truncated || n < len(p)
	return len(p), nil
}

func (c *Completer) git(ctx context.Context, limit int, args ...string) (string, bool, error) {
	operation := args[0]
	args = append([]string{"--no-pager", "--no-optional-locks", "-c", "color.ui=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.cwd
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = time.Second
	out := &cappedOutput{limit: limit}
	stderr := &cappedOutput{limit: historyLimit}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", false, fmt.Errorf("git %s: %w: %s", operation, err, strings.TrimSpace(string(stderr.data)))
	}
	return strings.ToValidUTF8(string(out.data), ""), out.truncated, nil
}

func (c *Completer) collect(ctx context.Context, all bool) (diff, history string, truncated bool, err error) {
	inside, _, err := c.git(ctx, 64, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if strings.Contains(err.Error(), "fatal: not a git repository") {
			return "", "", false, ErrNotRepository
		}
		return "", "", false, err
	}
	if strings.TrimSpace(inside) != "true" {
		return "", "", false, ErrNotRepository
	}
	_, _, headErr := c.git(ctx, 128, "rev-parse", "--verify", "--quiet", "HEAD")
	unborn := false
	if headErr != nil {
		var exit *exec.ExitError
		if !errors.As(headErr, &exit) || exit.ExitCode() != 1 {
			return "", "", false, headErr
		}
		unborn = true
	}
	args := []string{
		"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--ignore-submodules=dirty",
		"--no-relative", "--src-prefix=a/", "--dst-prefix=b/", "--unified=3",
	}
	if !all {
		args = append(args, "--cached")
	} else if unborn {
		// hash-object without -w computes the empty-tree ID without modifying the repository.
		var tree string
		tree, _, err = c.git(ctx, 128, "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			return "", "", false, err
		}
		args = append(args, strings.TrimSpace(tree))
	} else {
		args = append(args, "HEAD")
	}
	args = append(args, "--")
	diff, truncated, err = c.git(ctx, diffLimit, args...)
	if err != nil || diff == "" || unborn {
		return diff, "", truncated, err
	}
	history, _, err = c.git(ctx, historyLimit, "log", "-5", "--format=%s", "--no-show-signature")
	return diff, history, truncated, err
}
