package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
)

// Manager is the single source of truth for session messages.
// Entries form a tree linked by parent IDs; context is built by walking
// from the current leaf back to the session root, honoring compaction.
//
// It is safe for concurrent use: mutations and reads take the internal lock.
type Manager struct {
	mu              sync.Mutex
	cwd             string
	entries         []MessageEntry // session header and session entries
	byIDs           map[string]MessageEntry
	sessionFile     string
	leafID          *string
	shouldFlush     bool
	flushed         bool
	sessionID       string
	config          ManagerConfig
	hasAssistantMsg bool
}

// ManagerConfig holds the options used to build a Manager.
type ManagerConfig struct {
	sessionDir  string
	shouldFlush bool
	parentID    string
	model       string
}

// ManagerOption applies a mutation to ManagerConfig.
type ManagerOption interface {
	Apply(config ManagerConfig) ManagerConfig
}

// OptionFunc adapts a function into a ManagerOption.
type OptionFunc func(config ManagerConfig) ManagerConfig

// Apply calls fn on config and returns the result.
func (fn OptionFunc) Apply(config ManagerConfig) ManagerConfig {
	return fn(config)
}

// WithShouldFlush returns an option that enables JSONL persistence.
func WithShouldFlush(shouldFlush bool) OptionFunc {
	return func(config ManagerConfig) ManagerConfig {
		config.shouldFlush = shouldFlush
		return config
	}
}

// WithSessionDir returns an option that sets the directory for session files.
func WithSessionDir(sessionDir string) OptionFunc {
	return func(config ManagerConfig) ManagerConfig {
		config.sessionDir = sessionDir
		return config
	}
}

// WithParent returns an option that links the session to a parent session ID.
func WithParent(sessionID string) OptionFunc {
	return func(config ManagerConfig) ManagerConfig {
		config.parentID = sessionID
		return config
	}
}

// WithModel records the initial model selection as an EntryModelChange entry.
func WithModel(name string) OptionFunc {
	return func(config ManagerConfig) ManagerConfig {
		config.model = name
		return config
	}
}

// NewSessionManager creates a session rooted at sessionPath. WithSessionDir +
// WithShouldFlush(true) enable persisting entries as JSONL.
func NewSessionManager(sessionPath string, opt ...ManagerOption) (*Manager, error) {
	config := ManagerConfig{}
	for _, o := range opt {
		config = o.Apply(config)
	}

	sessionID := generateSessionID()
	header := SessionHeader{
		Type:          EntrySession,
		ParentSession: config.parentID,
		ID:            sessionID,
		Timestamp:     time.Now().Format("2006-01-02T15-04-05"),
		Cwd:           sessionPath,
	}

	m := &Manager{
		cwd:         sessionPath,
		config:      config,
		entries:     make([]MessageEntry, 0, 64),
		byIDs:       make(map[string]MessageEntry, 64),
		leafID:      nil,
		sessionID:   sessionID,
		flushed:     false,
		shouldFlush: config.shouldFlush,
	}
	m.entries = append(m.entries, header)

	if config.shouldFlush {
		if err := os.MkdirAll(config.sessionDir, 0o755); err != nil {
			return nil, err
		}
		fileTimestamp := time.Now().Format("2006-01-02T15-04-05")
		m.sessionFile = filepath.Join(config.sessionDir, fmt.Sprintf("%s_%s.jsonl", fileTimestamp, m.sessionID))
	}
	if err := m.SetModel(config.model); err != nil {
		return nil, err
	}
	return m, nil
}

// NewManager creates an in-memory session manager (no persistence).
func NewManager(sessionDir string) *Manager {
	m, err := NewSessionManager(sessionDir, WithShouldFlush(false))
	if err != nil {
		panic(err) // cannot fail without flush
	}
	return m
}

// GetBranch returns the path of entries from fromID back to the session root,
// newest first.
func (sm *Manager) GetBranch(fromID string) []MessageEntry {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	return sm.getBranch(fromID)
}

