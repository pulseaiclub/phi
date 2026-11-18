package controller

import "github.com/pulseaiclub/phi/internal/llm"

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
