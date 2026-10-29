package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
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
	parent, _ := sm.entries[0].(SessionHeader)
	header := SessionHeader{
		Type:          EntrySession,
		ID:            newID,
		Timestamp:     time.Now().Format("2006-01-02T15-04-05"),
		Cwd:           sm.cwd,
		ParentSession: parentID,
		// A fork is still the same conversation to the user, so it keeps the
		// name they gave it.
		Title: parent.Title,
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

// Title returns the user-facing session name, empty when never renamed.
func (sm *Manager) Title() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	header, _ := sm.entries[0].(SessionHeader)
	return header.Title
}

// SetTitle renames the session. The header is line 1 of the JSONL, so a
// flushed session is rewritten once through rewriteTitle; before the first
// flush there is no file yet and the title simply lands with it. An empty
// title clears the name.
func (sm *Manager) SetTitle(title string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	header, ok := sm.entries[0].(SessionHeader)
	if !ok {
		return errors.New("session: set title: missing header")
	}
	title = NormalizeTitle(title)
	if header.Title == title {
		return nil
	}
	// Persist before mutating memory: a failed rewrite must leave the live
	// manager describing the file that is still on disk.
	if sm.flushed {
		if err := rewriteTitle(sm.sessionFile, title); err != nil {
			return err
		}
	}
	header.Title = title
	sm.entries[0] = header
	return nil
}

// MaxTitleRunes caps a session title; longer names only crowd the picker row.
const MaxTitleRunes = 80

// NormalizeTitle reduces a typed title to one clean line: control characters
// become spaces, runs of whitespace collapse, and the result is capped. An
// empty title means "unnamed", which omits the field from the JSONL header.
// Callers that report a rename back to the user normalize first, so the toast
// matches what the file stores.
func NormalizeTitle(title string) string {
	title = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	return util.TruncateRunes(title, MaxTitleRunes, "…")
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
	if !sm.hasAssistantMsg {
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
	return withSessionLock(sm.sessionFile, func() error {
		f, err := os.Create(sm.sessionFile)
		if err != nil {
			return err
		}
		defer f.Close()
		return sm.encodeEntries(f, sm.entries)
	})
}

func (sm *Manager) appendFile(entry MessageEntry) error {
	return withSessionLock(sm.sessionFile, func() error {
		f, err := os.OpenFile(sm.sessionFile, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		return sm.encodeEntries(f, []MessageEntry{entry})
	})
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
// by the messages retained after it. Everything summarized by the newest
// compaction never reaches the output, so the walk stops at the retained
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
		if i == compactionIdx || up[i].GetType() != EntryMessage {
			continue
		}
		messages = append(messages, up[i])
	}
	return messages
}
