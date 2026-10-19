package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

const (
	defaultBaseURL   = "https://api.anthropic.com/v1"
	apiVersion       = "2023-06-01"
	messagesPath     = "/messages"
	defaultMaxTokens = 4096
	// stopReasonMaxTokens marks a response that hit the output cap: the text is
	// a prefix, not a finished answer.
	stopReasonMaxTokens = "max_tokens"
)

var toolCallIDRegex = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func normalizeBaseURL(baseURL string) string {
	if baseURL == "" {
		return defaultBaseURL
	}
	normalized := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(normalized, "/v1") {
		return normalized
	}
	return normalized + "/v1"
}

// BuildRequest converts the normalized messages into the Anthropic Messages
// API shape. System text and tool results are merged so consecutive tool
// messages become one user message with tool_result blocks; prompt caching
// is pinned to the tail of the request.
func BuildRequest(
	cfg llm.ModelConfig,
	system string,
	messages []llm.Message,
	tools []llm.ToolDefinition,
) AnthropicRequest {
	cc := &cacheControl{Type: "ephemeral", TTL: "1h"}

	req := AnthropicRequest{
		Model:     cfg.Name,
		MaxTokens: defaultMaxTokens,
		Stream:    true,
	}

	if cfg.Think.Enabled {
		req.Thinking = buildThinkingConfig(cfg.Think.Mode)
		req.MaxTokens = req.Thinking.requiredMaxTokens()
	}

	var systemText strings.Builder
	if strings.TrimSpace(system) != "" {
		systemText.WriteString(system)
	}
	var msgs []llm.Message
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			if systemText.Len() > 0 {
				systemText.WriteByte('\n')
			}
			systemText.WriteString(m.Content)
			continue
		}
		msgs = append(msgs, m)
	}
	if systemText.Len() > 0 {
		req.System = []sysBlock{{
			Type:         "text",
			Text:         systemText.String(),
			CacheControl: cc,
		}}
	}
	for i, t := range tools {
		tool := anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: llm.MarshalToolParams(t.Params, "{}"),
		}
		if i == len(tools)-1 {
			tool.CacheControl = cc
		}
		req.Tools = append(req.Tools, tool)
	}
	var prefix nativePrefix
	endpoint := endpointFingerprint(cfg.BaseURL)
	toolIDs := make(map[string]string)

	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.Role {
		case llm.RoleUser:
			msg := anthropicMessage{Role: "user"}
			if len(m.Images) > 0 {
				blocks := make([]anthropicContentBlock, 0, len(m.Images)+1)
				if m.Content != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
				}
				for _, img := range m.Images {
					blocks = append(blocks, anthropicContentBlock{
						Type: "image",
						Source: &anthropicImageSource{
							Type:      "base64",
							MediaType: img.MimeType,
							Data:      img.Data,
						},
					})
				}
				msg.Content = blocks
			} else {
				msg.Content = m.Content
			}
			req.Messages = append(req.Messages, msg)

		case llm.RoleAssistant:
			msg := anthropicMessage{Role: "assistant"}
			state := m.ProviderState
			if state != nil && state.Provider == llm.Anthropic &&
				state.Model == cfg.Name && state.Endpoint == endpoint && req.Thinking != nil &&
				len(state.Data.Items) > 0 && state.Data.Prefix != "" && state.Data.Prefix == prefix.sum(&req) {
				items := make([]json.RawMessage, len(state.Data.Items))
				for j, item := range state.Data.Items {
					items[j] = bytes.Clone(item)
				}
				msg.Content = items
				for _, tc := range m.ToolCalls {
					toolIDs[tc.ID] = tc.ID
				}
			} else if len(m.ToolCalls) > 0 {
				var blocks []anthropicContentBlock
				if m.Content != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					id := normalizeToolCallID(tc.ID)
					toolIDs[tc.ID] = id
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    id,
						Name:  tc.Function.Name,
						Input: toolUseInput(tc.Function.Arguments),
					})
				}
				msg.Content = blocks
			} else if m.Content != "" {
				msg.Content = m.Content
			} else {
				continue // Legacy thinking-only messages have no replayable content.
			}
			req.Messages = append(req.Messages, msg)

		case llm.RoleTool:
			blocks := make([]anthropicContentBlock, 0, 1)
			for i < len(msgs) && msgs[i].Role == llm.RoleTool {
				tm := msgs[i]
				id, ok := toolIDs[tm.ToolCallID]
				if !ok {
					id = normalizeToolCallID(tm.ToolCallID)
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:      "tool_result",
					ToolUseID: id,
					Content:   tm.Content,
				})
				i++
			}
			i--
			req.Messages = append(req.Messages, anthropicMessage{
				Role:    "user",
				Content: blocks,
			})
		default:
			continue
		}
	}

	// Pin prompt caching to the tail of the last user message. Image blocks
	// cannot carry cache_control, so
	// the pin lands on the last non-image block (text or tool_result).
	if len(req.Messages) > 0 {
		last := &req.Messages[len(req.Messages)-1]
		if last.Role == "user" {
			if blocks, ok := last.Content.([]anthropicContentBlock); ok && len(blocks) > 0 {
				for i, b := range slices.Backward(blocks) {
					if b.Type == "image" {
						continue
					}
					blocks[i].CacheControl = cc
					break
				}
				last.Content = blocks
			} else if text, ok := last.Content.(string); ok {
				last.Content = []anthropicContentBlock{
					{Type: "text", Text: text, CacheControl: cc},
				}
			}
		}
	}

	if prefix.hash != nil {
		if sum := prefix.sum(&req); sum != "" {
			req.native = &llm.ProviderState{
				Provider: llm.Anthropic,
				Model:    cfg.Name,
				Endpoint: endpoint,
				Data:     llm.ProviderData{Prefix: sum},
			}
		}
	} else {
		req.nativeInput = snapshotNativeInput(&req)
	}
	return req
}

