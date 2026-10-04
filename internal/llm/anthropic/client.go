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
		// Mode Off yields a nil config; Enabled+Off is still an explicit off
		// and must filter thinking history like Enabled=false does.
		req.thinkingRequested = req.Thinking != nil
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

	toolIDs := make(map[string]string)
	endpoint := endpointFingerprint(cfg.BaseURL)
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
			if items := replayNative(endpoint, m.Native); len(items) > 0 {
				msg.Content = items
				req.nativeEndpoint = m.Native.Endpoint
				if req.nativeMessages == nil {
					req.nativeMessages = make(map[int]*llm.NativeState)
				}
				req.nativeMessages[len(req.Messages)] = m.Native
				// Signed content stays untouched, so results must use its original
				// IDs — the paired ToolCalls already carry them.
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
				// Old sessions and source changes can leave display-only thinking.
				// An empty assistant message is not a valid substitute for it.
				continue
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

	return req
}

func normalizeToolCallID(id string) string {
	normalized := toolCallIDRegex.ReplaceAllString(id, "_")
	if len(normalized) > 64 {
		normalized = normalized[:64]
	}
	return normalized
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
		if req.Thinking.Type != "enabled" {
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
		if req.nativeEndpoint != "" && req.nativeEndpoint != endpointFingerprint(cfg.BaseURL) {
			yield(
				llm.StreamEvent{},
				errors.New(
					"anthropic: native thinking history belongs to another endpoint; rebuild the request with the selected endpoint",
				),
			)
			return
		}
		if err := validateThinking(req); err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		prefix, err := req.prepareNative()
		if err != nil {
			yield(llm.StreamEvent{}, fmt.Errorf("anthropic: prepare thinking history: %w", err))
			return
		}

		body, err := json.Marshal(req)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}

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

		// Captured state records the model that actually serves the request —
		// the post-hook req.Model, not the configured name — so the next
		// request matches replay against its own post-hook model.
		processStream(
			httpResp.Body,
			req.Model,
			endpointFingerprint(cfg.BaseURL),
			func(ev llm.StreamEvent, err error) bool {
				if ev.Final != nil && ev.Final.Native != nil {
					ev.Final.Native.Prefix = prefix
				}
				return yield(ev, err)
			},
		)
	}
}

