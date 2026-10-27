package composer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/commitmsg"
)

func TestCommitCompletionThroughInputAndPicker(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	git("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "example.txt"), []byte("hello\n"), 0o600))
	pane, bus := wiredComposer(t)
	generated := make(chan string, 1)
	complete := commitmsg.New(dir, func(_ context.Context, _, user string) (string, error) {
		generated <- user
		return "feat: add example", nil
	})
	pane.SetBashPredictor(NewCommitBashPredictor(NewBashPredictor(dir), complete.Complete))
	pane.Chat.ReplaceRange(0, 0, `!git commit -m "`)
	pane.ApplyBashSuggestions(drainBash(t, bus))
	assert.True(t, pane.bash.picker.Open)
	assert.Contains(t, pane.bash.picker.Status, "no staged changes")
	assert.Contains(t, pane.bash.picker.Status, "git add")
	assert.False(t, pane.Chat.BashOpen)
	assert.Empty(t, generated)

	git("add", "example.txt")
	pane.Chat.ReplaceRange(0, len(pane.Chat.Value), `!git commit -m "feat: `)
	pane.ApplyBashSuggestions(drainBash(t, bus))
	require.Len(t, pane.bash.picker.Items, 1)
	assert.Contains(t, <-generated, "+hello")
	assert.Equal(t, `git commit -m "feat: add example"`, pane.bash.picker.Items[0].Path)
	pane.bash.accept(pane.bash.picker.Items[0])
	assert.Equal(t, `!git commit -m "feat: add example"`, pane.Chat.Value)
	assert.False(t, pane.bash.picker.Open)
}
