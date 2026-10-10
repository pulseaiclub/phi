package controller

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

// ExtPickerReply is the user's response for an extension list picker.
// OK is false when the picker went down without a choice.
type ExtPickerReply struct {
	OK bool
	ID string
}

// ContinueReply is the user's response when the tool-round budget is exhausted.
type ContinueReply struct {
	Continue bool
}
