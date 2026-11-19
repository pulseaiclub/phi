// Package sessions owns the session command domain (/sessions, /new, /fork):
// the session picker round-trip and the UI reset that follows a switch.
package sessions

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// ModelLabels is the composer surface that shows the model/think label.
type ModelLabels interface {
	SetModelLabel(name, thinkLevel string)
}

// ActivitySink resets activity when the session view resets.
type ActivitySink interface {
	Apply(controller.Activity)
}

// ReplayStore is the transcript surface a session switch reprojects.
type ReplayStore interface {
	LoadReplay(snap session.Snapshot)
	ResetSubagents()
	Sync()
	StickToBottom()
}

// SessionListHost is the composer's session picker.
type SessionListHost interface {
	ShowSessionList(items []session.SessionMeta, currentID string, onAccept, onDelete func(string))
	RefreshSessionList(items []session.SessionMeta)
}

// SessionCommands owns session command UI side effects.
type SessionCommands struct {
	Ctrl     *controller.EngineController
	Labels   ModelLabels
	Store    ReplayStore
	Lists    SessionListHost
	Bus      *controller.Bus
	Activity ActivitySink
	// StreamActive reports whether session changes should be blocked.
	StreamActive func() bool
	// SessionDir / SessionID override Ctrl for tests when set.
	SessionDir func() string
	SessionID  func() string

	openPicker  func(items []session.SessionMeta, currentID string)
	refreshRows func(items []session.SessionMeta)
}

// New builds the session command domain and registers /new, /sessions, /fork.
// The picker callbacks bind this plugin's own Accept/Delete, so the composer
// never sees command-domain handlers.
func New(r *commands.CommandRegistry, deps SessionCommands) *SessionCommands {
	s := &deps
	s.openPicker = s.openList
	s.refreshRows = s.pushRefreshedRows
	s.Register(r)
	return s
}

func (s *SessionCommands) openList(items []session.SessionMeta, currentID string) {
	if s.Lists != nil {
		s.Lists.ShowSessionList(items, currentID, s.Accept, s.Delete)
	}
}

// Register wires /sessions, /new and /fork into r.
func (s *SessionCommands) Register(r *commands.CommandRegistry) {
	if r == nil {
		return
	}
	r.Register(commands.Command{
		Name:        "new",
		Description: "Start a fresh empty session",
		Slash:       true,
		Insert:      "/new",
		Run: func(_ commands.Context, _ []string) {
			s.NewSession()
		},
	})
	r.Register(commands.Command{
		Name:        "sessions",
		Description: "Browse and resume sessions for this directory",
		Slash:       true,
		Insert:      "/sessions",
		Run: func(_ commands.Context, _ []string) {
			s.Show()
		},
	})
	r.Register(commands.Command{
		Name:        "fork",
		Description: "Copy this session at its current point into a new one",
		Slash:       true,
		Insert:      "/fork",
		Run: func(_ commands.Context, args []string) {
			s.Fork(args)
		},
	})
}

// OpenList opens the picker with this plugin's accept/delete handlers bound.
// The root wires composer.ShowSessionList here; the plugin owns the handlers.
func (s *SessionCommands) OpenList(
	items []session.SessionMeta,
	currentID string,
	show func(items []session.SessionMeta, currentID string, onAccept, onDelete func(string)),
) {
	show(items, currentID, s.Accept, s.Delete)
}

// Show opens the session picker for the current session directory.
func (s *SessionCommands) Show() {
	dir, currentID := s.dirAndID()
	list, err := session.ListSessions(dir)
	if err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 3*time.Second)
		return
	}
	if len(list) == 0 {
		commands.PublishToast(s.Bus, "No sessions for this directory", toast.ToastWarning, 3*time.Second)
		return
	}
	if s.openPicker == nil {
		return
	}
	s.openPicker(list, currentID)
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
	if s.blocked("resume") {
		return
	}
	s.resume(id)
}

