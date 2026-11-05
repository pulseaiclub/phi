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
		if msg, ok := entry.(SessionMessageEntry); ok && msg.Message.Role == llm.RoleUser && msg.Bash == nil {
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
	if sm.leafID != nil {
		for _, entry := range sm.getBranch(*sm.leafID) {
			if entry.GetID() != entryID {
				continue
			}
			msg, ok := entry.(SessionMessageEntry)
			if !ok || msg.Message.Role != llm.RoleUser || msg.Bash != nil {
				break
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
	}
	return "", fmt.Errorf("session: %q is not a user message on the current branch; select a current turn", entryID)
}
