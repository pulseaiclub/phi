package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

func TestBranchCommandRegistersSlash(t *testing.T) {
	r := NewCommandRegistry()
	b := NewBranchCommands(controller.NewBus(nil))
	b.OpenOverlay = func([]gitx.Branch, []string, func(string), func(string)) {}
	b.Register(r)

	assert.Equal(t, "/branch ", r.LookupInsert("branch"))
	assert.True(t, r.DispatchSlash("/branch", NewContext(controller.NewBus(nil), nil)))
}

func TestBranchShowOpensOverlayWithRows(t *testing.T) {
	branches := []gitx.Branch{{Name: "main", Current: true}, {Name: "fix/x"}}
	b := stubBranchCommands(branches)
	var got []string
	b.OpenOverlay = func(list []gitx.Branch, recent []string, onAccept, onDelete func(string)) {
		for _, item := range list {
			got = append(got, item.Name)
		}
		assert.Equal(t, []string{"fix/x"}, recent, "reflog order reaches the picker")
		require.NotNil(t, onAccept)
		require.NotNil(t, onDelete, "the picker owns the branch delete path")
	}

	b.Show()

	assert.Equal(t, []string{"main", "fix/x"}, got)
}

func TestRunWithNameCreatesMissingBranch(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "main", Current: true}})
	b.Bus = bus
	var created []string
	b.Create = func(_ context.Context, _, name string) error {
		created = append(created, name)
		return nil
	}

	b.Run([]string{"new-work"})
	waitFor(t, bus, "Created new-work")

	assert.Equal(t, []string{"new-work"}, created)
}

func TestRunWithNameSwitchesExistingBranch(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "main", Current: true}, {Name: "fix/x"}})
	b.Bus = bus
	var switched []string
	b.Switch = func(_ context.Context, _, ref string) error {
		switched = append(switched, ref)
		return nil
	}
	created := false
	b.Create = func(context.Context, string, string) error {
		created = true
		return nil
	}

	b.Run([]string{"fix/x"})
	waitFor(t, bus, "Switched to fix/x")

	assert.Equal(t, []string{"fix/x"}, switched)
	assert.False(t, created)
}

func TestRunWithRemoteNameChecksOutItsLocalBranch(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{
		{Name: "main", Current: true},
		{Name: "origin/feat", Remote: true},
	})
	b.Bus = bus
	var switched []string
	b.Switch = func(_ context.Context, _, ref string) error {
		switched = append(switched, ref)
		return nil
	}

	// Both the row id and a typed origin/feat land on the local "feat", which is
	// what git creates as a tracking branch.
	b.Run([]string{"origin/feat"})
	waitFor(t, bus, "Switched to feat")
	b.Run([]string{"feat"})
	waitFor(t, bus, "Switched to feat")

	assert.Equal(t, []string{"feat", "feat"}, switched)
}

func TestRunRejectsOptionLikeNames(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "main", Current: true}})
	b.Bus = bus
	called := false
	b.Switch = func(context.Context, string, string) error {
		called = true
		return nil
	}
	b.Create = func(context.Context, string, string) error {
		called = true
		return nil
	}

	b.Run([]string{"--detach"})

	assert.False(t, called)
	assert.Contains(t, drainToast(t, bus), "Not a branch name")
}

func TestRunWithTooManyArgumentsToastsUsage(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands(nil)
	b.Bus = bus

	b.Run([]string{"a", "b"})

	assert.Contains(t, drainToast(t, bus), "Usage: /branch [name]")
}

func TestSwitchToRefusedDuringMergeOrRebase(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "main", Current: true}})
	b.Bus = bus
	b.Preflight = func(context.Context, string) (gitx.Status, error) {
		return gitx.Status{Dirty: 3, Op: "rebase"}, nil
	}
	called := false
	b.Switch = func(context.Context, string, string) error {
		called = true
		return nil
	}

	b.SwitchTo("other")

	assert.False(t, called, "git will not let an unfinished rebase switch")
	assert.Contains(t, drainToast(t, bus), "in-progress rebase")
}

func TestBranchShowReportsGitFailure(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands(nil)
	b.Bus = bus
	b.Branches = func(context.Context, string) ([]gitx.Branch, error) {
		return nil, errors.New("git for-each-ref: not a git repository")
	}

	b.Show()

	assert.Contains(t, drainToast(t, bus), "not a git repository")
}

func TestBranchShowWithoutBranchesToasts(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands(nil)
	b.Bus = bus

	b.Show()

	assert.Contains(t, drainToast(t, bus), "commit something first")
}

func TestBranchShowIgnoresMissingReflog(t *testing.T) {
	b := stubBranchCommands([]gitx.Branch{{Name: "main", Current: true}})
	b.Recent = func(context.Context, string) ([]string, error) {
		return nil, errors.New("fatal: your current branch does not have any commits yet")
	}
	opened := false
	b.OpenOverlay = func(_ []gitx.Branch, recent []string, _, _ func(string)) {
		opened = true
		assert.Empty(t, recent)
	}

	b.Show()

	assert.True(t, opened, "row order must not gate switching")
}

