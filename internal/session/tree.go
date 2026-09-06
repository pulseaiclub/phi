package session

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
)

const (
	EntryBranchSummary = "EntryBranchSummary"
	EntryLabel         = "EntryLabel"
	EntryHistory       = "EntryHistory"
)

// BranchSummary carries context from an abandoned branch without truncating the destination.
type BranchSummary struct {
	Summary       string    `json:"summary"`
	FromID        string    `json:"fromId"`
	Usage         llm.Usage `json:"usage,omitempty"`
	ReadFiles     []string  `json:"readFiles,omitempty"`
	ModifiedFiles []string  `json:"modifiedFiles,omitempty"`
}

type BranchSummaryEntry struct {
	SessionBaseEntry
	BranchSummary
}

// Labels are append-only bookkeeping, not conversation ancestors.
type LabelEntry struct {
	SessionBaseEntry
	TargetID string `json:"targetId"`
	Label    string `json:"label"`
}

// HistoryEntry preserves UI-only activity; custom messages also enter model context.
type HistoryEntry struct {
	SessionBaseEntry
	Kind string  `json:"kind"` // bash | model | error | cancelled | custom
	Text string  `json:"text"`
	Run  ToolRun `json:"run,omitempty"`
}

func (b SessionBaseEntry) GetID() string      { return b.ID }
func (b SessionBaseEntry) GetParent() *string { return b.ParentID }
func (b SessionBaseEntry) GetType() string    { return b.Type }

type TreeSnapshot struct {
	Entries []MessageEntry
	LeafID  string
	Labels  map[string]LabelEntry
}

// TreeSnapshot includes compacted history and inactive branches. Entries are immutable.
func (sm *Manager) TreeSnapshot() TreeSnapshot {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	snap := TreeSnapshot{Entries: slices.Clone(sm.entries[1:]), Labels: make(map[string]LabelEntry)}
	if sm.leafID != nil {
		snap.LeafID = *sm.leafID
	}
	for _, entry := range snap.Entries {
		if label, ok := entry.(LabelEntry); ok {
			snap.Labels[label.TargetID] = label
		}
	}
	return snap
}

type TreeNavigation struct {
	FromID     string
	TargetID   string
	SelectedID string
	EditorText string
	Noop       bool
	Departing  []MessageEntry // oldest first, excluding the common ancestor
}

func (s TreeSnapshot) Plan(selected string) (TreeNavigation, error) {
	p := TreeNavigation{FromID: s.LeafID, SelectedID: selected, TargetID: selected}
	byID := make(map[string]MessageEntry, len(s.Entries))
	for _, entry := range s.Entries {
		byID[entry.GetID()] = entry
	}
	if selected == s.LeafID {
		p.Noop = true
		return p, nil
	}
	if selected != "" {
		entry := byID[selected]
		if entry == nil || entry.GetType() == EntryLabel {
			return p, fmt.Errorf("session: tree node %q not found", selected)
		}
		switch e := entry.(type) {
		case SessionMessageEntry:
			if e.Message.Role == llm.RoleUser {
				p.EditorText = e.Message.Content
				p.TargetID = parentID(entry)
			}
		case HistoryEntry:
			if e.Kind == "custom" {
				p.EditorText = e.Text
				p.TargetID = parentID(entry)
			}
		}
	}
	ancestors := map[string]bool{"": true}
	for id := p.TargetID; id != "" && !ancestors[id]; id = parentID(byID[id]) {
		ancestors[id] = true
	}
	seen := make(map[string]bool)
	for id := p.FromID; id != "" && !ancestors[id] && !seen[id]; id = parentID(byID[id]) {
		seen[id] = true
		if e := byID[id]; e != nil {
			p.Departing = append(p.Departing, e)
		}
	}
	slices.Reverse(p.Departing)
	return p, nil
}

func parentID(entry MessageEntry) string {
	if entry == nil || entry.GetParent() == nil {
		return ""
	}
	return *entry.GetParent()
}

// Navigate commits only after preparation succeeds and the source cursor still matches.
// Like Pi, navigation without a summary is memory-only until another entry is appended.
func (sm *Manager) Navigate(from, target string, summary *BranchSummary) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	current := ""
	if sm.leafID != nil {
		current = *sm.leafID
	}
	if current != from {
		return errors.New("session changed during tree navigation; reopen /tree")
	}
	if target != "" {
		e := sm.byIDs[target]
		if e == nil || e.GetType() == EntryLabel {
			return fmt.Errorf("session: tree node %q not found", target)
		}
	}
	previous := sm.leafID
	sm.leafID = nil
	if target != "" {
		sm.leafID = &target
	}
	if summary != nil {
		entry := BranchSummaryEntry{SessionBaseEntry: sm.baseEntry(EntryBranchSummary), BranchSummary: *summary}
		if err := sm.appendEntry(entry); err != nil {
			sm.leafID = previous
			return err
		}
	}
	return nil
}

func (sm *Manager) SetLabel(target, text string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	e := sm.byIDs[target]
	if e == nil || e.GetType() == EntryLabel {
		return fmt.Errorf("session: cannot label unknown node %q", target)
	}
	return sm.appendEntry(
		LabelEntry{SessionBaseEntry: sm.baseEntry(EntryLabel), TargetID: target, Label: strings.TrimSpace(text)},
	)
}

func (sm *Manager) AppendHistory(history HistoryEntry) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	history.SessionBaseEntry = sm.baseEntry(EntryHistory)
	if err := sm.appendEntry(history); err != nil {
		return "", err
	}
	return history.ID, nil
}

func (sm *Manager) baseEntry(kind string) SessionBaseEntry {
	return SessionBaseEntry{Type: kind, ID: sm.generateID(), ParentID: sm.leafID, Timestamp: time.Now()}
}
