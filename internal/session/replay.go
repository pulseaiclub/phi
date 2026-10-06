package session

import "github.com/pulseaiclub/phi/internal/llm"

// ToolDetail resolves a friendly one-line display detail for a tool call's raw
// JSON arguments, mirroring the live executor's DetailFromArgs. It returns ""
// when no friendly detail is available, in which case replay falls back to the
// raw arguments. nil means always use the raw arguments.
type ToolDetail func(toolName, args string) string

// ReplaySnapshot rebuilds a UI transcript snapshot from context path entries
// (user/assistant text, thinking, tool runs, compaction markers) so /resume
// and /new render the same styled rows as the live session without
// re-streaming. detail resolves tool-call arguments into the same friendly
// one-line detail the live turn shows (e.g. "read foo.go:10-20" instead of raw
// JSON); pass nil to keep raw arguments.
func ReplaySnapshot(entries []MessageEntry, detail ToolDetail) Snapshot {
	var snap Snapshot
	for _, entry := range entries {
		switch entry.GetType() {
		case EntryCompaction:
			// TokensBefore is persisted on the entry; carry it through so a resumed
			// session's marker still shows the pre-cut context size.
			comp := entry.(CompactionEntry).Compaction
			snap = Apply(snap, CompactionComplete{ID: entry.GetID(), TokensBefore: comp.TokensBefore})
		case EntryMessage:
			snap = replayEntry(snap, entry.(SessionMessageEntry), detail)
		}
	}
	return snap
}

func replayEntry(snap Snapshot, entry SessionMessageEntry, detail ToolDetail) Snapshot {
	msg := entry.Message
	switch msg.Role {
	case llm.RoleUser:
		images := make([]llm.Image, 0, len(msg.Images))
		return Apply(snap, UserAppend{
			ID:     entry.GetID(),
			Text:   msg.Content,
			Images: append(images, msg.Images...),
		})
	case llm.RoleAssistant:
		return Apply(snap, AssistantMessageUpdate{Message: replayAssistant(entry.GetID(), entry, detail)})
	case llm.RoleTool:
		// The persisted result is the model-facing content; Name/Detail of the
		// run are carried over from the tool_use block by Apply's merge.
		return Apply(snap, ToolData{Run: ToolRun{
			ToolUseID: msg.ToolCallID,
			Status:    ToolDone,
			Output:    msg.Content,
		}})
	}
	return snap
}

// replayAssistant converts a persisted assistant llm.Message into a session
// Message. Usage comes from the entry because it is persisted separately.
func replayAssistant(id string, entry SessionMessageEntry, detail ToolDetail) Message {
	msg := entry.Message
	reason := StopNone
	if len(msg.ToolCalls) > 0 {
		reason = StopToolUse
	}
	return ProjectAssistant(id, msg, detail, StateComplete, reason, TokenUsageFrom(entry.Usage))
}

// ProjectAssistant converts a complete model message into the transcript shape.
// Keeping this projection here makes live rendering and replay use the same
// content block and tool argument rules.
func ProjectAssistant(
	id string,
	msg llm.Message,
	detail ToolDetail,
	state State,
	reason StopReason,
	usage TokenUsage,
) Message {
	blocks := make([]ContentBlock, 0, 2+len(msg.ToolCalls))
	if msg.ReasoningContent != "" {
		blocks = append(blocks, ContentBlock{Type: BlockThinking, Text: msg.ReasoningContent})
	}
	if msg.Content != "" {
		blocks = append(blocks, ContentBlock{Type: BlockText, Text: msg.Content})
	}
	for _, call := range msg.ToolCalls {
		blocks = append(blocks, ContentBlock{
			Type: BlockToolUse, ID: call.ID, Name: call.Function.Name,
			Input: replayToolInput(call, detail), Complete: true,
		})
	}
	return Message{ID: id, State: state, StopReason: reason, Text: msg.Content, Content: blocks, Usage: usage}
}

// replayToolInput prefers the friendly detail (matching the live turn) and
// falls back to the raw JSON arguments.
func replayToolInput(call llm.ToolCall, detail ToolDetail) string {
	if detail != nil {
		if d := detail(call.Function.Name, call.Function.Arguments); d != "" {
			return d
		}
	}
	return call.Function.Arguments
}
