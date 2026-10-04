package anthropic

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
)

const nativeStateVersion = 2

// blockStart is the typed view of a content_block_start payload, decoded once
// and shared by the stream parser and nativeCapture.
type blockStart struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Input     json.RawMessage `json:"input"`
}

func endpointFingerprint(baseURL string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(normalizeBaseURL(baseURL))))
}

// replayNative keeps the candidate items for any state from the same source:
// version, API, and endpoint must match. Model and thinking policy can only be
// judged against the final post-hook request, so prepareNative decides those.
func replayNative(endpoint string, state *llm.NativeState) []json.RawMessage {
	if state == nil || state.Version != nativeStateVersion || state.API != llm.Anthropic ||
		state.Endpoint != endpoint {
		return nil
	}
	// Hooks may modify request bodies; never lend them the session's byte slices.
	items := make([]json.RawMessage, len(state.Items))
	for i, item := range state.Items {
		items[i] = bytes.Clone(item)
	}
	return items
}

// Keep the full content array only for messages with thinking. Rebuilding it
// from normalized text/tool calls would lose block boundaries and ordering.
type nativeCapture struct {
	items        []json.RawMessage
	block        map[string]json.RawMessage
	index        int
	kind         string
	text         strings.Builder
	signature    strings.Builder
	hasThinking  bool
	invalid      bool
	unsigned     bool
	unreplayable bool
}

func (c *nativeCapture) start(index int, raw json.RawMessage, typed blockStart) {
	if c.block != nil || index != c.index {
		c.invalid = true
	}
	c.index = index
	c.text.Reset()
	c.signature.Reset()
	c.block = nil
	if typed.Type == "" {
		c.invalid = true
		return
	}
	if json.Unmarshal(raw, &c.block) != nil || c.block == nil {
		c.invalid = true
		return
	}
	c.kind = typed.Type
	switch c.kind {
	case "thinking":
		c.hasThinking = true
		c.text.WriteString(typed.Thinking)
		c.signature.WriteString(typed.Signature)
	case "redacted_thinking":
		c.hasThinking = true
	case "text":
		c.text.WriteString(typed.Text)
	case "tool_use":
	default:
		c.invalid = true
	}
}

func (c *nativeCapture) delta(index int, kind, value string) {
	if c.block == nil || c.index != index {
		c.invalid = true
		return
	}
	switch {
	case kind == "text_delta" && c.kind == "text", kind == "thinking_delta" && c.kind == "thinking":
		c.text.WriteString(value)
	case kind == "signature_delta" && c.kind == "thinking":
		c.signature.WriteString(value)
	case kind == "input_json_delta" && c.kind == "tool_use":
		// Arguments accumulate in processStream's toolArgs; nothing to track here.
	default:
		c.invalid = true
	}
}

func (c *nativeCapture) stop(index int, args string) {
	if c.block == nil || c.index != index {
		c.invalid = true
		return
	}
	switch c.kind {
	case "thinking":
		c.block["thinking"], _ = json.Marshal(c.text.String())
		c.block["signature"], _ = json.Marshal(c.signature.String())
		c.unsigned = c.unsigned || c.signature.Len() == 0
	case "redacted_thinking":
		var data string
		_ = json.Unmarshal(c.block["data"], &data)
		c.unsigned = c.unsigned || data == ""
	case "text":
		c.block["text"], _ = json.Marshal(c.text.String())
	case "tool_use":
		if args != "" {
			c.block["input"] = json.RawMessage(args)
		}
	}
	item, err := json.Marshal(c.block)
	c.block = nil
	c.index++
	if err != nil {
		// Truncated tool arguments belong to the executor's fallback, not thinking integrity.
		c.unreplayable = true
		return
	}
	c.items = append(c.items, item)
}

func (c *nativeCapture) state(model, endpoint string) *llm.NativeState {
	if !c.hasThinking || c.invalid || c.unsigned || c.unreplayable || c.block != nil {
		return nil
	}
	return &llm.NativeState{
		Version: nativeStateVersion, API: llm.Anthropic, Model: model,
		Endpoint: endpoint, Items: c.items,
	}
}
