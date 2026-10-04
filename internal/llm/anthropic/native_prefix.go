package anthropic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

type nativeBlock map[string]json.RawMessage

// prepareNative checks what will actually go on the wire, including hook edits.
// Model identity and thinking policy are judged here against the final post-hook
// request, not the config BuildRequest saw. Hash incrementally so a long history
// is visited once, not once per signature.
func (req *AnthropicRequest) prepareNative() (string, error) {
	header := struct {
		System []sysBlock      `json:"system"`
		Tools  []anthropicTool `json:"tools"`
	}{System: slices.Clone(req.System), Tools: slices.Clone(req.Tools)}
	for i := range header.System {
		header.System[i].CacheControl = nil
	}
	for i := range header.Tools {
		header.Tools[i].CacheControl = nil
	}
	data, err := canonicalNativeJSON(header)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write(data)
	messages := make([]anthropicMessage, 0, len(req.Messages))
	for i, msg := range req.Messages {
		blocks, err := nativeBlocks(msg.Content)
		if err != nil {
			return "", fmt.Errorf("message %d: %w", i, err)
		}
		content, err := canonicalNativeBlocks(blocks)
		if err != nil {
			return "", err
		}
		if msg.Role == "assistant" && slices.ContainsFunc(blocks, isThinkingBlock) {
			// An explicitly disabled request filters thinking blocks from the wire
			// but leaves persisted history alone: a hook may express the off as
			// thinking.type "disabled" or by clearing the parameter entirely.
			// A cleared parameter alone does not mean "off" when the config
			// requested thinking: unknown endpoints may default thinking on, and
			// the thinking config is not part of the signature's prefix anyway.
			// Checked before the comparison so an off request never pays for
			// canonicalizing the persisted items it is about to discard.
			thinkingOff := (req.Thinking != nil && req.Thinking.Type == "disabled") ||
				(req.Thinking == nil && !req.thinkingRequested)
			state := req.nativeMessages[i]
			valid := false
			if !thinkingOff && state != nil &&
				state.Model == req.Model && state.Prefix == hex.EncodeToString(h.Sum(nil)) {
				original, err := nativeBlocks(state.Items)
				if err != nil {
					return "", err
				}
				expected, err := canonicalNativeBlocks(original)
				if err != nil {
					return "", err
				}
				valid = bytes.Equal(content, expected)
			}
			if !valid {
				blocks = slices.DeleteFunc(blocks, isThinkingBlock)
				if len(blocks) == 0 {
					continue
				}
				// Keep the original text/tool order and tool IDs so paired results
				// remain valid even when their thinking no longer is.
				msg.Content = blocks
				content, err = canonicalNativeBlocks(blocks)
				if err != nil {
					return "", err
				}
			}
		}
		data, err = json.Marshal(struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}{Role: msg.Role, Content: content})
		if err != nil {
			return "", err
		}
		_, _ = h.Write(data)
		messages = append(messages, msg)
	}
	req.Messages = messages
	return hex.EncodeToString(h.Sum(nil)), nil
}

func nativeBlocks(content any) ([]nativeBlock, error) {
	if text, ok := content.(string); ok {
		raw, err := json.Marshal(text)
		if err != nil {
			return nil, err
		}
		return []nativeBlock{{"type": json.RawMessage(`"text"`), "text": raw}}, nil
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	var blocks []nativeBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

func isThinkingBlock(block nativeBlock) bool {
	var kind string
	_ = json.Unmarshal(block["type"], &kind)
	return kind == "thinking" || kind == "redacted_thinking"
}

func canonicalNativeBlocks(blocks []nativeBlock) ([]byte, error) {
	clean := make([]nativeBlock, len(blocks))
	for i, block := range blocks {
		clean[i] = maps.Clone(block)
		// Only block metadata is exempt; a tool input named cache_control is
		// still part of the conversation and must affect the fingerprint.
		delete(clean[i], "cache_control")
	}
	return canonicalNativeJSON(clean)
}

func canonicalNativeJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var canonical any
	if err := decoder.Decode(&canonical); err != nil {
		return nil, err
	}
	return json.Marshal(canonical)
}
