package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

// Responses completion states that mark a capped (partial) answer.
const (
	statusIncomplete          = "incomplete"
	incompleteMaxOutputTokens = "max_output_tokens"
)

func responsesURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, responsesPath) {
		return base
	}
	return base + responsesPath
}

// requestHeaders returns Responses API auth headers; a streaming request
// additionally advertises SSE.
func requestHeaders(apiKey string, stream bool) http.Header {
	h := http.Header{"Authorization": {"Bearer " + apiKey}}
	if stream {
		h.Set("Accept", util.ContentEventStream)
	}
	return h
}

// CompactRequest POSTs a non-streaming Responses create and returns assistant text.
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
		responsesURL(cfg.BaseURL), body, requestHeaders(cfg.APIKey, false))
	if err != nil {
		return llm.CompactResult{}, err
	}

	var resp compactResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return llm.CompactResult{}, err
	}

	text := resp.outputText()
	if text == "" {
		return llm.CompactResult{}, errors.New("LLM API error: empty Responses output")
	}
	return llm.CompactResult{Text: text, Truncated: resp.truncated()}, nil
}

type compactResponse struct {
	Status string `json:"status"`
	// IncompleteDetails carries why status is "incomplete"; only max_output_tokens
	// means the text is a prefix rather than a finished answer.
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

// truncated reports whether the response stopped at the output cap.
func (r compactResponse) truncated() bool {
	return r.Status == statusIncomplete &&
		r.IncompleteDetails != nil && r.IncompleteDetails.Reason == incompleteMaxOutputTokens
}

func (r compactResponse) outputText() string {
	var b strings.Builder
	for _, item := range r.Output {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Type == "output_text" || c.Type == "text" {
				b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}

// Stream POSTs a streaming Responses create and yields normalized events.
func Stream(
	ctx context.Context,
	httpClient *http.Client,
	cfg llm.ModelConfig,
	req *Request,
) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		body, err := llm.MarshalRequest(req, cfg)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}

		httpResp, err := llm.Post(ctx, httpClient, "LLM",
			responsesURL(cfg.BaseURL), body, requestHeaders(cfg.APIKey, true))
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		defer httpResp.Body.Close()

		processStream(httpResp.Body, yield)
	}
}