func TestSwitchToReportsFailureAndSuccess(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands(nil)
	b.Bus = bus
	b.Dir = func() string { return "/repo" }

	var switched []string
	b.Switch = func(_ context.Context, _, ref string) error {
		switched = append(switched, ref)
		return errors.New("git switch target: your local changes would be overwritten — commit or stash first")
	}

	b.SwitchTo("target")
	waitFor(t, bus, "your local changes")

	b.Switch = func(_ context.Context, _, ref string) error {
		switched = append(switched, ref)
		return nil
	}
	b.SwitchTo("target")
	waitFor(t, bus, "Switched to target")
	assert.Equal(t, []string{"target", "target"}, switched)
}

func TestSwitchToRefusesToClaimAMoveWhenAlreadyThere(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands(nil)
	b.Bus = bus
	b.CurrentBranch = func(context.Context, string) string { return "main" }
	called := false
	b.Switch = func(context.Context, string, string) error {
		called = true
		return nil
	}

	b.SwitchTo("main")
	waitFor(t, bus, "Already on main")

	assert.False(t, called, "git would answer 'Switched to branch' for a no-op")
}

func TestSwitchToRefusedWhileStreaming(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands(nil)
	b.Bus = bus
	b.StreamActive = func() bool { return true }
	called := false
	b.Switch = func(context.Context, string, string) error {
		called = true
		return nil
	}

	b.SwitchTo("target")

	assert.False(t, called, "a running reply must not be repointed at another branch")
	assert.Contains(t, drainToast(t, bus), "Cannot switch branches")
}

func TestDeleteRemovesTheChosenBranchAndRefreshesRows(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "main", Current: true}, {Name: "fix/x"}})
	b.Bus = bus
	var removed []string
	b.Remove = func(_ context.Context, _, name string) error {
		removed = append(removed, name)
		return nil
	}
	b.Branches = func(context.Context, string) ([]gitx.Branch, error) {
		if len(removed) == 0 {
			return []gitx.Branch{{Name: "main", Current: true}, {Name: "fix/x"}}, nil
		}
		return []gitx.Branch{{Name: "main", Current: true}}, nil
	}
	var refreshed []string
	b.RefreshOverlay = func(branches []gitx.Branch, _ []string) {
		for _, item := range branches {
			refreshed = append(refreshed, item.Name)
		}
	}

	b.Delete("fix/x")

	assert.Equal(t, []string{"fix/x"}, removed)
	assert.Equal(t, []string{"main"}, refreshed, "the row is gone without reopening the picker")
	assert.Contains(t, drainToast(t, bus), "Deleted fix/x")
}

func TestDeleteRefusesTheCheckedOutBranch(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "main", Current: true}})
	b.Bus = bus
	called := false
	b.Remove = func(context.Context, string, string) error {
		called = true
		return nil
	}

	b.Delete("main")

	assert.False(t, called, "git would refuse with a path-laden error")
	assert.Contains(t, drainToast(t, bus), "switch to another branch first")
}

func TestDeleteRefusesRemoteTrackingBranches(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "feat"}, {Name: "origin/feat", Remote: true}})
	b.Bus = bus
	called := false
	b.Remove = func(context.Context, string, string) error {
		called = true
		return nil
	}

	b.Delete("origin/feat")

	assert.False(t, called, "the local feat is a different branch")
	assert.Contains(t, drainToast(t, bus), "Cannot delete remote-tracking origin/feat")
}

func TestDeleteReportsGitRefusal(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "fix/x"}})
	b.Bus = bus
	b.Remove = func(context.Context, string, string) error {
		return errors.New("git branch -d fix/x: the branch 'fix/x' is not fully merged — merge or rebase it first")
	}

	b.Delete("fix/x")

	assert.Contains(t, drainToast(t, bus), "not fully merged")
}

func TestDeleteRefusedWhileStreaming(t *testing.T) {
	bus := controller.NewBus(nil)
	b := stubBranchCommands([]gitx.Branch{{Name: "fix/x"}})
	b.Bus = bus
	b.StreamActive = func() bool { return true }
	called := false
	b.Remove = func(context.Context, string, string) error {
		called = true
		return nil
	}

	b.Delete("fix/x")

	assert.False(t, called)
	assert.Contains(t, drainToast(t, bus), "Cannot delete a branch")
}

func stubBranchCommands(branches []gitx.Branch) *BranchCommands {
	b := NewBranchCommands(nil)
	b.Dir = func() string { return "/repo" }
	b.CurrentBranch = func(context.Context, string) string { return "" }
	b.Branches = func(context.Context, string) ([]gitx.Branch, error) { return branches, nil }
	b.Recent = func(context.Context, string) ([]string, error) { return []string{"fix/x"}, nil }
	b.Preflight = func(context.Context, string) (gitx.Status, error) { return gitx.Status{}, nil }
	b.Switch = func(context.Context, string, string) error { return nil }
	b.Create = func(context.Context, string, string) error { return nil }
	b.Remove = func(context.Context, string, string) error { return nil }
	return b
}

// waitFor drains the bus until a toast containing want shows up. The checkout
// runs on a goroutine, so the message is not there the instant SwitchTo returns.
func waitFor(t *testing.T, bus *controller.Bus, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range bus.Drain() {
			if msg, ok := m.(controller.ToastMsg); ok && strings.Contains(msg.Message, want) {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.FailNow(t, "no toast containing", want)
}
