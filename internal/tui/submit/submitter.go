package submit

import (
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
	imgutil "github.com/pulseaiclub/phi/internal/util/image"
)

// Submitter owns submit / cancel / slash dispatch and coordinates bash runs.
type Submitter struct {
	ctrl       *controller.EngineController
	commands   *commands.CommandRegistry
	transcript *transcript.TranscriptPane
	activity   *controller.ActivityHandler
	composer   composer.Input
	bash       *BashRunner

	commandContext func() commands.Context
	bus            *controller.Bus

	permissionActive  func() bool
	continueActive    func() bool
	confirmActive     func() bool
	resolvePermission func(controller.AskReply)
	resolveContinue   func(controller.ContinueReply)
	resolveConfirm    func(controller.ExtConfirmReply)
}

// NewSubmitter builds a Submitter from explicit collaborators (no *Editor back-pointer).
// BashRunner is composed internally from transcript/composer/bus.
func NewSubmitter(
	ctrl *controller.EngineController,
	commands *commands.CommandRegistry,
	transcript *transcript.TranscriptPane,
	activity *controller.ActivityHandler,
	composer composer.Input,
	bus *controller.Bus,
	commandContext func() commands.Context,
	permissionActive func() bool,
	continueActive func() bool,
	confirmActive func() bool,
	resolvePermission func(controller.AskReply),
	resolveContinue func(controller.ContinueReply),
	resolveConfirm func(controller.ExtConfirmReply),
) *Submitter {
	return &Submitter{
		ctrl:              ctrl,
		commands:          commands,
		transcript:        transcript,
		activity:          activity,
		composer:          composer,
		bash:              newBashRunner(transcript, composer, bus),
		commandContext:    commandContext,
		bus:               bus,
		permissionActive:  permissionActive,
		continueActive:    continueActive,
		confirmActive:     confirmActive,
		resolvePermission: resolvePermission,
		resolveContinue:   resolveContinue,
		resolveConfirm:    resolveConfirm,
	}
}

// SyncBashBorder updates composer chrome for "!cmd" prefix.
func (s *Submitter) SyncBashBorder(text string) {
	if s == nil || s.bash == nil {
		return
	}
	s.bash.SyncBorder(text)
}

// Submit handles a user prompt from the composer (agent, slash, or bash).
func (s *Submitter) Submit(text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "!") {
		if s.bash != nil && s.bash.HandleSubmit(text) {
			return
		}
	}
	if strings.HasPrefix(text, "/") {
		if insert, ok := s.incompleteSlash(text); ok {
			s.composer.HideCompleters()
			s.composer.SetInput(insert)
			s.composer.SyncBashBorder("")
			return
		}
		if s.dispatchSlash(text) {
			s.composer.HideCompleters()
			s.composer.ClearInput()
			s.composer.ClearPendingImages()
			s.composer.SyncBashBorder("")
			return
		}
	}
	s.handleUserInput(text)
}

