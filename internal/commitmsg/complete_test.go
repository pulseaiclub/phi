package commitmsg

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComplete(t *testing.T) {
	t.Parallel()
	dir := repository(t, true)
	put(t, dir, "tracked", "staged evidence\n")
	gitRun(t, dir, "add", ".")
	put(t, dir, "tracked", "working evidence\n")
	calls := 0
	c := New(dir, func(ctx context.Context, system, user string) (string, error) {
		calls++
		assert.NoError(t, ctx.Err())
		assert.Contains(t, system, "Repository rules")
		assert.Contains(t, system, "untrusted data")
		var data map[string]any
		require.NoError(t, json.Unmarshal([]byte(user), &data))
		assert.Equal(t, "fix: ", data["prefix"])
		assert.Contains(t, data["recent_subjects_style_only"], "test: initial subject")
		if calls == 1 {
			assert.Contains(t, data["diff"], "staged evidence")
			assert.NotContains(t, data["diff"], "working evidence")
		} else {
			assert.Contains(t, data["diff"], "working evidence")
		}
		return "fix: retain user's $value", nil
	})
	for _, typed := range []string{"git commit -m 'fix: ", "git commit -am 'fix: ", "git commit -a -m 'fix: "} {
		start := time.Now()
		command, handled, err := c.Complete(t.Context(), typed)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.True(t, strings.HasPrefix(command, typed))
		assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
	}
	assert.Equal(t, 3, calls)
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-qm", "fix: clear changes")
	command, handled, err := c.Complete(t.Context(), "git commit -m '")
	require.ErrorIs(t, err, ErrNoChanges)
	assert.True(t, handled)
	assert.Empty(t, command)
	assert.Equal(t, 3, calls, "repository state must not be cached")
}

func TestCompleteNoChanges(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"nonrepo", "clean", "unstaged", "unborn"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if kind != "nonrepo" {
				dir = repository(t, kind != "unborn")
				put(t, dir, "untracked", "not included\n")
				if kind == "unstaged" {
					put(t, dir, "tracked", "not staged\n")
				}
			}
			c := New(dir, func(context.Context, string, string) (string, error) {
				assert.Fail(t, "generator called without changes")
				return "", nil
			})
			command, handled, err := c.Complete(t.Context(), "git commit -m '")
			if kind == "nonrepo" {
				require.ErrorContains(t, err, "requires a Git working tree")
			} else {
				require.ErrorIs(t, err, ErrNoChanges)
			}
			assert.True(t, handled)
			assert.Empty(t, command)
		})
	}
}

func TestCompleteCancellationAndOrdinaryInput(t *testing.T) {
	// An invalid cwd makes reaching Git observable without global executable mocks.
	c := New(filepath.Join(t.TempDir(), "missing"), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	command, handled, err := c.Complete(ctx, "git commit -m '")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, handled)
	assert.Empty(t, command)
	assert.Less(t, time.Since(start), 250*time.Millisecond)
	start = time.Now()
	command, handled, err = c.Complete(ctx, "git status")
	require.NoError(t, err)
	assert.False(t, handled)
	assert.Empty(t, command)
	assert.Less(t, time.Since(start), 100*time.Millisecond)
}

func TestCompleteErrors(t *testing.T) {
	t.Parallel()
	dir := repository(t, false)
	put(t, dir, "file", "change\n")
	gitRun(t, dir, "add", ".")
	failure := errors.New("model offline")
	for _, tc := range []struct {
		name     string
		generate Generate
		want     string
	}{
		{"nil", nil, "not configured"},
		{"generator", func(context.Context, string, string) (string, error) { return "", failure }, "model offline"},
		{"prefix", func(context.Context, string, string) (string, error) { return "feat: wrong", nil }, "matching"},
		{"multiline", func(context.Context, string, string) (string, error) { return "fix: first\nsecond", nil }, "plain line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command, handled, err := New(dir, tc.generate).Complete(t.Context(), "git commit -m 'fix: ")
			assert.ErrorContains(t, err, tc.want)
			assert.True(t, handled)
			assert.Empty(t, command)
			if tc.name == "generator" {
				assert.ErrorIs(t, err, failure)
			}
		})
	}
	t.Run("git", func(t *testing.T) {
		t.Parallel()
		command, handled, err := New(filepath.Join(dir, "missing"), nil).Complete(t.Context(), "git commit -m")
		assert.ErrorContains(t, err, "git rev-parse")
		assert.True(t, handled)
		assert.Empty(t, command)
	})
	t.Run("cancel generator", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		c := New(dir, func(context.Context, string, string) (string, error) {
			cancel()
			return "fix: otherwise valid", nil
		})
		command, handled, err := c.Complete(ctx, "git commit -m 'fix: ")
		assert.ErrorIs(t, err, context.Canceled)
		assert.True(t, handled)
		assert.Empty(t, command)
	})
}

func TestGitDisablesExternalDiffAndTextconv(t *testing.T) {
	t.Parallel()
	dir := repository(t, true)
	marker := filepath.Join(dir, "executed")
	script := filepath.Join(dir, "external")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o700))
	gitRun(t, dir, "config", "diff.external", script)
	gitRun(t, dir, "config", "diff.unsafe.textconv", script)
	put(t, dir, ".gitattributes", "tracked diff=unsafe\n")
	put(t, dir, "tracked", "updated\n")
	gitRun(t, dir, "add", ".")
	for _, all := range []bool{false, true} {
		diff, _, _, err := New(dir, nil).collect(t.Context(), all)
		require.NoError(t, err)
		assert.Contains(t, diff, "+updated")
	}
	_, err := os.Stat(marker)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