// getBranch requires sm.mu to be held.
func (sm *Manager) getBranch(fromID string) []MessageEntry {
	var message []MessageEntry

	current := sm.byIDs[fromID]
	for current != nil {
		message = append(message, current)
		parent := current.GetParent()
		if parent == nil {
			break
		}
		current = sm.byIDs[*parent]
	}
	return message
}

// BuildContext returns the conversation path from the current leaf to the
// root, oldest first, with compaction applied. Model selections stay as state
// entries for projection; ModelName resolves them independently of compaction.
func (sm *Manager) BuildContext() []MessageEntry {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.leafID == nil {
		return nil
	}
	return buildSessionContext(sm.entries, *sm.leafID, sm.byIDs)
}

// Append adds a message as a new leaf and returns its entry ID.
func (sm *Manager) Append(msg llm.Message) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := SessionMessageEntry{
		SessionBaseEntry: SessionBaseEntry{
			Type:      EntryMessage,
			ID:        sm.generateID(),
			ParentID:  sm.leafID,
			Timestamp: time.Now(),
		},
		Message: msg,
		Usage:   msg.Usage,
	}
	if err := sm.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// AppendBash records a user "!!cmd" run as an in-context user message. The
// message is what the model reads; Bash lets replay draw the row as a bash
// block, so a resumed session does not turn the run into a user bubble.
func (sm *Manager) AppendBash(command, output string, exitCode int, cancelled bool) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := SessionMessageEntry{
		SessionBaseEntry: SessionBaseEntry{
			Type:      EntryMessage,
			ID:        sm.generateID(),
			ParentID:  sm.leafID,
			Timestamp: time.Now(),
		},
		Message: llm.Message{
			Role:    llm.RoleUser,
			Content: BashContextText(command, output, exitCode, cancelled),
		},
		Bash: &BashExecution{
			Command:   command,
			Output:    output,
			ExitCode:  exitCode,
			Cancelled: cancelled,
		},
	}
	if err := sm.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// Fork copies the selected branch into an independent persisted session. The
// source is never written to: the fork file exists before the caller switches
// to it, so a failed fork leaves the source usable. Entry IDs and parent links
// are copied verbatim, so the fork rebuilds the same context, compaction
// boundary included.
func (sm *Manager) Fork() (*Manager, error) { return sm.ForkAt("") }

// ForkAt copies the branch ending at leafID into an independent persisted
// session; an empty leafID means the current leaf. Forking at an earlier entry
// reopens that point without the messages that followed it, which is how a user
// takes a conversation in another direction.
func (sm *Manager) ForkAt(leafID string) (*Manager, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	from := sm.leafID
	if leafID != "" {
		if _, ok := sm.byIDs[leafID]; !ok {
			return nil, fmt.Errorf("session: fork point %q is not on this session", leafID)
		}
		id := leafID
		from = &id
	}
	return sm.forkLocked(from)
}