func normalizeToolCallID(id string) string {
	// Provider ids already sit in the allowed alphabet, so the regexp (and the
	// copy it always allocates) is only worth paying for the rare offending id.
	if len(id) <= maxToolCallIDLen && isToolCallID(id) {
		return id
	}
	normalized := toolCallIDRegex.ReplaceAllString(id, "_")
	if len(normalized) > maxToolCallIDLen {
		normalized = normalized[:maxToolCallIDLen]
	}
	return normalized
}

// maxToolCallIDLen is the Messages API limit on a tool_use id.
const maxToolCallIDLen = 64

// isToolCallID reports whether every byte of id is one the Messages API allows,
// i.e. [a-zA-Z0-9_-].
func isToolCallID(id string) bool {
	for i := 0; i < len(id); i++ {
		switch c := id[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

func toolUseInput(arguments string) json.RawMessage {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(arguments)) {
		return json.RawMessage(arguments)
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

// minThinkingBudget is the API floor for budget_tokens.
const minThinkingBudget = 1024

// buildThinkingConfig maps a ThinkMode to Anthropic's thinking parameter.
// Adaptive ("adaptive") lets the model decide the effort level;
// budget-based sets a fixed token cap per thinking level.
func buildThinkingConfig(mode llm.ThinkMode) *thinkingConfig {
	switch mode {
	case llm.Off:
		return nil
	case llm.Minimal, llm.Low:
		budget := minThinkingBudget
		return &thinkingConfig{Type: "enabled", BudgetTokens: &budget}
	case llm.Medium:
		budget := 8192
		return &thinkingConfig{Type: "enabled", BudgetTokens: &budget}
	case llm.High, llm.XHigh, llm.Max:
		budget := 16384
		return &thinkingConfig{Type: "enabled", BudgetTokens: &budget}
	default:
		return &thinkingConfig{Type: "adaptive"}
	}
}

// requiredMaxTokens returns the max_tokens that keeps an answer-sized
// allowance beside the thinking budget: thinking tokens count toward
// max_tokens and the API requires budget_tokens < max_tokens. Without a
// budget (adaptive or thinking off) the default cap applies.
func (t *thinkingConfig) requiredMaxTokens() int {
	if t == nil || t.BudgetTokens == nil {
		return defaultMaxTokens
	}
	return defaultMaxTokens + *t.BudgetTokens
}

// validateThinking re-checks the budget invariants after hooks: a hook that
// raises budget_tokens, lowers max_tokens, or drops the budget below the API
// floor would otherwise fail at the API with a less specific error. Adaptive
// thinking carries no budget and passes.
func validateThinking(req *AnthropicRequest) error {
	if req.Thinking == nil {
		return nil
	}
	budget := req.Thinking.BudgetTokens
	if budget == nil {
		if req.Thinking.Type != "enabled" || req.vendorThinking {
			return nil // adaptive carries no budget
		}
		return fmt.Errorf(
			"anthropic: thinking type enabled requires budget_tokens of at least %d",
			minThinkingBudget,
		)
	}
	if *budget < minThinkingBudget {
		return fmt.Errorf(
			"anthropic: thinking budget_tokens %d must be at least %d",
			*budget, minThinkingBudget,
		)
	}
	if *budget >= req.MaxTokens {
		return fmt.Errorf(
			"anthropic: thinking budget_tokens %d must be less than max_tokens %d",
			*budget, req.MaxTokens,
		)
	}
	return nil
}

func newMessagesHTTPRequest(ctx context.Context, cfg llm.ModelConfig, body []byte, stream bool) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		normalizeBaseURL(cfg.BaseURL)+messagesPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Api-Key", cfg.APIKey)
	httpReq.Header.Set("Anthropic-Version", apiVersion)
	if stream {
		httpReq.Header.Set("Accept", util.ContentEventStream)
	}
	return httpReq, nil
}

// Stream POSTs a streaming request to the Messages API and yields normalized
// events (same StreamEvent contract as the OpenAI-compatible path).
func Stream(
	ctx context.Context,
	httpClient *http.Client,
	cfg llm.ModelConfig,
	req *AnthropicRequest,
) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		body, err := marshalRequest(req, cfg)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		native := req.native
		if native != nil && (native.Model != cfg.Name ||
			native.Endpoint != endpointFingerprint(cfg.BaseURL)) {
			native = nil
		}
		input := req.nativeInput

		httpReq, err := newMessagesHTTPRequest(ctx, cfg, body, true)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}

		httpResp, err := util.DoWithRetry(httpClient, httpReq)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			yield(llm.StreamEvent{}, llm.FormatAPIError("anthropic", httpResp.StatusCode, respBody))
			return
		}

		processStream(httpResp.Body, func(ev llm.StreamEvent, err error) bool {
			if ev.Final != nil && ev.Final.ProviderState != nil {
				var state *llm.ProviderState
				if native != nil {
					copy := *native
					state = &copy
				} else {
					state = nativeState(input, cfg)
				}
				if state != nil {
					state.Data.Items = ev.Final.ProviderState.Data.Items
				}
				ev.Final.ProviderState = state
			}
			return yield(ev, err)
		})
	}
}

