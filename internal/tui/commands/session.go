package commands

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/footer"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
)

// SessionCommands owns /sessions and /new UI side effects.
type SessionCommands struct {
	Ctrl       *controller.EngineController
	Transcript *transcript.TranscriptPane
	Footer     *footer.FooterChrome
	Bus        *controller.Bus
	SyncHooks  func()
	// OpenPicker opens the session list overlay (wired by Builtin.Bind).
	OpenPicker func(items []session.SessionMeta, currentID string)
	// RefreshPicker swaps rows into the open overlay (wired by Builtin.Bind).
	RefreshPicker func(items []session.SessionMeta, currentID string)
	// StreamActive reports whether resume/new should be blocked.
	StreamActive func() bool
	// SessionDir / SessionID override Ctrl for tests when set.
	SessionDir func() string
	SessionID  func() string
}

// NewSessionCommands builds session command handlers.
func NewSessionCommands(
	ctrl *controller.EngineController,
	transcript *transcript.TranscriptPane,
	footer *footer.FooterChrome,
	bus *controller.Bus,
	syncHooks func(),
) *SessionCommands {
	return &SessionCommands{
		Ctrl:       ctrl,
		Transcript: transcript,
		Footer:     footer,
		Bus:        bus,
		SyncHooks:  syncHooks,
	}
}

// Register wires /sessions and /new into r.
func (s *SessionCommands) Register(r *CommandRegistry) {
	r.Register(Command{
		Name:        "sessions",
		Description: "Browse and resume sessions for this directory",
		Slash:       true,
		Insert:      "/sessions",
		Run: func(_ Context, _ []string) error {
			s.Show()
			return nil
		},
	})
	r.Register(Command{
		Name:        "new",
		Description: "Start a fresh empty session",
		Slash:       true,
		Insert:      "/new",
		Run: func(_ Context, _ []string) error {
			s.NewSession()
			return nil
		},
	})
}

// Show opens the session picker for the current session directory.
func (s *SessionCommands) Show() {
	dir, currentID := s.dirAndID()
	list, err := session.ListSessions(dir)
	if err != nil {
		publishToast(s.Bus, err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	if len(list) == 0 {
		publishToast(s.Bus, "No sessions for this directory", toast.ToastWarning, 3*time.Second)
		return
	}
	s.OpenPicker(list, currentID)
}

func (s *SessionCommands) dirAndID() (dir, currentID string) {
	if s.SessionDir != nil {
		dir = s.SessionDir()
	} else if s.Ctrl != nil {
		dir = s.Ctrl.SessionDir()
	}
	if s.SessionID != nil {
		currentID = s.SessionID()
	} else if s.Ctrl != nil {
		currentID = s.Ctrl.SessionID()
	}
	return dir, currentID
}

// Accept resumes the chosen session (used by the session list overlay).
func (s *SessionCommands) Accept(id string) {
	if s.StreamActive != nil && s.StreamActive() {
		publishToast(s.Bus, "Cannot resume while a reply or command is running", toast.ToastWarning, 3*time.Second)
		return
	}
	s.resume(id)
}

// resume loads a prior session by id into the UI.
func (s *SessionCommands) resume(id string) {
	warn, err := s.Ctrl.Resume(id)
	if err != nil {
		publishToast(s.Bus, err.Error(), toast.ToastError, 4*time.Second)
		return
	}
	if s.SyncHooks != nil {
		s.SyncHooks()
	}
	snap := s.Ctrl.ReplaySnapshot()
	s.Transcript.LoadReplay(snap)
	s.Transcript.Sync()
	s.Transcript.StickToBottom()
	// The resumed session carries its own last usage; without this the footer
	// keeps showing the counts of the session we just left.
	s.Footer.UpdateTokenDisplay(snap.LastUsage())
	msg := "Resumed " + shortSessionID(s.Ctrl.SessionID())
	if warn != "" {
		publishToast(s.Bus, msg+" · "+warn, toast.ToastWarning, 4*time.Second)
		return
	}
	publishToast(s.Bus, msg, toast.ToastSuccess, 3*time.Second)
}

// Delete removes a session chosen in the picker overlay (Ctrl+X, then Y).
// Deleting the current session also resets to a fresh one: the engine must
// not keep appending to a file the user asked to remove.
func (s *SessionCommands) Delete(id string) {
	if s.StreamActive != nil && s.StreamActive() {
		publishToast(s.Bus, "Cannot delete while a reply or command is running", toast.ToastWarning, 3*time.Second)
		return
	}
	dir, currentID := s.dirAndID()
	if err := session.DeleteSession(dir, id); err != nil {
		publishToast(s.Bus, err.Error(), toast.ToastError, 4*time.Second)
		return
	}
	publishToast(s.Bus, "Deleted "+shortSessionID(id), toast.ToastSuccess, 3*time.Second)
	if id == currentID {
		s.NewSession()
	}
	s.refreshPicker()
}

// refreshPicker re-lists sessions and pushes the rows into the open overlay
// in place, keeping the filter query instead of reopening via Show.
func (s *SessionCommands) refreshPicker() {
	if s.RefreshPicker == nil {
		return
	}
	dir, currentID := s.dirAndID()
	list, err := session.ListSessions(dir)
	if err != nil {
		return // listing worked moments ago; keep the rows already on screen
	}
	s.RefreshPicker(list, currentID)
}

// NewSession starts a new empty session. Blocks while a stream or extension command is active.
func (s *SessionCommands) NewSession() {
	if s.StreamActive != nil && s.StreamActive() {
		publishToast(
			s.Bus,
			"Cannot start a new session while a reply or command is running",
			toast.ToastWarning,
			3*time.Second,
		)
		return
	}
	if err := s.Ctrl.NewSession(); err != nil {
		publishToast(s.Bus, err.Error(), toast.ToastError, 4*time.Second)
		return
	}
	s.Transcript.LoadReplay(s.Ctrl.ReplaySnapshot())
	s.Transcript.ResetSubagents()
	s.Footer.ClearTokenDisplay()
	s.Footer.Activity().Apply(controller.ActivityIdle)
	s.Transcript.Sync()
	s.Transcript.StickToBottom()
	publishToast(s.Bus, "New session "+shortSessionID(s.Ctrl.SessionID()), toast.ToastSuccess, 3*time.Second)
}

func shortSessionID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
