package interactions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

const provider = "gemini interactions"

// Stream runs one logical turn, including any continuations requested by Gemini.
func Stream(ctx context.Context, client *http.Client, cfg llm.ModelConfig, system string,
	messages []llm.Message, tools []llm.ToolDefinition,
) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		req, err := buildRequest(cfg, system, messages, tools, true, 0)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		done, _, err := run(ctx, client, cfg, req, yield)
		if !errors.Is(err, errStreamStopped) {
			yield(done, err)
		}
	}
}

// Compact runs an independent interaction with the summary's output cap.
func Compact(
	ctx context.Context,
	client *http.Client,
	cfg llm.ModelConfig,
	summary llm.CompactRequest,
) (llm.CompactResult, error) {
	input := []llm.Message{{Role: llm.RoleUser, Content: summary.Prompt}}
	req, err := buildRequest(cfg, "", input, nil, false, summary.MaxTokens)
	if err != nil {
		return llm.CompactResult{}, err
	}
	done, truncated, err := run(ctx, client, cfg, req, func(llm.StreamEvent, error) bool { return true })
	if err != nil {
		return llm.CompactResult{}, err
	}
	if done.Final.Content == "" && !truncated {
		return llm.CompactResult{}, errors.New("gemini interactions: empty summary")
	}
	return llm.CompactResult{Text: done.Final.Content, Truncated: truncated}, nil
}

func run(ctx context.Context, client *http.Client, cfg llm.ModelConfig, original request,
	yield func(llm.StreamEvent, error) bool,
) (llm.StreamEvent, bool, error) {
	req := original
	var text, reasoning strings.Builder
	var calls []llm.ToolCall
	var items []json.RawMessage
	var usage llm.Usage
	tokens := make(map[string]bool)
	for {
		state, err := runSegment(ctx, client, cfg, &req, yield)
		if err != nil {
			return llm.StreamEvent{}, false, err
		}
		truncated := state.Status == "incomplete" || state.Status == "budget_exceeded"
		switch state.Status {
		case "completed", "requires_action", "requires_continuation":
		case "incomplete", "budget_exceeded":
			if original.Stream {
				return llm.StreamEvent{}, false, fmt.Errorf(
					"gemini interactions: response ended with status %s",
					state.Status,
				)
			}
		default:
			return llm.StreamEvent{}, false, fmt.Errorf(
				"gemini interactions: unexpected terminal status %q",
				state.Status,
			)
		}
		part, steps, err := state.final()
		if err != nil {
			return llm.StreamEvent{}, false, err
		}
		text.WriteString(part.Final.Content)
		reasoning.WriteString(part.Final.ReasoningContent)
		calls = append(calls, part.Final.ToolCalls...)
		items = append(items, steps...)
		addUsage(&usage, part.Final.Usage)
		if state.Status != "requires_continuation" {
			if state.Status == "requires_action" && len(calls) == 0 {
				return llm.StreamEvent{}, false, errors.New(
					"gemini interactions: requires_action response has no function call",
				)
			}
			done := llm.AssistantDone(text.String(), reasoning.String(), calls, usage)
			if original.Stream {
				done.Final.ProviderState = original.providerState(state.ID, items, *done.Final)
			}
			return done, truncated, nil
		}
		if state.ContinuationToken == "" || tokens[state.ContinuationToken] {
			return llm.StreamEvent{}, false, errors.New("gemini interactions: missing or repeated continuation token")
		}
		tokens[state.ContinuationToken] = true
		req.ContinuationToken = state.ContinuationToken
		if req.Store {
			if state.ID == "" {
				return llm.StreamEvent{}, false, errors.New(
					"gemini interactions: stored continuation is missing its interaction ID",
				)
			}
			req.PreviousInteractionID = state.ID
			req.Input = nil
		}
	}
}

func addUsage(total *llm.Usage, segment llm.Usage) {
	total.PromptTokens += segment.PromptTokens
	total.CompletionTokens += segment.CompletionTokens
	total.TotalTokens += segment.TotalTokens
	if segment.PromptTokensDetails != nil {
		if total.PromptTokensDetails == nil {
			total.PromptTokensDetails = &llm.PromptTokensDetails{}
		}
		total.PromptTokensDetails.CachedTokens += segment.CachedTokens()
	}
}

func runSegment(ctx context.Context, client *http.Client, cfg llm.ModelConfig, req *request,
	yield func(llm.StreamEvent, error) bool,
) (*streamState, error) {
	body, err := llm.MarshalRequest(req, cfg)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(endpointURL(cfg.BaseURL))
	if err != nil {
		return nil, err
	}
	state := new(streamState)
	// Redirects can forward both the API key and the conversation to another origin.
	direct := *client
	direct.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	read := func(method, target string, payload io.Reader, stream bool) error {
		r, err := http.NewRequestWithContext(ctx, method, target, payload)
		if err != nil {
			return err
		}
		r.Header.Set("Api-Revision", "2026-05-20")
		if cfg.APIKey != "" {
			r.Header.Set("x-goog-api-key", cfg.APIKey)
		}
		if payload != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if stream {
			r.Header.Set("Accept", util.ContentEventStream)
		}
		resp, err := util.DoWithRetry(&direct, r)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && stream &&
			!strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
			return state.read(resp.Body, yield)
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return llm.FormatAPIError(provider, resp.StatusCode, raw)
		}
		return state.snapshot(raw, yield)
	}
	err = read(http.MethodPost, endpoint.String(), bytes.NewReader(body), req.Stream)
	for req.Background && state.ID != "" {
		if err == nil {
			if state.Status != "queued" && state.Status != "in_progress" {
				break
			}
		} else if !errors.Is(err, io.ErrUnexpectedEOF) {
			if _, ok := errors.AsType[net.Error](err); !ok || ctx.Err() != nil {
				break
			}
		}
		// A stored background job survives disconnects. Resume from its last event,
		// or poll the full snapshot when the service hasn't issued a cursor.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
		target := endpoint.JoinPath(url.PathEscape(state.ID))
		q := target.Query()
		// Input steps are already in the session and must not become part of the reply.
		q.Set("include_input", "false")
		resume := req.Stream && state.LastEventID != ""
		if resume {
			q.Set("stream", "true")
			q.Set("last_event_id", state.LastEventID)
		}
		target.RawQuery = q.Encode()
		err = read(http.MethodGet, target.String(), nil, resume)
	}
	return state, err
}
