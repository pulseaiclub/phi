package session

import (
	"fmt"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
)

// UserTurns walks the actual branch, including messages before compaction.
func (sm *Manager) UserTurns() []SessionMessageEntry {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	var turns []SessionMessageEntry
	if sm.leafID == nil {
		return turns
	}
	for _, entry := range sm.getBranch(*sm.leafID) {
		if msg, ok := userTurn(entry); ok {
			turns = append(turns, msg)
		}
	}
	return turns
}

// RewindTo moves to the selected user message's parent. Persist before moving
// the in-memory cursor so a failed append leaves the current branch usable.
// Model selection is a setting: preserve it on the newly selected branch.
func (sm *Manager) RewindTo(entryID string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	msg, err := sm.rewindTarget(entryID)
	if err != nil {
		return "", err
	}
	base := SessionBaseEntry{
		Type:      EntryLeaf,
		ID:        sm.generateID(),
		ParentID:  msg.ParentID,
		Timestamp: time.Now(),
	}
	var marker MessageEntry = LeafEntry{base}
	leaf := msg.ParentID
	if current := sm.modelName(); current != sm.modelNameAt(msg.ParentID) {
		base.Type = EntryModelChange
		marker = ModelChangeEntry{SessionBaseEntry: base, Model: current}
		leaf = &base.ID
	}
	if sm.config.shouldFlush {
		if !sm.flushed {
			if err := sm.flushAllEntries(); err != nil {
				return "", err
			}
			sm.flushed = true
		}
		if err := sm.appendFile(marker); err != nil {
			return "", err
		}
	}
	sm.entries = append(sm.entries, marker)
	if marker.GetType() == EntryModelChange {
		sm.byIDs[base.ID] = marker
	}
	sm.leafID = leaf
	return msg.UserInput(), nil
}

// CanRewindTo validates the target without changing the active branch.
func (sm *Manager) CanRewindTo(entryID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, err := sm.rewindTarget(entryID)
	return err
}

// rewindTarget requires sm.mu; RewindTo rechecks even after a successful preflight.
func (sm *Manager) rewindTarget(entryID string) (SessionMessageEntry, error) {
	for id := sm.leafID; id != nil; {
		entry := sm.byIDs[*id]
		if entry == nil {
			break
		}
		if *id == entryID {
			if msg, ok := userTurn(entry); ok {
				return msg, nil
			}
			break
		}
		id = entry.GetParent()
	}
	return SessionMessageEntry{}, fmt.Errorf(
		"session: %q is not a user message on the current branch; select a current turn",
		entryID,
	)
}

func userTurn(entry MessageEntry) (SessionMessageEntry, bool) {
	msg, ok := entry.(SessionMessageEntry)
	return msg, ok && msg.Message.Role == llm.RoleUser && msg.Bash == nil
}
