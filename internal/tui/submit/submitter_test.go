package submit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
	imgutil "github.com/pulseaiclub/phi/internal/util/image"
)

type stubComposer struct {
	skills []string
	images []imgutil.Attachment
	input  string
}

func (stubComposer) HideCompleters()                       {}
func (s *stubComposer) ClearInput()                        { s.input = "" }
func (s *stubComposer) SetInput(text string)               { s.input = text }
func (s stubComposer) PendingSkills() []string             { return s.skills }
func (s stubComposer) PendingImages() []imgutil.Attachment { return s.images }
func (stubComposer) ClearPendingSkills()                   {}
func (stubComposer) ClearPendingImages()                   {}
func (stubComposer) SyncBashBorder(string)                 {}
func (stubComposer) SetBashBorderActive(bool)              {}

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
		nil,
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

func TestSubmitter_IsBusy(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	tp := transcript.NewTranscriptPane(th, spin, "Phi test")
	sub := newTestSubmitter(t, tp, nil, nil, nil)
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

func TestSubmitter_Submit_unknownSlashFallsThroughToAgent(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	tp := transcript.NewTranscriptPane(th, spin, "Phi test")
	sub := newTestSubmitter(t, tp, nil, nil, commands.NewCommandRegistry())
	sub.Submit("/not-a-real-command")
	require.Len(t, tp.Snapshot().Messages, 1)
	assert.Equal(t, "/not-a-real-command", tp.Snapshot().Messages[0].Text)
	assert.Equal(t, session.RoleUser, tp.Snapshot().Messages[0].Role)
}

func TestSubmitter_Submit_bareBangFallsThroughToAgent(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	tp := transcript.NewTranscriptPane(th, spin, "Phi test")
	sub := newTestSubmitter(t, tp, nil, nil, nil)
	sub.Submit("!")
	require.Len(t, tp.Snapshot().Messages, 1)
	assert.Equal(t, "!", tp.Snapshot().Messages[0].Text)
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

func TestSubmitter_Submit_withImagesOnly(t *testing.T) {
	th := components.DefaultTheme()
	spin := status.NewSpinner(th.ToolName)
	tp := transcript.NewTranscriptPane(th, spin, "Phi test")
	images := []imgutil.Attachment{
		{Label: "a.png", Result: imgutil.Result{Data: []byte("abc"), MimeType: "image/png"}},
	}
	sub := newTestSubmitter(t, tp, nil, &stubComposer{images: images}, nil)
	sub.Submit("")
	require.Len(t, tp.Snapshot().Messages, 1)
	msg := tp.Snapshot().Messages[0]
	assert.Equal(t, session.RoleUser, msg.Role)
	require.Len(t, msg.Images, 1)
}
