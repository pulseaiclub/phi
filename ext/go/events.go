package ext

// Lifecycle event names for coding-agent extensions.
//
// Full chain (PXB-subprocess shaped):
//
//	user_input → before_agent_start → agent_start
//	  → turn_start → [LLM] → tool_execution_start → tool_call → Gate → Run
//	    → tool_result → tool_execution_end → turn_end
//	  → turn_stopping (natural stop) → agent_end
//	session_*: before_switch / start / shutdown / compact
const (
	EventToolCall            = "tool_call"
	EventToolResult          = "tool_result"
	EventToolExecutionStart  = "tool_execution_start"
	EventToolExecutionEnd    = "tool_execution_end"
	EventSessionStart        = "session_start"
	EventSessionShutdown     = "session_shutdown"
	EventSessionBeforeSwitch = "session_before_switch"
	EventBeforeAgentStart    = "before_agent_start"
	EventAgentStart          = "agent_start"
	EventAgentEnd            = "agent_end"
	EventTurnStart           = "turn_start"
	EventTurnEnd             = "turn_end"
	EventUserInput           = "user_input"
	EventTurnStopping        = "turn_stopping"
	EventSessionCompact      = "session_compact"
)