func buildUserDisplay(text string, refs []chat.Ref, skills []string, images []imgutil.Attachment) string {
	var parts []string
	if len(refs) > 0 {
		labels := make([]string, len(refs))
		for i, r := range refs {
			labels[i] = r.Label()
		}
		parts = append(parts, "Refs: "+strings.Join(labels, " "))
	}
	if len(skills) > 0 {
		parts = append(parts, "Skills: "+strings.Join(skills, ", "))
	}
	if len(images) > 0 {
		labels := make([]string, len(images))
		for i, att := range images {
			labels[i] = att.Label
		}
		parts = append(parts, "Images: "+strings.Join(labels, " "))
	}
	text = strings.TrimSpace(text)
	if text != "" {
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}

// promptWithRefs expands attached references into fenced blocks ahead of the
// typed prompt. The model gets the selected lines; the transcript keeps the
// chip labels from buildUserDisplay.
func promptWithRefs(text string, refs []chat.Ref) string {
	if len(refs) == 0 {
		return text
	}
	blocks := make([]string, len(refs))
	for i, r := range refs {
		blocks[i] = r.Block()
	}
	expanded := strings.Join(blocks, "\n\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return expanded
	}
	return expanded + "\n\n" + text
}

func (s *Submitter) handleUserInput(text string) {
	pendingRefs := s.composer.PendingRefs()
	pendingSkills := s.composer.PendingSkills()
	pendingImages := s.composer.PendingImages()
	if (text == "" && len(pendingRefs) == 0 && len(pendingSkills) == 0 && len(pendingImages) == 0) || s.IsBusy() {
		return
	}

	s.composer.CloseMentionSlash()

	llmImages := make([]llm.Image, 0, len(pendingImages))
	for _, att := range pendingImages {
		llmImages = append(llmImages, imgutil.ToLLM(att.Result))
	}

	s.activity.Apply(controller.ActivitySubmitting)
	display := buildUserDisplay(text, pendingRefs, pendingSkills, pendingImages)
	s.transcript.ApplySession(session.UserAppend{Text: display, Images: llmImages})
	s.transcript.Sync()
	s.transcript.StickToBottom()

	s.activity.Apply(controller.ActivityWaiting)

	s.composer.ClearInput()
	s.composer.ClearPendingSkills()
	s.composer.ClearPendingImages()
	s.composer.ClearPendingRefs()

	if s.ctrl != nil {
		s.ctrl.StartPrompt(promptWithRefs(text, pendingRefs), pendingSkills, llmImages)
	}
}

// Cancel aborts overlays, bash, or the in-flight agent stream.
func (s *Submitter) Cancel() {
	if s == nil {
		return
	}
	if s.resolvePermission != nil && s.permissionActive != nil && s.permissionActive() {
		s.resolvePermission(controller.AskReply{})
	}
	if s.resolveContinue != nil && s.continueActive != nil && s.continueActive() {
		s.resolveContinue(controller.ContinueReply{})
	}
	if s.resolveConfirm != nil && s.confirmActive != nil && s.confirmActive() {
		s.resolveConfirm(controller.ExtConfirmReply{})
	}
	if s.bash != nil && s.bash.Cancel() {
		return
	}
	if s.ctrl != nil {
		s.ctrl.Cancel()
	}
	s.transcript.ApplySession(session.CancelStreaming{})
	s.transcript.Sync()
	s.activity.Apply(controller.ActivityCancelled)
	time.AfterFunc(1200*time.Millisecond, func() {
		s.bus.Publish(controller.FooterMsg{Kind: controller.FooterClearIfActivity, If: controller.ActivityCancelled})
	})
}

// RunningBash reports whether a local "!cmd" is in flight.
func (s *Submitter) RunningBash() bool {
	if s == nil || s.bash == nil {
		return false
	}
	return s.bash.Running()
}

// IsBusy reports agent stream or local bash activity.
func (s *Submitter) IsBusy() bool {
	if s == nil {
		return false
	}
	if s.transcript != nil && s.transcript.IsStreaming() {
		return true
	}
	return s.bash != nil && s.bash.Running()
}

// StreamActive reports whether user input should be blocked for stream/overlays.
func (s *Submitter) StreamActive() bool {
	if s == nil {
		return false
	}
	if s.IsBusy() ||
		(s.permissionActive != nil && s.permissionActive()) ||
		(s.continueActive != nil && s.continueActive()) {
		return true
	}
	if s.activity == nil {
		return false
	}
	switch s.activity.Current {
	case controller.ActivitySubmitting,
		controller.ActivityWaiting,
		controller.ActivityStreaming,
		controller.ActivityTools,
		controller.ActivityCompacting,
		controller.ActivityAwaitingApproval,
		controller.ActivityRetrying:
		return true
	default:
		return false
	}
}

func (s *Submitter) dispatchSlash(text string) bool {
	if s.commands == nil || s.commandContext == nil {
		return false
	}
	return s.commands.DispatchSlash(text, s.commandContext())
}

func (s *Submitter) incompleteSlash(text string) (string, bool) {
	if s == nil || s.commands == nil {
		return "", false
	}
	return s.commands.IncompleteSlash(text)
}
