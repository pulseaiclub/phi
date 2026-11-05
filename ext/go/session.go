package ext

import "time"

// SelectItem is one option in a host picker.
type SelectItem struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// SelectRequest describes an interactive picker.
type SelectRequest struct {
	Title string       `json:"title"`
	Items []SelectItem `json:"items"`
}

// SelectReply contains the original item index, or OK=false on cancellation.
type SelectReply struct {
	Index int  `json:"index"`
	OK    bool `json:"ok"`
}

// SessionTurn identifies a user message on the active branch, newest first.
type SessionTurn struct {
	ImageCount int       `json:"image_count,omitempty"`
	EntryID    string    `json:"entry_id"`
	Text       string    `json:"text"`
	Timestamp  time.Time `json:"timestamp"`
}

// RewindRequest moves the conversation to immediately before EntryID.
// ValidateOnly checks eligibility without changing the conversation.
type RewindRequest struct {
	SessionID    string `json:"session_id"`
	EntryID      string `json:"entry_id"`
	ValidateOnly bool   `json:"validate_only,omitempty"`
}
