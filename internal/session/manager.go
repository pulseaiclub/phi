package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	return sm.appendMessage(msg, nil)
}

// AppendUser records original input alongside the model-facing user message.
func (sm *Manager) AppendUser(msg llm.Message, rawInput string) (string, error) {
	if msg.Role != llm.RoleUser {
		return "", fmt.Errorf("session: AppendUser requires role %q, got %q", llm.RoleUser, msg.Role)
	}
	return sm.appendMessage(msg, &rawInput)
}

func (sm *Manager) appendMessage(msg llm.Message, rawInput *string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := SessionMessageEntry{
		SessionBaseEntry: SessionBaseEntry{
			Type:      EntryMessage,
			ID:        sm.generateID(),
			ParentID:  sm.leafID,
			Timestamp: time.Now(),
		},
		Message:  msg,
		Usage:    msg.Usage,
		RawInput: rawInput,
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

// Fork copies the selected branch into an independent persisted session.
func (sm *Manager) Fork() (*Manager, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	newID := generateSessionID()

	parentID := sm.sessionID
	header := SessionHeader{
		Type:          EntrySession,
		ID:            newID,
		Timestamp:     time.Now().Format("2006-01-02T15-04-05"),
		Cwd:           sm.cwd,
		ParentSession: parentID,
	}
	var branch []MessageEntry
	if sm.leafID != nil {
		branch = sm.getBranch(*sm.leafID)
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

	var leafCopy *string
	if sm.leafID != nil {
		id := *sm.leafID
		leafCopy = &id
	}

	config := ManagerConfig{
		sessionDir:  sm.config.sessionDir,
		shouldFlush: true,
		parentID:    parentID,
	}

	file := filepath.Join(sm.config.sessionDir,
		fmt.Sprintf("%s_%s.jsonl", time.Now().Format("2006-01-02T15-04-05"), newID))

	dst := &Manager{
		cwd:             sm.cwd,
		config:          config,
		entries:         copied,
		byIDs:           byIDs,
		leafID:          leafCopy,
		sessionID:       newID,
		shouldFlush:     true,
		sessionFile:     file,
		hasAssistantMsg: sm.hasAssistantMsg,
	}
	if err := dst.flushAllEntries(); err != nil {
		return nil, err
	}
	dst.flushed = true
	return dst, nil
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
	return sm.modelNameAt(sm.leafID)
}

func (sm *Manager) modelNameAt(leaf *string) string {
	for id := leaf; id != nil; {
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
