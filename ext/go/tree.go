package ext

const (
	EventSessionBeforeTree = "session_before_tree"
	EventSessionTree       = "session_tree"
)

type SessionBeforeTreeEvent struct {
	FromID       string
	TargetID     string
	SelectedID   string
	Summarize    bool
	Instructions string
}

type SessionBeforeTreeResult struct {
	Cancel       bool
	Reason       string
	Summary      string
	Instructions string
}

type SessionTreeEvent struct {
	FromID    string
	TargetID  string
	Summary   string
	SummaryID string
}
