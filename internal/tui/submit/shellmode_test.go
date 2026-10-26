package submit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session/shellhist"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
)

// In shell mode the bare text is the command: nothing is stripped, nothing is
// sent to the agent, and the run lands in the project shell history like a
// "!cmd" would.
func TestHandleShellSubmitRunsBareCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	th := components.DefaultTheme()
	pane := transcript.NewTranscriptPane(th, status.NewSpinner(th.ToolName), "Phi test")
	b := newBashRunner(pane, &stubComposer{}, controller.NewBus(nil), shellhist.New(dir))
	t.Cleanup(func() { b.Cancel() })

	require.True(t, b.HandleShellSubmit("printf shell-mode"))
	result := waitForShell(t, b)

	assert.Equal(t, "printf shell-mode", result.Detail)
	assert.Equal(t, "printf shell-mode", recordedEntries(t, dir)[0].Command)
}

// An empty shell-mode submit is not a command: it falls through so the
// submitter ignores it instead of flashing an empty transcript entry.
func TestHandleShellSubmitIgnoresEmptyText(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	th := components.DefaultTheme()
	pane := transcript.NewTranscriptPane(th, status.NewSpinner(th.ToolName), "Phi test")
	b := newBashRunner(pane, &stubComposer{}, controller.NewBus(nil), shellhist.New(dir))
	t.Cleanup(func() { b.Cancel() })

	assert.False(t, b.HandleShellSubmit("   "))
	assert.NoFileExists(t, dir+"/history.jsonl")
}

// The Submitter consults the composer's shell mode before the "!" prefix: in
// the mode, a prefix-less line runs locally and never reaches the agent hop.
func TestSubmitterShellModeRoutesToBash(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	pane := transcript.NewTranscriptPane(th, spin, "Phi test")
	composer := &stubComposer{shell: true}
	sub := NewSubmitter(
		&controller.EngineController{},
		nil,
		pane,
		nil,
		composer,
		controller.NewBus(nil),
		nil, nil, nil, nil, nil, nil, nil,
	)
	// The zero controller has no session dir, so NewSubmitter built the
	// runner without a history store; give it the test's store directly.
	sub.bash = newBashRunner(pane, composer, controller.NewBus(nil), shellhist.New(dir))
	t.Cleanup(func() { sub.bash.Cancel() })

	sub.Submit("printf routed")

	waitForShell(t, sub.bash)
	assert.Equal(t, "printf routed", recordedEntries(t, dir)[0].Command)
}

// Shell mode off, the same line is a prompt: nothing runs locally.
func TestSubmitterWithoutShellModeKeepsPromptRouting(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	pane := transcript.NewTranscriptPane(th, spin, "Phi test")
	sub := NewSubmitter(
		&controller.EngineController{},
		nil,
		pane,
		nil,
		&stubComposer{},
		controller.NewBus(nil),
		nil, nil, nil, nil, nil, nil, nil,
	)

	sub.Submit("printf not-a-command")

	assert.False(t, sub.bash.Running())
}
