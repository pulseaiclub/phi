package submit

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
	imgutil "github.com/pulseaiclub/phi/internal/util/image"
)

type stubComposer struct {
	skills []string
	images []imgutil.Attachment
	input  string
	shell  bool
}

func (stubComposer) HideCompleters()                       {}
func (s *stubComposer) ClearInput()                        { s.input = "" }
func (s *stubComposer) SetInput(text string)               { s.input = text }
func (s stubComposer) PendingSkills() []string             { return s.skills }
func (s stubComposer) PendingImages() []imgutil.Attachment { return s.images }
func (stubComposer) PendingRefs() []chat.Ref               { return nil }
func (stubComposer) ClearPendingSkills()                   {}
func (stubComposer) ClearPendingImages()                   {}
func (stubComposer) ClearPendingRefs()                     {}
func (stubComposer) SyncBashBorder(string)                 {}
func (stubComposer) CloseMentionSlash()                    {}
func (stubComposer) SetBashBorderActive(bool)              {}
func (s *stubComposer) ShellMode() bool                    { return s.shell }

// newTestSubmitter wires a Submitter over a zero EngineController: SessionDir
// is empty, so no shell history is created. Tests must not reach
// StartPrompt — that path needs a live controller.
func newTestSubmitter(
	t *testing.T,
	tp *transcript.TranscriptPane,
	activity *controller.ActivityHandler,
	composer *stubComposer,
	cmds *commands.CommandRegistry,
) *Submitter {
	t.Helper()
	if composer == nil {
		composer = &stubComposer{}
	}
	return NewSubmitter(
		&controller.EngineController{},
		cmds,
		tp,
		activity,
		composer,
		nil,
		func() commands.Context { return commands.NewContext(nil, nil) },
		nil, nil, nil,
		nil, nil, nil,
	)
}

// Tests driving Submit all the way into the agent hop were removed with the
// Submitter's nil guards: they only passed because a nil ctrl silently no-oped
// in handleUserInput, and a zero EngineController crashes runLoop for lack of
// a bus. That hop is covered by the editor-level tests.

func TestSubmitter_IsBusy(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	tp := transcript.NewTranscriptPane(th, spin, "Phi test")
	sub := newTestSubmitter(t, tp, nil, nil, nil)
	assert.False(t, sub.IsBusy())
}

// Esc has to cancel a request that is in flight but has not streamed a token
// yet: the transcript is empty there, so a snapshot-only check reads "idle".
func TestSubmitter_IsBusy_requestInFlight(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	activity := controller.NewActivityHandler(spin)
	sub := newTestSubmitter(t, transcript.NewTranscriptPane(th, spin, "Phi test"), activity, nil, nil)

	for _, a := range []controller.Activity{
		controller.ActivitySubmitting,
		controller.ActivityWaiting,
		controller.ActivityStreaming,
		controller.ActivityRetrying,
	} {
		activity.Apply(a)
		assert.True(t, sub.IsBusy(), "activity %v must stay cancelable", a)
	}

	activity.Apply(controller.ActivityIdle)
	assert.False(t, sub.IsBusy())
}

func TestSubmitter_StreamActive_activity(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	activity := controller.NewActivityHandler(spin)
	sub := newTestSubmitter(t, transcript.NewTranscriptPane(th, spin, "Phi test"), activity, nil, nil)
	activity.Apply(controller.ActivityWaiting)
	assert.True(t, sub.StreamActive())
}

func TestSubmitter_Submit_needsArgsRefillsComposer(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	tp := transcript.NewTranscriptPane(th, spin, "Phi test")
	comp := &stubComposer{}
	reg := commands.NewCommandRegistry()
	reg.Register(commands.Command{
		Name:      "plan",
		Slash:     true,
		NeedsArgs: true,
		Run:       func(commands.Context, []string) error { return nil },
	})
	sub := newTestSubmitter(t, tp, nil, comp, reg)
	sub.Submit("/plan")
	assert.Equal(t, "/plan ", comp.input)
	assert.Empty(t, tp.Snapshot().Messages)
}
