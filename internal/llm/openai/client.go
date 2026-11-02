package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

const (
	chatCompletionsPath = "/chat/completions"
	// finishReasonLength is the OpenAI finish reason for a completion that hit
	// the output cap: the content is a prefix, not a finished answer.
	finishReasonLength = "length"
)

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type apiTool struct {
	Type     string             `json:"type"`
	Function llm.ToolDefinition `json:"function"`
}

type apiMessage struct {
	Role             llm.Role       `json:"role"`
	Content          any            `json:"content"` // string, or []contentPart when images are attached
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

// Request is the OpenAI-compatible chat completions body.
type Request struct {
	Model           string          `json:"model"`
	Messages        []apiMessage    `json:"messages"`
	Tools           []apiTool       `json:"tools,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	StreamOptions   *streamOptions  `json:"stream_options,omitempty"`
	Thinking        *ThinkingConfig `json:"thinking,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	// MaxTokens caps the completion length. Compaction sets it so a runaway
	// summary cannot outgrow the context the summary is meant to free.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// ThinkingConfig is a top-level vendor thinking toggle. ClearThinking is
// a z.ai addition: false keeps earlier reasoning in context ("preserved
// thinking"), which interleaved tool calling needs; nil omits the field so
// providers that do not know it are unaffected.
type ThinkingConfig struct {
	Type          string `json:"type"`
	ClearThinking *bool  `json:"clear_thinking,omitempty"`
}

type streamChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *usageWire     `json:"usage,omitempty"`
}

// usageWire is the provider's usage block. Vendors disagree on where cache hits
// live: OpenAI nests them under prompt_tokens_details, DeepSeek and Kimi report
// prompt_cache_hit_tokens, and OpenRouter-compatible providers add
// cache_write_tokens. prompt_tokens covers all of them, so the buckets are split
// out here before the counts reach the rest of the program.
type usageWire struct {
	PromptTokens         int               `json:"prompt_tokens"`
	CompletionTokens     int               `json:"completion_tokens"`
	PromptCacheHitTokens int               `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *usageWireDetails `json:"prompt_tokens_details"`
}

// usageWireDetails is the nested cache breakdown some vendors send.
type usageWireDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

// normalizeUsage splits the wire usage into disjoint buckets: prompt_tokens is
// the whole prompt, so cache reads and writes come out of it. The total is
// recomputed instead of trusted, because providers that report a gross
// prompt_tokens disagree about what their total_tokens already includes.
func normalizeUsage(w usageWire) llm.Usage {
	cacheRead := w.PromptCacheHitTokens
	cacheWrite := 0
	if d := w.PromptTokensDetails; d != nil {
		// Providers that send both fields agree; take the larger so a real hit
		// count is never dropped.
		cacheRead = max(cacheRead, d.CachedTokens)
		cacheWrite = d.CacheWriteTokens
	}
	usage := llm.Usage{
		CompletionTokens: w.CompletionTokens,
		PromptTokens:     max(w.PromptTokens-cacheRead-cacheWrite, 0),
	}
	if cacheRead > 0 || cacheWrite > 0 {
		usage.PromptTokensDetails = &llm.PromptTokensDetails{
			CachedTokens:     cacheRead,
			CacheWriteTokens: cacheWrite,
		}
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens + usage.CachedTokens() + usage.CacheWriteTokens()
	return usage
}

type streamChoice struct {
	Delta   llm.StreamDelta `json:"delta"`
	Message *llm.Message    `json:"message,omitempty"`
}

// toAPIMessage converts a normalized message. With attached images the
// content becomes the OpenAI content-parts array (text + image_url data
// URLs); otherwise it stays a plain string, keeping the wire shape stable.
func toAPIMessage(m llm.Message) apiMessage {
	out := apiMessage{
		Role:             m.Role,
		ReasoningContent: m.ReasoningContent,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
	}
	if len(m.Images) == 0 {
		out.Content = m.Content
		return out
	}
	parts := make([]any, 0, len(m.Images)+1)
	if m.Content != "" {
		parts = append(parts, map[string]string{"type": "text", "text": m.Content})
	}
	for _, img := range m.Images {
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]string{
				"url": "data:" + img.MimeType + ";base64," + img.Data,
			},
		})
	}
	out.Content = parts
	return out
}

// BuildRequest converts the normalized messages into an OpenAI-shaped request.
// The system prompt is prepended as a system message, mirroring the previous
// in-client behavior. Vendor-specific fields (e.g. thinking options) belong
// on model presets via RequestInterceptor, not here.
func BuildRequest(cfg llm.ModelConfig, system string, messages []llm.Message, tools []llm.ToolDefinition) *Request {
	msgs := make([]apiMessage, 0, len(messages)+1)
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, apiMessage{Role: llm.RoleSystem, Content: system})
	}
	for _, m := range messages {
		msgs = append(msgs, toAPIMessage(m))
	}

	apiTools := make([]apiTool, len(tools))
	for i, t := range tools {
		apiTools[i] = apiTool{Type: "function", Function: t}
	}

	req := &Request{
		Model:         cfg.Name,
		Messages:      msgs,
		Tools:         apiTools,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	req.ApplyThink(cfg.Think)
	return req
}

// ApplyThink shares reasoning settings between streaming and compaction.
func (r *Request) ApplyThink(think llm.ThinkConfig) {
	r.ReasoningEffort = ""
	if think.Enabled {
		r.ReasoningEffort = string(think.Mode)
	}
}

func chatCompletionsURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, chatCompletionsPath) {
		return baseURL
	}
	return baseURL + chatCompletionsPath
}

// chatHeaders returns chat-completions auth headers; a streaming request
// additionally advertises SSE.
func chatHeaders(apiKey string, stream bool) http.Header {
	h := http.Header{"Authorization": {"Bearer " + apiKey}}
	if stream {
		h.Set("Accept", util.ContentEventStream)
	}
	return h
}

// NewCompactRequest builds a summary request with the streaming reasoning settings.
func NewCompactRequest(cfg llm.ModelConfig, prompt string, maxTokens int) *Request {
	req := &Request{
		Model:     cfg.Name,
		Messages:  []apiMessage{{Role: llm.RoleUser, Content: prompt}},
		MaxTokens: maxTokens,
	}
	req.ApplyThink(cfg.Think)
	return req
}

// CompactRequest POSTs a non-streaming chat request body and returns assistant text.
func CompactRequest(
	ctx context.Context,
	httpClient *http.Client,
	cfg llm.ModelConfig,
	req *Request,
) (llm.CompactResult, error) {
	body, err := llm.MarshalRequest(req, cfg)
	if err != nil {
		return llm.CompactResult{}, err
	}

	raw, err := llm.PostBody(ctx, httpClient, "LLM",
		chatCompletionsURL(cfg.BaseURL), body, chatHeaders(cfg.APIKey, false))
	if err != nil {
		return llm.CompactResult{}, err
	}

	var resp struct {
		Choices []struct {
			Message      llm.Message `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return llm.CompactResult{}, err
	}

	if len(resp.Choices) == 0 {
		return llm.CompactResult{}, errors.New("LLM API error: empty choices")
	}
	return llm.CompactResult{
		Text:      resp.Choices[0].Message.Content,
		Truncated: resp.Choices[0].FinishReason == finishReasonLength,
	}, nil
}