// streamEvent is one decoded Messages API SSE payload. Decoding every event
// with one struct keeps the stream loop to a single JSON pass; the sub-objects
// only the rare event types read stay raw so their shape cannot change what the
// hot content_block_delta path does.
type streamEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	Message      json.RawMessage `json:"message,omitempty"`
	Usage        json.RawMessage `json:"usage,omitempty"`
	ContentBlock json.RawMessage `json:"content_block,omitempty"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
}

// Usage updates are cumulative: absent fields keep their counts, explicit zeros replace them.
type streamUsage struct {
	InputTokens  *int `json:"input_tokens"`
	CacheRead    *int `json:"cache_read_input_tokens"`
	CacheCreate  *int `json:"cache_creation_input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

func (u streamUsage) apply(usage *llm.Usage) {
	if u.InputTokens != nil {
		usage.PromptTokens = *u.InputTokens
	}
	if u.OutputTokens != nil {
		usage.CompletionTokens = *u.OutputTokens
	}
	if u.CacheRead != nil || u.CacheCreate != nil {
		var details llm.PromptTokensDetails
		if usage.PromptTokensDetails != nil {
			details = *usage.PromptTokensDetails
		}
		if u.CacheRead != nil {
			details.CachedTokens = *u.CacheRead
		}
		if u.CacheCreate != nil {
			details.CacheWriteTokens = *u.CacheCreate
		}
		usage.PromptTokensDetails = &details
	}
}

