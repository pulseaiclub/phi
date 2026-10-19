package anthropic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pulseaiclub/phi/internal/llm"
)

type nativeBlock map[string]json.RawMessage

// Capture whole blocks: flattening text and tool calls loses signed block order.
type nativeCapture struct {
	items       []json.RawMessage
	block       nativeBlock
	kind        string
	text        strings.Builder
	signature   strings.Builder
	hasThinking bool
	invalid     bool
}

func (c *nativeCapture) start(index int, raw json.RawMessage) {
	if c.invalid {
		return
	}
	if c.block != nil || index != len(c.items) {
		c.discard()
		return
	}
	c.text.Reset()
	c.signature.Reset()
	if json.Unmarshal(raw, &c.block) != nil || c.block == nil {
		c.discard()
		return
	}
	c.kind = ""
	_ = json.Unmarshal(c.block["type"], &c.kind)
	if c.kind == "" {
		c.discard()
		return
	}
	c.hasThinking = c.hasThinking || c.kind == "thinking" || c.kind == "redacted_thinking"
	key := "text"
	if c.kind == "thinking" {
		key = "thinking"
	}
	var text, signature string
	_ = json.Unmarshal(c.block[key], &text)
	_ = json.Unmarshal(c.block["signature"], &signature)
	c.text.WriteString(text)
	c.signature.WriteString(signature)
}

func (c *nativeCapture) delta(index int, kind, value string) {
	if c.invalid {
		return
	}
	if c.block == nil || index != len(c.items) {
		c.discard()
		return
	}
	switch {
	case kind == "text_delta" && c.kind == "text", kind == "thinking_delta" && c.kind == "thinking":
		c.text.WriteString(value)
	case kind == "signature_delta" && c.kind == "thinking":
		c.signature.WriteString(value)
	case kind == "input_json_delta" && c.kind == "tool_use":
	default:
		c.discard()
	}
}

func (c *nativeCapture) stop(index int, args string) {
	if c.invalid {
		return
	}
	if c.block == nil || index != len(c.items) {
		c.discard()
		return
	}
	switch c.kind {
	case "thinking":
		if c.signature.Len() == 0 {
			c.discard()
			return
		}
		c.block["thinking"], _ = json.Marshal(c.text.String())
		c.block["signature"], _ = json.Marshal(c.signature.String())
	case "redacted_thinking":
		var data string
		_ = json.Unmarshal(c.block["data"], &data)
		if data == "" {
			c.discard()
			return
		}
	case "text":
		c.block["text"], _ = json.Marshal(c.text.String())
	case "tool_use":
		if args != "" {
			c.block["input"] = json.RawMessage(args)
		}
	}
	item, err := json.Marshal(c.block)
	if err != nil {
		c.discard()
		return
	}
	c.items = append(c.items, item)
	c.block = nil
}

func (c *nativeCapture) discard() {
	// No later block can repair an incomplete signed sequence.
	*c = nativeCapture{invalid: true}
}

type nativePrefix struct {
	hash    hash.Hash
	count   int
	invalid bool
}

// Advance only when a saved state needs checking; unsigned conversations skip hashing.
func (p *nativePrefix) sum(req *AnthropicRequest) string {
	if p.hash == nil {
		p.hash = sha256.New()
		p.invalid = !writePrefix(p.hash, struct {
			System []sysBlock      `json:"system"`
			Tools  []anthropicTool `json:"tools"`
		}{req.System, req.Tools})
	}
	for !p.invalid && p.count < len(req.Messages) {
		p.invalid = !writePrefix(p.hash, req.Messages[p.count])
		p.count++
	}
	if p.invalid {
		return ""
	}
	return hex.EncodeToString(p.hash.Sum(nil))
}

// Replay is selected before hooks, so retain that same input for deferred hashing.
// Strings are immutable; only the mutable slices and image sources need copies.
func snapshotNativeInput(req *AnthropicRequest) *AnthropicRequest {
	input := &AnthropicRequest{
		System:   slices.Clone(req.System),
		Tools:    slices.Clone(req.Tools),
		Messages: slices.Clone(req.Messages),
	}
	for i := range input.Tools {
		input.Tools[i].InputSchema = bytes.Clone(input.Tools[i].InputSchema)
	}
	for i, msg := range input.Messages {
		if content, ok := msg.Content.([]anthropicContentBlock); ok {
			blocks := slices.Clone(content)
			for j := range blocks {
				blocks[j].Input = bytes.Clone(blocks[j].Input)
				if blocks[j].Source != nil {
					source := *blocks[j].Source
					blocks[j].Source = &source
				}
			}
			input.Messages[i].Content = blocks
		}
	}
	return input
}

func nativeState(input *AnthropicRequest, cfg llm.ModelConfig) *llm.ProviderState {
	if input == nil {
		return nil
	}
	var prefix nativePrefix
	sum := prefix.sum(input)
	if sum == "" {
		return nil
	}
	return &llm.ProviderState{
		Provider: llm.Anthropic,
		// Replay is selected before hooks, using the configured model identity.
		Model:    cfg.Name,
		Endpoint: endpointFingerprint(cfg.BaseURL),
		Data:     llm.ProviderData{Prefix: sum},
	}
}

func endpointFingerprint(baseURL string) string {
	// Session files should not expose credentials embedded in endpoint URLs.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(normalizeBaseURL(baseURL))))
}

// Hash the wire prefix incrementally. Strings and text arrays are equivalent,
// object key order is irrelevant, and cache markers are not signature inputs.
func writePrefix(h hash.Hash, value any) bool {
	if msg, ok := value.(anthropicMessage); ok {
		if text, ok := msg.Content.(string); ok && utf8.ValidString(text) {
			// Invalid UTF-8 needs the JSON round trip to preserve saved fingerprints.
			return writePrefixObject(h, map[string]any{"role": msg.Role, "content": msg.Content})
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if decoder.Decode(&result) != nil {
		return false
	}
	return writePrefixObject(h, result)
}

func writePrefixObject(h hash.Hash, result map[string]any) bool {
	if text, ok := result["content"].(string); ok {
		block := map[string]any{"type": "text"}
		if text != "" {
			block["text"] = text
		}
		result["content"] = []any{block}
	}
	for _, key := range []string{"system", "tools", "content"} {
		if blocks, ok := result[key].([]any); ok {
			for _, block := range blocks {
				if fields, ok := block.(map[string]any); ok {
					delete(fields, "cache_control")
				}
			}
		}
	}
	return json.NewEncoder(h).Encode(result) == nil
}