// StreamChatCompletion POSTs a streaming chat completion and yields normalized events.
func StreamChatCompletion(
	ctx context.Context,
	httpClient *http.Client,
	cfg llm.ModelConfig,
	payload any,
) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		body, err := llm.MarshalRequest(payload, cfg)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}

		httpResp, err := llm.Post(ctx, httpClient, "LLM",
			chatCompletionsURL(cfg.BaseURL), body, chatHeaders(cfg.APIKey, true))
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		defer httpResp.Body.Close()

		var usage llm.Usage

		acc := newStreamAccumulator()

		for data, parseErr := range util.ParseDataStream(httpResp.Body) {
			if parseErr != nil {
				yield(llm.StreamEvent{}, parseErr)
				return
			}
			payloadLine := bytes.TrimSpace(data)
			if len(payloadLine) == 0 {
				continue
			}
			if bytes.Equal(payloadLine, []byte("[DONE]")) {
				break
			}
			decodeData := data
			if bytes.Contains(decodeData, []byte("\t")) {
				decodeData = bytes.ReplaceAll(decodeData, []byte("\t"), []byte(" "))
			}

			var chunk streamChunk
			if err := json.Unmarshal(decodeData, &chunk); err != nil {
				continue
			}
			if chunk.Usage != nil {
				usage = normalizeUsage(*chunk.Usage)
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			sc := chunk.Choices[0]
			delta := sc.Delta
			acc.applyDelta(delta)
			if sc.Message != nil {
				acc.applyMessage(sc.Message)
			}

			if hasStreamDelta(delta, sc.Message) {
				if !yield(llm.StreamEvent{
					Type:  llm.StreamEventTypeDelta,
					Delta: delta,
					Usage: usage,
				}, nil) {
					return
				}
			}
		}

		msg := acc.message()
		msg.Usage = usage
		yield(llm.StreamEvent{Type: llm.StreamEventTypeDone, Final: &msg}, nil)
	}
}

func hasStreamDelta(delta llm.StreamDelta, msg *llm.Message) bool {
	if delta.Content != "" || delta.ReasoningContent != "" || delta.Role != "" || len(delta.ToolCalls) > 0 {
		return true
	}
	if msg == nil {
		return false
	}
	return strings.TrimSpace(msg.Content) != "" ||
		strings.TrimSpace(msg.ReasoningContent) != "" ||
		len(msg.ToolCalls) > 0
}
