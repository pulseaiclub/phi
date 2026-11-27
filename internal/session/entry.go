package session

import (
	"encoding/json"
	"time"

	"github.com/pulseaiclub/phi/internal/core"
)

// MessageEntry is one node in the session tree. Entries are linked by
// parent ID, enabling branching and cursor-based context building.
type MessageEntry interface {
	GetType() string
	GetID() string
	GetParent() *string
}

// EntryType is the JSON "type" discriminant of an entry.
type EntryType string

// EntryType values stored in the JSON "type" discriminant.
const (
	EntrySession     = "EntrySession"
	EntryMessage     = "EntryMessage"
	EntryCompaction  = "EntryCompaction"
	EntryModelChange = "EntryModelChange"
)

// SessionHeader is the root entry of a session file.
type SessionHeader struct {
	Type          string `json:"type"` // Always "session"
	Version       int    `json:"version,omitempty"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
}

// GetType implements MessageEntry.
func (SessionHeader) GetType() string { return EntrySession }

// GetID implements MessageEntry.
func (s SessionHeader) GetID() string { return s.ID }

// GetParent implements MessageEntry.
func (s SessionHeader) GetParent() *string {
	if s.ParentSession == "" {
		return nil
	}
	return &s.ParentSession
}

// SessionBaseEntry is the common header for non-root entries.
type SessionBaseEntry struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ParentID  *string   `json:"parentID"` // null for root entries
	Timestamp time.Time `json:"timestamp"`
}

// SessionMessageEntry wraps an LLM message as a session tree node.
// Usage lives here because message.Usage is transient completion metadata.
// Its JSON methods keep token counts stable across persist/reload.
type SessionMessageEntry struct {
	SessionBaseEntry
	Message core.Message `json:"-"`
	Usage   core.Usage   `json:"-"`
	// Bash is set for a user "!!cmd" run whose output belongs in the model
	// context. Message carries the model-facing text; Bash tells replay to draw
	// the row as a bash block instead of a user bubble.
	Bash *BashExecution `json:"bash,omitempty"`
}

// MarshalJSON preserves the historical session format through explicit wire DTOs.
func (e SessionMessageEntry) MarshalJSON() ([]byte, error) {
	type entry SessionMessageEntry
	return json.Marshal(struct {
		entry
		Message sessionMessageWire `json:"message"`
		Usage   sessionUsageWire   `json:"usage,omitempty"`
	}{entry: entry(e), Message: messageToWire(e.Message), Usage: usageToWire(e.Usage)})
}

// UnmarshalJSON restores the separately persisted message and usage counters.
func (e *SessionMessageEntry) UnmarshalJSON(data []byte) error {
	type entry SessionMessageEntry
	var decoded struct {
		entry
		Message sessionMessageWire `json:"message"`
		Usage   sessionUsageWire   `json:"usage"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*e = SessionMessageEntry(decoded.entry)
	e.Message = decoded.Message.domain()
	e.Usage = decoded.Usage.domain()
	return nil
}

// BashExecution records a user-run shell command that entered the LLM context.
type BashExecution struct {
	Command   string `json:"command"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exitCode"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

// GetType implements MessageEntry.
func (SessionMessageEntry) GetType() string { return EntryMessage }

// GetID implements MessageEntry.
func (s SessionMessageEntry) GetID() string { return s.ID }

// GetParent implements MessageEntry.
func (s SessionMessageEntry) GetParent() *string { return s.ParentID }

// ModelChangeEntry records a model selection on one conversation branch.
type ModelChangeEntry struct {
	SessionBaseEntry
	Model string `json:"model"`
}

// GetType implements MessageEntry.
func (ModelChangeEntry) GetType() string { return EntryModelChange }

// GetID implements MessageEntry.
func (e ModelChangeEntry) GetID() string { return e.ID }

// GetParent implements MessageEntry.
func (e ModelChangeEntry) GetParent() *string { return e.ParentID }

// Compaction is the data attached to a compaction entry.
type Compaction struct {
	Summary          string            `json:"summary"`
	FirstKeptEntryID string            `json:"firstKeptEntryId"`
	TokensBefore     int               `json:"tokensBefore"`
	Details          CompactionDetails `json:"details,omitempty"`
	PreserveData     map[string]any    `json:"preserveData,omitempty"`
	FromExtension    *bool             `json:"fromExtension,omitempty"`
}

// CompactionEntry is a compaction node in the session tree.
type CompactionEntry struct {
	SessionBaseEntry
	Compaction Compaction `json:"compaction"`
}

// CompactionDetails records file reads/modifications captured at compaction.
type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles,omitempty"`
	ModifiedFiles []string `json:"modifiedFiles,omitempty"`
}

// GetType implements MessageEntry.
func (CompactionEntry) GetType() string { return EntryCompaction }

// GetID implements MessageEntry.
func (c CompactionEntry) GetID() string { return c.ID }

// GetParent implements MessageEntry.
func (c CompactionEntry) GetParent() *string { return c.ParentID }
