package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

// Session owns the message store for the engine loop. It wraps a
// session.Manager and projects entries (including compaction summaries)
// into LLM context. Compaction policy lives on Engine, not here.
type Session struct {
	lastID            string
	manager           *session.Manager
	contextCache      []llm.Message
	contextCacheValid bool
}

// SessionOption configures NewSession.
type SessionOption func(*sessionConfig)

type sessionConfig struct {
	cwd        string
	sessionDir string
	persist    bool
	resumePath string
	resumeID   string
	parentID   string
}

// WithCwd sets the cwd written to SessionHeader (usually process cwd).
func WithCwd(cwd string) SessionOption {
	return func(c *sessionConfig) { c.cwd = cwd }
}

// WithSessionDir sets ~/.phi/session (required when Persist is true or resuming by id).
func WithSessionDir(dir string) SessionOption {
	return func(c *sessionConfig) { c.sessionDir = dir }
}

// WithPersist enables JSONL persistence (false → in-memory; tests default).
func WithPersist(persist bool) SessionOption {
	return func(c *sessionConfig) { c.persist = persist }
}

// WithResumePath opens this jsonl path (mutually exclusive with WithResumeID).
func WithResumePath(path string) SessionOption {
	return func(c *sessionConfig) { c.resumePath = path }
}

// WithResumeID resolves a session under SessionDir (mutually exclusive with WithResumePath).
func WithResumeID(id string) SessionOption {
	return func(c *sessionConfig) { c.resumeID = id }
}

// WithParentID links a new persisted session to a parent (sub-agents).
func WithParentID(id string) SessionOption {
	return func(c *sessionConfig) { c.parentID = id }
}

// NewSession creates a session wrapper according to opts.
func NewSession(opts ...SessionOption) (*Session, error) {
	var cfg sessionConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.resumePath != "" && cfg.resumeID != "" {
		return nil, errors.New("agent: ResumePath and ResumeID are mutually exclusive")
	}

	if cfg.resumePath != "" || cfg.resumeID != "" {
		path := cfg.resumePath
		if path == "" {
			if cfg.sessionDir == "" {
				return nil, errors.New("agent: SessionDir required to resume by id")
			}
			var err error
			path, err = session.FindSessionFile(cfg.sessionDir, cfg.resumeID)
			if err != nil {
				return nil, err
			}
		}
		m, err := session.OpenSession(path)
		if err != nil {
			return nil, err
		}
		return &Session{manager: m, lastID: m.LeafID()}, nil
	}

	if cfg.persist {
		if cfg.sessionDir == "" {
			return nil, errors.New("agent: SessionDir required when Persist is true")
		}
		m, err := session.NewSessionManager(cfg.cwd,
			session.WithSessionDir(cfg.sessionDir),
			session.WithShouldFlush(true),
			session.WithParent(cfg.parentID),
		)
		if err != nil {
			return nil, err
		}
		return &Session{manager: m}, nil
	}

	return &Session{manager: session.NewManager(cfg.cwd)}, nil
}

// Fork creates a new Session that shares the same underlying manager state
// but starts from the current leaf, allowing independent conversation branches.
func (s *Session) Fork() (*Session, error) {
	fork, err := s.manager.Fork()
	if err != nil {
		return nil, fmt.Errorf("agent: failed to fork session: %w", err)
	}
	return &Session{manager: fork, lastID: fork.LeafID()}, nil
}

// ID returns the durable session id (empty only if manager missing).
func (s *Session) ID() string {
	if s == nil || s.manager == nil {
		return ""
	}
	return s.manager.ID()
}

// File returns the JSONL path, or empty in memory mode / before first flush path assignment.
func (s *Session) File() string {
	if s == nil || s.manager == nil {
		return ""
	}
	return s.manager.File()
}

// Cwd returns the session header cwd.
func (s *Session) Cwd() string {
	if s == nil || s.manager == nil {
		return ""
	}
	return s.manager.Cwd()
}

func (s *Session) SetTitle(title string) error {
	return s.manager.SetTitle(title)
}

func (s *Session) Title() string { return s.manager.Title() }

func (s *Session) invalidateContextCache() {
	s.contextCacheValid = false
}

// Append records one or more messages.
func (s *Session) Append(message ...llm.Message) error {
	s.invalidateContextCache()
	for _, msg := range message {
		id, err := s.manager.Append(msg)
		if err != nil {
			return err
		}
		s.lastID = id
	}
	return nil
}

// AppendCompaction records a compaction entry and invalidates the context cache.
func (s *Session) AppendCompaction(c session.Compaction) error {
	s.invalidateContextCache()
	id, err := s.manager.AppendCompaction(c)
	if err != nil {
		return err
	}
	s.lastID = id
	return nil
}

// PathEntries returns the current leaf-to-root session entries for compaction.
func (s *Session) PathEntries() []session.MessageEntry {
	return s.manager.BuildContext()
}

// BuildContext returns the messages for LLM inference, oldest first.
// Compaction entries are projected as user messages carrying the summary.
func (s *Session) BuildContext() []llm.Message {
	if s.contextCacheValid {
		return s.contextCache
	}
	entries := s.manager.BuildContext()
	msgs := make([]llm.Message, 0, len(entries))
	for _, entry := range entries {
		switch entry.GetType() {
		case session.EntryCompaction:
			m := entry.(session.CompactionEntry)
			msgs = append(msgs, llm.Message{
				Role:    llm.RoleUser,
				Content: m.Compaction.Summary,
			})
		case session.EntryMessage:
			m := entry.(session.SessionMessageEntry)
			msgs = append(msgs, m.Message)
		case session.EntryBranchSummary:
			m := entry.(session.BranchSummaryEntry)
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "Context from another branch:\n" + m.Summary})
		case session.EntryHistory:
			m := entry.(session.HistoryEntry)
			if m.Kind == "custom" {
				msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: m.Text})
			}
		}
	}
	s.contextCache = normalizeToolResults(msgs)
	s.contextCacheValid = true
	return s.contextCache
}

// Len returns the number of stored entries (including the session header).
func (s *Session) Len() int {
	return s.manager.Len()
}

// LastID returns the ID of the most recently appended entry.
func (s *Session) LastID() string {
	return s.lastID
}

// AddUser records a single user message for this chat turn.
func (s *Session) AddUser(text string) error {
	return s.Append(llm.Message{
		Role:    llm.RoleUser,
		Content: text,
	})
}

// AddAssistant records an assistant message from an LLM response (including tool_calls).
func (s *Session) AddAssistant(assistant llm.Message, usage llm.Usage) error {
	return s.Append(llm.Message{
		Role:             llm.RoleAssistant,
		Content:          assistant.Content,
		ReasoningContent: assistant.ReasoningContent,
		ToolCalls:        assistant.ToolCalls,
		Usage:            usage,
	})
}

// AddFinalAssistant records the last assistant message when it carries text or tool_calls.
func (s *Session) AddFinalAssistant(resp llm.Response) error {
	if len(resp.Choices) == 0 {
		return nil
	}
	final := resp.Choices[0].Message
	if strings.TrimSpace(final.Content) == "" && len(final.ToolCalls) == 0 {
		return nil
	}
	return s.AddAssistant(final, resp.Usage)
}