// forkLocked copies the branch ending at from (nil copies an empty session).
// Requires sm.mu to be held.
func (sm *Manager) forkLocked(from *string) (*Manager, error) {
	// Without a session file the fork would invent a relative path and drop the
	// copy into the process cwd.
	if sm.sessionFile == "" {
		return nil, errors.New("session: fork requires a persisted session")
	}
	var branch []MessageEntry
	if from != nil {
		branch = sm.getBranch(*from)
		if len(branch) == 0 {
			// A header-only fork would point its leaf at an entry it does not hold.
			return nil, fmt.Errorf("session: fork could not resolve branch at leaf %q", *from)
		}
	}

	dir := filepath.Dir(sm.sessionFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session: create fork directory: %w", err)
	}

	parentID := sm.sessionID
	var leafCopy *string
	if from != nil {
		id := *from
		leafCopy = &id
	}

	// The source's hasAssistantMsg may reflect replies the fork point leaves
	// behind (or off-branch ones), so recompute it from the copied branch.
	hasAssistant := false
	for _, entry := range branch {
		if msg, ok := entry.(SessionMessageEntry); ok && msg.Message.Role == llm.RoleAssistant {
			hasAssistant = true
			break
		}
	}

	const maxForkAttempts = 3
	var lastErr error
	for range maxForkAttempts {
		newID := generateSessionID()
		stamp := time.Now().Format("2006-01-02T15-04-05")
		header := SessionHeader{
			Type:          EntrySession,
			ID:            newID,
			Timestamp:     stamp,
			Cwd:           sm.cwd,
			ParentSession: parentID,
		}
		copied := make([]MessageEntry, 0, len(branch)+1)
		copied = append(copied, header)
		for _, entry := range slices.Backward(branch) {
			copied = append(copied, entry)
		}

		byIDs := make(map[string]MessageEntry, len(copied))
		for _, entry := range copied[1:] {
			byIDs[entry.GetID()] = entry
		}

		dst := &Manager{
			cwd: sm.cwd,
			config: ManagerConfig{
				sessionDir:  dir,
				shouldFlush: true,
				parentID:    parentID,
			},
			entries:         copied,
			byIDs:           byIDs,
			leafID:          leafCopy,
			sessionID:       newID,
			shouldFlush:     true,
			sessionFile:     filepath.Join(dir, fmt.Sprintf("%s_%s.jsonl", stamp, newID)),
			hasAssistantMsg: hasAssistant,
		}
		err := dst.writeExclusive()
		switch {
		case err == nil:
			dst.flushed = true
			return dst, nil
		case errors.Is(err, fs.ErrExist):
			lastErr = err // another process claimed this id; retry with a fresh one
		default:
			return nil, err
		}
	}
	return nil, fmt.Errorf("session: could not allocate a fork session id: %w", lastErr)
}

// writeExclusive writes the whole session to a file that must not exist yet.
// O_EXCL keeps a fork from truncating a session that already claimed its id.
func (sm *Manager) writeExclusive() error {
	f, err := os.OpenFile(sm.sessionFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("session: write fork %s: %w", sm.sessionFile, err)
	}
	if err := sm.encodeEntries(f, sm.entries); err != nil {
		_ = f.Close()
		// A truncated fork would be listed with a short preview, so drop it
		// instead of leaving a half-written session behind.
		_ = os.Remove(sm.sessionFile)
		return fmt.Errorf("session: write fork %s: %w", sm.sessionFile, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(sm.sessionFile)
		return fmt.Errorf("session: close fork %s: %w", sm.sessionFile, err)
	}
	return nil
}

// ModelName returns the latest model selection on the current branch.
func (sm *Manager) ModelName() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.modelName()
}

// modelName requires sm.mu to be held. Compaction limits message context,
// but model selection may live anywhere on the selected branch.
func (sm *Manager) modelName() string {
	for id := sm.leafID; id != nil; {
		entry, ok := sm.byIDs[*id]
		if !ok {
			break
		}
		if change, ok := entry.(ModelChangeEntry); ok {
			return change.Model
		}
		id = entry.GetParent()
	}
	return ""
}

// SetModel appends a model selection without modifying existing records.
func (sm *Manager) SetModel(name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.modelName() == name {
		return nil
	}
	entry := ModelChangeEntry{
		SessionBaseEntry: SessionBaseEntry{
			Type:      EntryModelChange,
			ID:        sm.generateID(),
			ParentID:  sm.leafID,
			Timestamp: time.Now(),
		},
		Model: name,
	}
	if err := sm.appendEntry(entry); err != nil {
		// A failed selection must remain retryable and agree with the active engine.
		sm.entries = sm.entries[:len(sm.entries)-1]
		delete(sm.byIDs, entry.ID)
		sm.leafID = entry.ParentID
		return fmt.Errorf("session: append model change %q: %w", name, err)
	}
	return nil
}

// AppendCompaction adds a compaction entry as a new leaf and returns its ID.
func (sm *Manager) AppendCompaction(compaction Compaction) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := CompactionEntry{
		SessionBaseEntry: SessionBaseEntry{
			Type:      EntryCompaction,
			ID:        sm.generateID(),
			ParentID:  sm.leafID,
			Timestamp: time.Now(),
		},
		Compaction: compaction,
	}
	if err := sm.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