// processStream takes the serving identity (model, endpoint) rather than the
// pre-hook ModelConfig: the capture must record what actually serves the
// request, and this signature makes reading any other config field a
// compile-time impossibility.
func processStream(body io.Reader, model, endpoint string, yield func(llm.StreamEvent, error) bool) {
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
	emitDelta := func(text, thinking string) bool {
		return yield(llm.StreamEvent{
			Type:  llm.StreamEventTypeDelta,
			Delta: llm.StreamDelta{Content: text, ReasoningContent: thinking},
			Usage: usage,
		}, nil)
	}

	for data, parseErr := range util.ParseDataStream(body) {
		if parseErr != nil {
			yield(llm.StreamEvent{Type: llm.StreamEventTypeError, Err: parseErr.Error()}, parseErr)
			return
		}
		payloadLine := bytes.TrimSpace(data)
		if len(payloadLine) == 0 {
			continue
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payloadLine, &envelope); err != nil {
			// Compatible gateways may append non-JSON trailers after the terminal event.
			if !stopped {
				native.invalid = true
			}
			continue
		}
		if stopped && (strings.HasPrefix(envelope.Type, "content_block_") ||
			strings.HasPrefix(envelope.Type, "message_")) {
			err := fmt.Errorf("anthropic: %s after message_stop; retry the request", envelope.Type)
			yield(llm.StreamEvent{Type: llm.StreamEventTypeError, Err: err.Error()}, err)
			return
		}

		switch envelope.Type {
		case "message_start":
			var msg struct {
				Message struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
						CacheRead   int `json:"cache_read_input_tokens"`
						CacheCreate int `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal(payloadLine, &msg); err != nil {
				continue
			}
			u := msg.Message.Usage
			// Anthropic reports disjoint buckets already: input_tokens excludes
			// both cache reads and cache writes, so it needs no splitting.
			usage.PromptTokens = u.InputTokens
			if u.CacheRead > 0 || u.CacheCreate > 0 {
				usage.PromptTokensDetails = &llm.PromptTokensDetails{
					CachedTokens:     u.CacheRead,
					CacheWriteTokens: u.CacheCreate,
				}
			}

		case "content_block_start":
			var block struct {
				Index        int             `json:"index"`
				ContentBlock json.RawMessage `json:"content_block"`
			}
			if err := json.Unmarshal(payloadLine, &block); err != nil {
				native.invalid = true
				continue
			}
			var start blockStart
			if err := json.Unmarshal(block.ContentBlock, &start); err != nil {
				native.invalid = true
				continue
			}
			native.start(block.Index, block.ContentBlock, start)
			if start.Type == "tool_use" {
				currentTool = &llm.ToolCall{
					Index: block.Index,
					ID:    start.ID,
					Type:  "function",
					Function: llm.Function{
						Name:      start.Name,
						Arguments: string(start.Input),
					},
				}
			}
			if start.Text != "" || start.Thinking != "" {
				content.WriteString(start.Text)
				reasoning.WriteString(start.Thinking)
				if !emitDelta(start.Text, start.Thinking) {
					return
				}
			}

		case "content_block_delta":
			var block struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					Signature   string `json:"signature"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			err := json.Unmarshal(payloadLine, &block)
			if block.Delta.Type == "thinking_delta" || block.Delta.Type == "signature_delta" {
				native.hasThinking = true
			}
			if err != nil {
				native.invalid = true
				continue
			}

			switch block.Delta.Type {
			case "text_delta":
				native.delta(block.Index, block.Delta.Type, block.Delta.Text)
				content.WriteString(block.Delta.Text)
				if !emitDelta(block.Delta.Text, "") {
					return
				}

			case "thinking_delta":
				native.delta(block.Index, block.Delta.Type, block.Delta.Thinking)
				reasoning.WriteString(block.Delta.Thinking)
				if !emitDelta("", block.Delta.Thinking) {
					return
				}

			case "signature_delta":
				native.delta(block.Index, block.Delta.Type, block.Delta.Signature)

			case "input_json_delta":
				native.delta(block.Index, block.Delta.Type, block.Delta.PartialJSON)
				if currentTool == nil {
					continue
				}
				toolArgs.WriteString(block.Delta.PartialJSON)
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
			var block struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal(payloadLine, &block); err != nil {
				native.invalid = true
				continue
			}
			native.stop(block.Index, toolArgs.String())
			if currentTool != nil && block.Index == currentTool.Index {
				if toolArgs.Len() > 0 {
					currentTool.Function.Arguments = toolArgs.String()
				}
				toolCalls = append(toolCalls, *currentTool)
				currentTool = nil
				toolArgs.Reset()
			}

		case "message_delta":
			var msgDelta struct {
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(payloadLine, &msgDelta); err != nil {
				continue
			}
			usage.CompletionTokens = msgDelta.Usage.OutputTokens
		case "message_stop":
			if native.block != nil {
				// A later block_stop cannot repair an already-ended message.
				native.invalid = true
			}
			stopped = true
		case "error":
			// No HTTP status applies to an in-stream event; don't invent one.
			err := fmt.Errorf("anthropic stream error: %s", llm.APIErrorMessage(payloadLine))
			yield(llm.StreamEvent{Type: llm.StreamEventTypeError, Err: err.Error()}, err)
			return
		}
		if native.hasThinking && native.invalid {
			err := errors.New("anthropic: incomplete thinking content or invalid block order; retry the request")
			yield(llm.StreamEvent{Type: llm.StreamEventTypeError, Err: err.Error()}, err)
			return
		}
	}
	if native.hasThinking && (!stopped || native.block != nil || native.invalid) {
		err := errors.New("anthropic: incomplete thinking content; retry the request")
		yield(llm.StreamEvent{Type: llm.StreamEventTypeError, Err: err.Error()}, err)
		return
	}

	// Anthropic sends no total; the buckets are disjoint, so they add up.
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens + usage.CachedTokens() + usage.CacheWriteTokens()
	done := llm.AssistantDone(content.String(), reasoning.String(), toolCalls, usage)
	done.Final.Native = native.state(model, endpoint)
	yield(done, nil)
}

// Compact sends a single non-streaming request and returns the assistant
// text. Satisfies llm.Compactor for session compaction on Claude.
func Compact(
	ctx context.Context,
	httpClient *http.Client,
	cfg llm.ModelConfig,
	req llm.CompactRequest,
) (llm.CompactResult, error) {
	// Anthropic requires max_tokens; fall back to the default cap when the
	// caller has no budget to derive one from.
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	body, err := json.Marshal(AnthropicRequest{
		Model:     cfg.Name,
		MaxTokens: maxTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: req.Prompt},
		},
	})
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