func processStream(body io.Reader, yield func(llm.StreamEvent, error) bool) {
	var (
		content     strings.Builder
		reasoning   strings.Builder
		usage       llm.Usage
		toolCalls   []llm.ToolCall
		currentTool *llm.ToolCall
		toolArgs    strings.Builder
		native      nativeCapture
		stopped     bool
	)

stream:
	for data, parseErr := range util.ParseDataStream(body) {
		if parseErr != nil {
			yield(llm.StreamEvent{Type: llm.StreamEventTypeError, Err: parseErr.Error()}, parseErr)
			return
		}
		payloadLine := bytes.TrimSpace(data)
		if len(payloadLine) == 0 {
			continue
		}

		var ev streamEvent
		if err := json.Unmarshal(payloadLine, &ev); err != nil {
			native.discard()
			continue
		}

		switch ev.Type {
		case "message_start":
			var msg struct {
				Usage streamUsage `json:"usage"`
			}
			if len(ev.Message) > 0 {
				if err := json.Unmarshal(ev.Message, &msg); err != nil {
					continue
				}
			}
			// Anthropic reports disjoint buckets already: input_tokens excludes
			// both cache reads and cache writes, so it needs no splitting.
			msg.Usage.apply(&usage)

		case "content_block_start":
			native.start(ev.Index, ev.ContentBlock)
			var start struct {
				Type     string          `json:"type"`
				ID       string          `json:"id"`
				Name     string          `json:"name"`
				Text     string          `json:"text"`
				Thinking string          `json:"thinking"`
				Input    json.RawMessage `json:"input"`
			}
			if json.Unmarshal(ev.ContentBlock, &start) != nil {
				native.discard()
				continue
			}
			if start.Type == "tool_use" {
				currentTool = &llm.ToolCall{
					Index: ev.Index,
					ID:    start.ID,
					Type:  "function",
					Function: llm.Function{
						Name:      start.Name,
						Arguments: string(start.Input),
					},
				}
			}
			content.WriteString(start.Text)
			reasoning.WriteString(start.Thinking)
			if (start.Text != "" || start.Thinking != "") && !yield(llm.StreamEvent{
				Type:  llm.StreamEventTypeDelta,
				Delta: llm.StreamDelta{Content: start.Text, ReasoningContent: start.Thinking},
				Usage: usage,
			}, nil) {
				return
			}

		case "content_block_delta":
			delta := ev.Delta
			switch delta.Type {
			case "text_delta":
				native.delta(ev.Index, delta.Type, delta.Text)
				content.WriteString(delta.Text)
				if !yield(llm.StreamEvent{
					Type:  llm.StreamEventTypeDelta,
					Delta: llm.StreamDelta{Content: delta.Text},
					Usage: usage,
				}, nil) {
					return
				}

			case "thinking_delta":
				native.delta(ev.Index, delta.Type, delta.Thinking)
				reasoning.WriteString(delta.Thinking)
				if !yield(llm.StreamEvent{
					Type:  llm.StreamEventTypeDelta,
					Delta: llm.StreamDelta{ReasoningContent: delta.Thinking},
					Usage: usage,
				}, nil) {
					return
				}

			case "signature_delta":
				native.delta(ev.Index, delta.Type, delta.Signature)

			case "input_json_delta":
				native.delta(ev.Index, delta.Type, delta.PartialJSON)
				if currentTool == nil {
					continue
				}
				toolArgs.WriteString(delta.PartialJSON)
				currentTool.Function.Arguments = toolArgs.String()
				if !yield(llm.StreamEvent{
					Type: llm.StreamEventTypeDelta,
					Delta: llm.StreamDelta{ToolCalls: []llm.ToolCall{
						{
							Index: currentTool.Index,
							ID:    currentTool.ID,
							Type:  currentTool.Type,
							Function: llm.Function{
								Name:      currentTool.Function.Name,
								Arguments: currentTool.Function.Arguments,
							},
						},
					}},
					Usage: usage,
				}, nil) {
					return
				}
			}

		case "content_block_stop":
			native.stop(ev.Index, toolArgs.String())
			if currentTool != nil && ev.Index == currentTool.Index {
				if toolArgs.Len() > 0 {
					currentTool.Function.Arguments = toolArgs.String()
				}
				toolCalls = append(toolCalls, *currentTool)
				currentTool = nil
				toolArgs.Reset()
			}

		case "message_delta":
			var u streamUsage
			if len(ev.Usage) > 0 {
				if err := json.Unmarshal(ev.Usage, &u); err != nil {
					continue
				}
			}
			u.apply(&usage)
		case "error":
			err := fmt.Errorf("anthropic stream error: %s", llm.APIErrorMessage(payloadLine))
			yield(llm.StreamEvent{Type: llm.StreamEventTypeError, Err: err.Error()}, err)
			return
		case "message_stop":
			stopped = true
			break stream
		}
	}

	// Anthropic sends no total; the buckets are disjoint, so they add up.
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens + usage.CachedTokens() + usage.CacheWriteTokens()
	done := llm.AssistantDone(content.String(), reasoning.String(), toolCalls, usage)
	if native.hasThinking && !native.invalid && native.block == nil && stopped {
		done.Final.ProviderState = &llm.ProviderState{
			Data: llm.ProviderData{Items: native.items},
		}
	}
	yield(done, nil)
}

