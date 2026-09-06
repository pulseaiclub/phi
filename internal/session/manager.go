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
// root, oldest first, with compaction applied (compaction entry plus messages
// kept after it).
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

// Fork creates a new Manager that shares the same session entries
// but starts from the current leaf, allowing independent conversation branches.
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
	copied := make([]MessageEntry, 0, len(sm.entries))
	copied = append(copied, header)
	copied = append(copied, sm.entries[1:]...)

	byIDs := make(map[string]MessageEntry, len(copied))
	for _, entry := range copied {
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

func (sm *Manager) appendEntry(entry MessageEntry) (err error) {
	previous, hadAssistant := sm.leafID, sm.hasAssistantMsg
	defer func() {
		if err != nil {
			sm.leafID, sm.hasAssistantMsg = previous, hadAssistant
			delete(sm.byIDs, entry.GetID())
			sm.entries = sm.entries[:len(sm.entries)-1]
		}
	}()
	leafID := entry.GetID()
	if entry.GetType() != EntryLabel {
		sm.leafID = &leafID
	}
	sm.byIDs[leafID] = entry
	sm.entries = append(sm.entries, entry)

	if !sm.config.shouldFlush {
		return nil
	}

	if msgEntry, ok := entry.(SessionMessageEntry); ok && msgEntry.Message.Role == llm.RoleAssistant {
		sm.hasAssistantMsg = true
	}
	if !sm.hasAssistantMsg && !sm.flushed && entry.GetType() == EntryMessage {
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
	f, err := os.CreateTemp(filepath.Dir(sm.sessionFile), ".session-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer f.Close()
	if err := sm.encodeEntries(f, sm.entries); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), sm.sessionFile)
}

func (sm *Manager) appendFile(entry MessageEntry) error {
	f, err := os.OpenFile(sm.sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := sm.encodeEntries(f, []MessageEntry{entry}); err != nil {
		_ = f.Truncate(info.Size())
		return err
	}
	return nil
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

// buildSessionContext walks from the leaf back to the root and returns the
// messages that form the LLM context: a compaction entry (if any) followed by
// the messages kept after it.
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
		leaf = entries[len(entries)-1]
	}

	path := make([]MessageEntry, 0, len(entries))
	current := leaf
	for current != nil {
		path = append(path, current)
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
	slices.Reverse(path)

	var (
		messages      []MessageEntry
		compactionIdx = -1
	)
	for i, m := range path {
		if m.GetType() == EntryCompaction {
			compactionIdx = i
		}
	}

	appendMessage := func(entry MessageEntry) {
		if entry.GetType() == EntryMessage || entry.GetType() == EntryBranchSummary || entry.GetType() == EntryHistory {
			messages = append(messages, entry)
		}
	}

	if compactionIdx >= 0 {
		compaction := path[compactionIdx]
		messages = append(messages, compaction)

		firstKeptIdx := compactionIdx
		if ce, ok := compaction.(CompactionEntry); ok && ce.Compaction.FirstKeptEntryID != "" {
			for i := compactionIdx; i >= 0; i-- {
				if path[i].GetID() == ce.Compaction.FirstKeptEntryID {
					firstKeptIdx = i
					break
				}
			}
		}

		for i := firstKeptIdx; i < len(path); i++ {
			appendMessage(path[i])
		}
	} else {
		for _, entry := range path {
			appendMessage(entry)
		}
	}
	return messages
}