// streamEvent is a partial decode of Responses SSE payloads. Only fields we
// actually consume are listed; unknown event types are ignored.
type streamEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
	// function_call_arguments.done
	Arguments string `json:"arguments,omitempty"`
	Item      *struct {
		Type      string `json:"type"`
		ID        string `json:"id,omitempty"`
		CallID    string `json:"call_id,omitempty"`
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"item,omitempty"`
	Response *struct {
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			InputTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details,omitempty"`
		} `json:"usage,omitempty"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details,omitempty"`
	} `json:"response,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func processStream(body io.Reader, yield func(llm.StreamEvent, error) bool) {
	var (
		text      strings.Builder
		reasoning strings.Builder
		toolCalls []llm.ToolCall
		usage     llm.Usage
		// active function_call being streamed (by output order)
		activeIndex = -1
	)

	for data, parseErr := range util.ParseDataStream(body) {
		if parseErr != nil {
			yield(llm.StreamEvent{}, parseErr)
			return
		}
		payload := bytes.TrimSpace(data)
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}

		var ev streamEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "response.output_text.delta", "response.refusal.delta":
			if ev.Delta == "" {
				continue
			}
			text.WriteString(ev.Delta)
			if !yield(llm.StreamEvent{
				Type:  llm.StreamEventTypeDelta,
				Delta: llm.StreamDelta{Content: ev.Delta},
			}, nil) {
				return
			}

		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if ev.Delta == "" {
				continue
			}
			reasoning.WriteString(ev.Delta)
			if !yield(llm.StreamEvent{
				Type:  llm.StreamEventTypeDelta,
				Delta: llm.StreamDelta{ReasoningContent: ev.Delta},
			}, nil) {
				return
			}

		case "response.reasoning_summary_part.done":
			reasoning.WriteString("\n\n")
			if !yield(llm.StreamEvent{
				Type:  llm.StreamEventTypeDelta,
				Delta: llm.StreamDelta{ReasoningContent: "\n\n"},
			}, nil) {
				return
			}

		case "response.output_item.added":
			if ev.Item == nil || ev.Item.Type != "function_call" {
				continue
			}
			id := ev.Item.CallID
			if ev.Item.ID != "" {
				id += "|" + ev.Item.ID
			}
			tc := llm.ToolCall{
				Index: len(toolCalls),
				ID:    id,
				Type:  "function",
				Function: llm.Function{
					Name:      ev.Item.Name,
					Arguments: ev.Item.Arguments,
				},
			}
			toolCalls = append(toolCalls, tc)
			activeIndex = tc.Index
			if !yield(llm.StreamEvent{
				Type:  llm.StreamEventTypeDelta,
				Delta: llm.StreamDelta{ToolCalls: []llm.ToolCall{tc}},
			}, nil) {
				return
			}

		case "response.function_call_arguments.delta":
			if activeIndex < 0 || activeIndex >= len(toolCalls) || ev.Delta == "" {
				continue
			}
			toolCalls[activeIndex].Function.Arguments += ev.Delta
			delta := llm.ToolCall{
				Index: activeIndex,
				ID:    toolCalls[activeIndex].ID,
				Type:  "function",
				Function: llm.Function{
					Name:      toolCalls[activeIndex].Function.Name,
					Arguments: ev.Delta,
				},
			}
			if !yield(llm.StreamEvent{
				Type:  llm.StreamEventTypeDelta,
				Delta: llm.StreamDelta{ToolCalls: []llm.ToolCall{delta}},
			}, nil) {
				return
			}

		case "response.function_call_arguments.done":
			if activeIndex < 0 || activeIndex >= len(toolCalls) {
				continue
			}
			prev := toolCalls[activeIndex].Function.Arguments
			if ev.Arguments != "" {
				toolCalls[activeIndex].Function.Arguments = ev.Arguments
				if strings.HasPrefix(ev.Arguments, prev) {
					if d := ev.Arguments[len(prev):]; d != "" {
						delta := llm.ToolCall{
							Index: activeIndex,
							ID:    toolCalls[activeIndex].ID,
							Type:  "function",
							Function: llm.Function{
								Name:      toolCalls[activeIndex].Function.Name,
								Arguments: d,
							},
						}
						if !yield(llm.StreamEvent{
							Type:  llm.StreamEventTypeDelta,
							Delta: llm.StreamDelta{ToolCalls: []llm.ToolCall{delta}},
						}, nil) {
							return
						}
					}
				}
			}

		case "response.completed":
			if ev.Response != nil && ev.Response.Usage != nil {
				u := ev.Response.Usage
				// input_tokens includes the cached input, so cached tokens are
				// subtracted to leave the uncached bucket that the cache hit rate is
				// read from.
				cached := 0
				if u.InputTokensDetails != nil {
					cached = u.InputTokensDetails.CachedTokens
				}
				usage.PromptTokens = max(u.InputTokens-cached, 0)
				usage.CompletionTokens = u.OutputTokens
				usage.TotalTokens = u.TotalTokens
				if cached > 0 {
					usage.PromptTokensDetails = &llm.PromptTokensDetails{CachedTokens: cached}
				}
			}

		case "error":
			msg := ev.Message
			if msg == "" {
				msg = "unknown Responses stream error"
			}
			if ev.Code != "" {
				msg = "Error Code " + ev.Code + ": " + msg
			}
			yield(llm.StreamEvent{}, errors.New(msg))
			return

		case "response.failed":
			msg := "Unknown error (no error details in response)"
			if ev.Response != nil {
				if e := ev.Response.Error; e != nil {
					msg = e.Code + ": " + e.Message
				} else if d := ev.Response.IncompleteDetails; d != nil && d.Reason != "" {
					msg = "incomplete: " + d.Reason
				}
			}
			yield(llm.StreamEvent{}, errors.New(msg))
			return
		}
	}

	yield(llm.AssistantDone(text.String(), strings.TrimSpace(reasoning.String()), toolCalls, usage), nil)
}
