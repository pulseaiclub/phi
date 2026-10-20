package session

import (
	"slices"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
)

// Role is the speaker of a transcript message.
type Role int

// Role values for transcript messages.
const (
	RoleUser Role = iota
	RoleAssistant
	RoleCompaction // transcript marker after context compaction ("Compacted")
	RoleLocalBash  // user-initiated "!cmd" shell run (UI-only, not agent)
)

// State is the assistant message lifecycle.
type State int

// State lifecycle values.
const (
	StateStreaming State = iota
	StateComplete
	StateCancelled
	StateError
)

func (s State) String() string {
	switch s {
	case StateStreaming:
		return "streaming"
	case StateComplete:
		return "complete"
	case StateCancelled:
		return "cancelled"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// StopReason is set when an assistant message completes.
type StopReason int

// StopReason values for completed assistant messages.
const (
	StopNone StopReason = iota
	StopEndTurn
	StopToolUse
	StopMaxTokens
)

// BlockType is an assistant content block discriminant.
type BlockType int

// BlockType values for assistant content blocks.
const (
	BlockText BlockType = iota
	BlockThinking
	BlockToolUse
)

// ContentBlock is one assistant content part.
type ContentBlock struct {
	Type BlockType

	// Text / Thinking
	Text string

	// ToolUse
	ID       string
	Name     string
	Input    string // display / JSON-ish input
	Complete bool
}

// ToolStatus is the tool run status.
type ToolStatus int

// ToolStatus values for tool runs.
const (
	ToolQueued ToolStatus = iota
	ToolInProgress
	ToolDone
	ToolError
	ToolCancelled
	ToolRejected
)

func (s ToolStatus) String() string {
	switch s {
	case ToolQueued:
		return "queued"
	case ToolInProgress:
		return "in-progress"
	case ToolDone:
		return "done"
	case ToolError:
		return "error"
	case ToolCancelled:
		return "cancelled"
	case ToolRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// ParseToolStatus maps a progress / persist string onto ToolStatus.
// Unknown values (including "") become ToolInProgress so live rows keep spinning.
func ParseToolStatus(s string) ToolStatus {
	switch s {
	case "queued":
		return ToolQueued
	case "in-progress":
		return ToolInProgress
	case "done":
		return ToolDone
	case "error":
		return ToolError
	case "cancelled":
		return ToolCancelled
	case "rejected", "rejected-by-user":
		return ToolRejected
	default:
		return ToolInProgress
	}
}

// ToolRun is the live execution state for a tool_use id.
type ToolRun struct {
	ToolUseID string
	Name      string // tool name (bash, read, ...)
	Status    ToolStatus
	Output    string
	Error     string
	Detail    string // optional one-line detail (path, cmd summary)
	ExitCode  int    // set when a local bash run finishes (Status Done/Error)
	Local     bool   // user "!cmd" bash; ignored by agent streaming/busy checks
	InContext bool   // user "!!cmd" bash whose output entered the LLM context
	Expanded  bool   // TUI starts the tool row open (user toggle still wins)
}

// Message is one session message. Assistant rows carry Content blocks and State.
type Message struct {
	ID         string
	Role       Role
	State      State      // assistant
	StopReason StopReason // assistant when complete
	Text       string     // user visible text
	Images     []llm.Image
	Content    []ContentBlock
	// Usage is token consumption for the latest assistant turn (UI + diagnostics).
	// Zero means unknown / not yet reported by the provider.
	Usage TokenUsage
	// TokensBefore is the context size before a compaction cut (RoleCompaction rows).
	TokensBefore int
}

// TokenUsage is a UI-facing copy of provider token counts for one completion.
// The buckets mirror llm.Usage: PromptTokens is the input that missed the
// cache, and the cached counts are separate rather than a subset.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int // prompt cache reads (C in the composer)
	CacheWriteTokens int // prompt cache writes (W in the composer)
	TotalTokens      int
}

// TokenUsageFrom converts provider usage into the UI-facing copy.
func TokenUsageFrom(u llm.Usage) TokenUsage {
	return TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		CachedTokens:     u.CachedTokens(),
		CacheWriteTokens: u.CacheWriteTokens(),
		TotalTokens:      u.TotalTokens,
	}
}

// Reported is true when the provider sent any non-zero token count.
func (u TokenUsage) Reported() bool {
	return u.TotalTokens > 0 || u.PromptTokens > 0 || u.CompletionTokens > 0 ||
		u.CachedTokens > 0 || u.CacheWriteTokens > 0
}

// ContextTokens is the size of the context the last completion occupied: the
// provider's total when it sent one, otherwise the sum of the disjoint buckets.
// Deliberately not PromptTokens: that is only the uncached input, so a
// cache-heavy turn would read as an almost empty window. Mirrors
// llm.Usage.ContextTokens, and TestContextTokensMatchesProviderUsage keeps the
// two from drifting apart.
func (u TokenUsage) ContextTokens() int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.PromptTokens + u.CompletionTokens + u.CachedTokens + u.CacheWriteTokens
}

// FlatText joins assistant text blocks.
func (m Message) FlatText() string {
	if m.Role == RoleUser {
		return m.Text
	}
	var text strings.Builder
	for _, blk := range m.Content {
		if blk.Type == BlockText {
			text.WriteString(blk.Text)
		}
	}
	out := text.String()
	if out == "" {
		return m.Text
	}
	return out
}

// Event is applied to session state.
type Event interface {
	isSessionEvent()
}

// UserAppend appends a user message.
type UserAppend struct {
	ID     string
	Text   string
	Images []llm.Image
}

func (UserAppend) isSessionEvent() {}

// LocalBashStart appends a user-initiated "!cmd" bash row.
type LocalBashStart struct {
	ID        string
	Command   string
	InContext bool // "!!cmd": the finished output enters the LLM context
}

func (LocalBashStart) isSessionEvent() {}

// AssistantMessageUpdate replaces the last assistant with the same turn, or
// appends if the last message is not a streaming/incomplete assistant —
// mirrors assistant message-update semantics.
type AssistantMessageUpdate struct {
	Message Message
}

func (AssistantMessageUpdate) isSessionEvent() {}

// ToolData updates a tool run by tool_use id.
type ToolData struct {
	Run ToolRun
}

func (ToolData) isSessionEvent() {}

// CancelStreaming marks the current streaming assistant as cancelled and
// cancels in-progress / queued tools.
type CancelStreaming struct{}

func (CancelStreaming) isSessionEvent() {}

// CompactionStarted signals the UI that context compaction is in progress.
type CompactionStarted struct{}

func (CompactionStarted) isSessionEvent() {}

// CompactionComplete clears the compacting activity and, when Failed is false,
// appends a compaction transcript marker. TokensBefore is the context size
// before the cut, which that marker shows.
type CompactionComplete struct {
	ID           string
	TokensBefore int
	Failed       bool
}

func (CompactionComplete) isSessionEvent() {}

// Snapshot is the full session state the TUI projects from.
type Snapshot struct {
	Messages   []Message
	Tools      map[string]ToolRun
	Compacting bool
}

// LastUsage returns the newest reported token usage in the snapshot. The UI
// uses it to restore the token readout after a resume instead of keeping the
// previous session's counts.
func (s Snapshot) LastUsage() TokenUsage {
	for _, m := range slices.Backward(s.Messages) {
		if m.Usage.Reported() {
			return m.Usage
		}
	}
	return TokenUsage{}
}