func (sm *Manager) appendEntry(entry MessageEntry) error {
	leafID := entry.GetID()
	sm.leafID = &leafID
	sm.byIDs[leafID] = entry
	sm.entries = append(sm.entries, entry)

	if !sm.config.shouldFlush {
		return nil
	}

	if msgEntry, ok := entry.(SessionMessageEntry); ok && msgEntry.Message.Role == llm.RoleAssistant {
		sm.hasAssistantMsg = true
	}
	if !sm.hasAssistantMsg && !sm.flushed {
		return nil
	}
	return sm.flush(entry)
}

func (sm *Manager) flush(entry MessageEntry) error {
	if !sm.flushed {
		if err := sm.flushAllEntries(); err != nil {
			return err
		}
		sm.flushed = true
		return nil
	}
	return sm.appendFile(entry)
}

func (sm *Manager) flushAllEntries() error {
	f, err := os.Create(sm.sessionFile)
	if err != nil {
		return err
	}
	defer f.Close()
	return sm.encodeEntries(f, sm.entries)
}

func (sm *Manager) appendFile(entry MessageEntry) error {
	f, err := os.OpenFile(sm.sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return sm.encodeEntries(f, []MessageEntry{entry})
}

func (*Manager) encodeEntries(f *os.File, entries []MessageEntry) error {
	encoder := json.NewEncoder(f)
	for _, e := range entries {
		if err := encoder.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

func (sm *Manager) generateID() string {
	for range 100 {
		bytes := make([]byte, 4)
		if _, err := rand.Read(bytes); err != nil {
			panic(err)
		}
		id := hex.EncodeToString(bytes)
		if _, exists := sm.byIDs[id]; !exists {
			return id
		}
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}

// Len returns the number of entries including the session header.
func (sm *Manager) Len() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.entries)
}

func generateSessionID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}

// buildSessionContext returns the entries that form the LLM context for the
// branch ending at leafId, oldest first: a compaction entry (if any) followed
// by messages and model selections retained after it. Summarized entries
// never reach the output, so the walk stops at the retained
// tail instead of draining the whole branch.
func buildSessionContext(
	entries []MessageEntry,
	leafId string,
	byId map[string]MessageEntry,
) []MessageEntry {
	if len(byId) == 0 {
		for _, entry := range entries {
			byId[entry.GetID()] = entry
		}
	}
	if leafId == "" {
		return nil
	}

	leaf, ok := byId[leafId]
	if !ok {
		if len(entries) == 0 {
			return nil
		}
		leaf = entries[len(entries)-1]
	}

	// Walk the branch upward (newest first). Once the newest compaction entry
	// is crossed, nothing older than the entry it names in FirstKeptEntryID is
	// kept, so the walk stops there instead of draining summarized history.
	up := make([]MessageEntry, 0, 16)
	compactionIdx := -1 // newest-first index of the newest compaction entry
	keepFrom := ""      // its FirstKeptEntryID ("" = nothing kept below it)
	keptReached := false
	for current := leaf; ; {
		up = append(up, current)
		if compactionIdx < 0 && current.GetType() == EntryCompaction {
			compactionIdx = len(up) - 1
			if ce, ok := current.(CompactionEntry); ok {
				keepFrom = ce.Compaction.FirstKeptEntryID
			}
			if keepFrom == "" {
				break // summary is authoritative; nothing below stays verbatim
			}
		}
		if keepFrom != "" && current.GetID() == keepFrom {
			keptReached = true
			break
		}
		parentID := current.GetParent()
		if parentID == nil {
			break
		}
		next, ok := byId[*parentID]
		if !ok {
			break
		}
		current = next
	}

	messages := make([]MessageEntry, 0, len(up))
	if compactionIdx >= 0 {
		messages = append(messages, up[compactionIdx]) // summary first
	}
	start := len(up) - 1
	if compactionIdx >= 0 && !keptReached {
		start = compactionIdx - 1 // dangling FirstKeptEntryID: keep nothing below
	}
	for i := start; i >= 0; i-- {
		if i == compactionIdx || (up[i].GetType() != EntryMessage && up[i].GetType() != EntryModelChange) {
			continue
		}
		messages = append(messages, up[i])
	}
	return messages
}