// resume loads a prior session by id into the UI.
func (s *SessionCommands) resume(id string) {
	warn, err := s.Ctrl.Resume(id)
	if err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 4*time.Second)
		return
	}
	snap := s.Ctrl.ReplaySnapshot()
	s.Store.LoadReplay(snap)
	s.Store.Sync()
	s.Store.StickToBottom()
	s.refreshModel()
	msg := "Resumed " + session.ShortID(s.Ctrl.SessionID())
	if warn != "" {
		commands.PublishToast(s.Bus, msg+" · "+warn, toast.ToastWarning, 4*time.Second)
		return
	}
	commands.PublishToast(s.Bus, msg, toast.ToastSuccess, 3*time.Second)
}

// Delete removes a session chosen in the picker overlay (Ctrl+X, then Y).
// Deleting the current session also resets to a fresh one: the engine must
// not keep appending to a file the user asked to remove.
func (s *SessionCommands) Delete(id string) {
	if s.blocked("delete") {
		return
	}
	dir, currentID := s.dirAndID()
	resolved, err := session.DeleteSession(dir, id)
	if err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 4*time.Second)
		return
	}
	commands.PublishToast(s.Bus, "Deleted "+session.ShortID(resolved), toast.ToastSuccess, 3*time.Second)
	if resolved == currentID {
		s.NewSession()
	}
	s.refreshPicker()
}

// refreshPicker re-lists sessions and pushes the rows into the open overlay
// in place, keeping the filter query instead of reopening via Show.
func (s *SessionCommands) refreshPicker() {
	dir, _ := s.dirAndID()
	list, err := session.ListSessions(dir)
	if err != nil {
		return // listing worked moments ago; keep the rows already on screen
	}
	s.refreshRows(list)
}

func (s *SessionCommands) pushRefreshedRows(list []session.SessionMeta) {
	if s.Lists != nil {
		s.Lists.RefreshSessionList(list)
	}
}

// NewSession starts a new empty session. Blocks while a stream is active.
func (s *SessionCommands) NewSession() {
	if s.blocked("start a new session") {
		return
	}
	if err := s.Ctrl.NewSession(); err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 4*time.Second)
		return
	}
	s.resetSessionView()
	commands.PublishToast(s.Bus, "New session "+session.ShortID(s.Ctrl.SessionID()), toast.ToastSuccess, 3*time.Second)
}

// Fork copies the current session at its current point: both sessions keep
// going from here with the same history. Anything typed after /fork is
// reported instead of ignored.
func (s *SessionCommands) Fork(args []string) {
	if len(args) > 0 {
		commands.PublishToast(s.Bus, "Usage: /fork", toast.ToastWarning, 3*time.Second)
		return
	}
	if s.blocked("fork") {
		return
	}
	id, err := s.Ctrl.ForkSession()
	if err != nil {
		commands.PublishToast(s.Bus, err.Error(), toast.ToastError, 4*time.Second)
		return
	}
	s.resetSessionView()
	commands.PublishToast(s.Bus, "Forked "+session.ShortID(id), toast.ToastSuccess, 3*time.Second)
}

// blocked reports whether a session switch has to wait, and says why.
func (s *SessionCommands) blocked(action string) bool {
	if s.StreamActive == nil || !s.StreamActive() {
		return false
	}
	commands.PublishToast(
		s.Bus,
		"Cannot "+action+" while a reply or command is running",
		toast.ToastWarning,
		3*time.Second,
	)
	return true
}

// resetSessionView clears transient UI state when starting a new or forked session.
func (s *SessionCommands) resetSessionView() {
	s.Store.LoadReplay(s.Ctrl.ReplaySnapshot())
	s.Store.ResetSubagents()
	s.refreshModel()
	if s.Activity != nil {
		s.Activity.Apply(controller.ActivityIdle)
	}
	s.Store.Sync()
	s.Store.StickToBottom()
}

func (s *SessionCommands) refreshModel() {
	if s.Labels != nil {
		s.Labels.SetModelLabel(s.Ctrl.ModelName(), string(s.Ctrl.ThinkLevel()))
	}
}