// NewCompactRequest leaves Claude thinking off to reserve the output cap for summary text.
func NewCompactRequest(model, prompt string, maxTokens int) *AnthropicRequest {
	// Anthropic requires max_tokens; fall back to the default cap when the
	// caller has no budget to derive one from.
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	return &AnthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}
}

// CompactRequest validates final parameters before sending a summary request.
func CompactRequest(
	ctx context.Context,
	httpClient *http.Client,
	cfg llm.ModelConfig,
	req *AnthropicRequest,
) (llm.CompactResult, error) {
	body, err := marshalRequest(req, cfg)
	if err != nil {
		return llm.CompactResult{}, err
	}

	httpReq, err := newMessagesHTTPRequest(ctx, cfg, body, false)
	if err != nil {
		return llm.CompactResult{}, err
	}

	httpResp, err := util.DoWithRetry(httpClient, httpReq)
	if err != nil {
		return llm.CompactResult{}, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return llm.CompactResult{}, err
	}
	if httpResp.StatusCode != http.StatusOK {
		return llm.CompactResult{}, llm.FormatAPIError("anthropic", httpResp.StatusCode, respBody)
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return llm.CompactResult{}, err
	}
	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	if sb.Len() == 0 {
		return llm.CompactResult{}, errors.New("anthropic API error: empty response")
	}
	return llm.CompactResult{
		Text:      sb.String(),
		Truncated: resp.StopReason == stopReasonMaxTokens,
	}, nil
}

func marshalRequest(req *AnthropicRequest, cfg llm.ModelConfig) ([]byte, error) {
	body, err := llm.MarshalRequest(req, cfg)
	if err != nil {
		return nil, err
	}
	// Reparse only overridden thinking; max_tokens is protected and conversations can be large.
	thinking := req.Thinking
	if _, ok := cfg.RequestParameters["thinking"]; ok {
		var final struct {
			Thinking *thinkingConfig `json:"thinking"`
		}
		if err := json.Unmarshal(body, &final); err != nil {
			return nil, fmt.Errorf("anthropic request parameters: %w", err)
		}
		thinking = final.Thinking
	}
	if err := validateThinking(&AnthropicRequest{
		MaxTokens: req.MaxTokens, Thinking: thinking, vendorThinking: req.vendorThinking,
	}); err != nil {
		return nil, err
	}
	return body, nil
}
