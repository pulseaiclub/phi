package ext

// Context is passed to event handlers and command handlers.
type Context struct {
	Cwd       string
	SessionID string
	HasUI     bool
	UI        UI
}

// ConfirmRequest describes a modal yes/no dialog.
type ConfirmRequest struct {
	Title   string
	Message string
	Yes     string // default "Yes"
	No      string // default "No"
	Danger  bool   // style Yes as destructive
}

// ConfirmReply is the user's choice.
type ConfirmReply struct {
	OK bool
}

// PickerRequest describes a filterable list the user picks one row from.
type PickerRequest struct {
	Title string
	Items []PickerItem
}

// PickerItem is one picker row. Label is the bold identity column — a branch,
// a path, a model — so keep it short; Detail carries the rest.
type PickerItem struct {
	ID     string // returned in PickerReply
	Label  string // primary column
	Detail string // remaining text
	Badge  string // optional tag after the label (e.g. "current")
}

// PickerReply is the chosen row. OK is false when the user dismissed the
// picker (or the host has no UI), in which case ID is empty.
type PickerReply struct {
	OK bool
	ID string
}

// UI is the interactive surface available to extensions.
// Headless/run mode may provide a no-op or deny-by-default Confirm, and a
// ShowPicker that reports every request as dismissed.
type UI interface {
	Notify(message, kind string) // kind: info | warning | error
	Confirm(title, message string) bool
	ConfirmOpts(ConfirmRequest) ConfirmReply
	// ShowPicker opens the host list overlay (the one /branch and /sessions
	// use) and blocks until the user picks a row or dismisses it.
	ShowPicker(PickerRequest) PickerReply
	SetStatus(key, text string) // empty text clears
}
