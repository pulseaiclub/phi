package controller

import "github.com/pulseaiclub/phi/internal/llm"

// AskReply is the user's response for a gated tool confirmation.
type AskReply struct {
	Approved        bool
	Feedback        string
	AllowSession    bool // Allow All for This Session
	AllowPersistent bool // Allow All for Every Session
}

// ExtConfirmReply is the user's response for an extension Confirm dialog.
type ExtConfirmReply struct {
	OK bool
}

// ContinueReply is the user's response when the tool-round budget is exhausted.
type ContinueReply struct {
	Continue bool
}

// ErrorReply is the user's response to a loop-error dialog.
type ErrorReply struct {
	Retry bool // re-run the last prompt
	Copy  bool // copy the error text to the clipboard
}

// LastPrompt is the most recent prompt submission, retained so an error
// dialog can offer Retry.
type LastPrompt struct {
	Text   string
	Skills []string
	Images []llm.Image
}
