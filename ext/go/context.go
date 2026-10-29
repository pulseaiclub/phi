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

// UI is the interactive surface available to extensions.
// Headless/run mode may provide a no-op or deny-by-default Confirm.
type UI interface {
	Notify(message, kind string) // kind: info | warning | error
	Confirm(title, message string) bool
	ConfirmOpts(ConfirmRequest) ConfirmReply
	SetStatus(key, text string) // empty text clears
}
