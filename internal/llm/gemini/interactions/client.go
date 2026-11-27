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
		done, _, err := run(ctx, client, cfg, req, messages, yield)
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
	done, truncated, err := run(ctx, client, cfg, req, input, func(llm.StreamEvent, error) bool { return true })
	if err != nil {
		return llm.CompactResult{}, err
	}
	if done.Final.Content == "" && !truncated {
		return llm.CompactResult{}, errors.New("gemini interactions: empty summary")
	}
	return llm.CompactResult{Text: done.Final.Content, Truncated: truncated}, nil
}

func run(ctx context.Context, client *http.Client, cfg llm.ModelConfig, original request,
	messages []llm.Message,
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
		if errors.Is(err, errMissingInteraction) {
			// Only the initial anchored POST can raise this error, before accepting the request.
			req, err = original.withHistory(messages, false)
			if err == nil {
				continue
			}
		}
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
		segment := part.Final.Usage
		if original.Background && segment.BilledTokens() > 0 {
			// A background resource counts the same input again on every server-side hop.
			initial := state.initialInput
			if initial == 0 {
				initial = estimatePrompt(original, messages)
			}
			output := segment.CompletionTokens
			if output == 0 {
				output = part.Final.EstimatedTokens()
			}
			segment.OccupiedTokens = initial + output
		}
		addUsage(&usage, segment)
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
	if occupied := segment.ContextTokens(); occupied > 0 {
		total.OccupiedTokens = occupied
	}
	total.PromptTokens += segment.PromptTokens
	total.CompletionTokens += segment.CompletionTokens
	total.TotalTokens += segment.BilledTokens()
	if segment.PromptTokensDetails != nil {
		if total.PromptTokensDetails == nil {
			total.PromptTokensDetails = &llm.PromptTokensDetails{}
		}
		total.PromptTokensDetails.CachedTokens += segment.CachedTokens()
	}
}

func estimatePrompt(req request, messages []llm.Message) int {
	chars := len(req.SystemInstruction)
	for _, t := range req.Tools {
		chars += len(t.Name) + len(t.Description) + len(t.Parameters)
	}
	tokens := (chars + 3) / 4
	for _, m := range messages {
		if m.Role != llm.RoleSystem {
			tokens += m.EstimatedTokens()
		}
	}
	return tokens
}

var errMissingInteraction = errors.New("previous interaction unavailable")

func (e *interactionError) missingAnchor(id string, allowResource bool) bool {
	if id == "" {
		return false
	}
	if allowResource {
		for _, detail := range e.Details {
			if detail.Type == "type.googleapis.com/google.rpc.ResourceInfo" &&
				strings.HasSuffix("/"+detail.ResourceName, "/interactions/"+id) {
				return true
			}
		}
	}
	message := strings.ToLower(strings.TrimSpace(e.Message))
	message = strings.NewReplacer("'", "", "\"", "", "`", "", "previous_interaction_id", "previous interaction").
		Replace(message)
	message = strings.TrimPrefix(message, "the ")
	subjects := []string{"interaction", "previous interaction"}
	if strings.Contains(e.Message, id) {
		id = strings.ToLower(id)
		subjects = append(subjects, "interaction "+id, "interaction with id "+id, "interactions/"+id)
	}
	for _, subject := range subjects {
		if reason, ok := strings.CutPrefix(message, subject+" "); ok {
			firstSentence, _, _ := strings.Cut(reason, ".")
			switch firstSentence {
			case "not found", "was not found", "does not exist", "has expired", "expired":
				return true
			}
		}
	}
	return false
}

func (e initialStreamError) missing(id string) bool {
	switch strings.ToLower(strings.Trim(string(e.Code), "\"")) {
	case "404", "410", "not_found", "gone":
		return e.missingAnchor(id, true)
	case "400", "invalid_request", "invalid_argument", "bad_request":
		return e.missingAnchor(id, false)
	}
	return false
}

func requestError(status int, raw []byte, anchor string) error {
	err := llm.FormatAPIError(provider, status, raw)
	if anchor == "" || (status != http.StatusNotFound && status != http.StatusBadRequest && status != http.StatusGone) {
		return err
	}
	// A missing model or endpoint can return the same HTTP status.
	var envelope struct {
		Error *interactionError `json:"error"`
	}
	var missing bool
	if json.Unmarshal(raw, &envelope) == nil {
		missing = envelope.Error != nil && envelope.Error.missingAnchor(anchor, status != http.StatusBadRequest)
	} else {
		// Streaming creates may return an SSE error frame even with a non-2xx HTTP status.
		var state streamState
		streamErr := state.read(bytes.NewReader(raw), func(llm.StreamEvent, error) bool { return false })
		rejection, ok := errors.AsType[initialStreamError](streamErr)
		missing = ok && rejection.missing(anchor)
	}
	if missing {
		return fmt.Errorf("%w: %w", errMissingInteraction, err)
	}
	return err
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
	send := func(callCtx context.Context, method, target string, payload io.Reader, stream bool) (*http.Response, error) {
		r, err := http.NewRequestWithContext(callCtx, method, target, payload)
		if err != nil {
			return nil, err
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
		// A gateway failure can hide an accepted background job. Another create would start a second job.
		if req.Background && method == http.MethodPost {
			return direct.Do(r)
		}
		return util.DoWithRetry(&direct, r)
	}
	read := func(method, target string, payload io.Reader, stream bool) error {
		resp, err := send(ctx, method, target, payload, stream)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var anchor string
		if method == http.MethodPost && req.ContinuationToken == "" {
			anchor = req.PreviousInteractionID
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && stream &&
			!strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
			err := state.read(resp.Body, yield)
			if rejection, ok := errors.AsType[initialStreamError](err); ok && rejection.missing(anchor) {
				return fmt.Errorf("%w: %w", errMissingInteraction, err)
			}
			return err
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return requestError(resp.StatusCode, raw, anchor)
		}
		return state.snapshot(raw, yield)
	}
	err = read(http.MethodPost, endpoint.String(), bytes.NewReader(body), req.Stream)
follow:
	for state.shouldFollow(req, err) {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			break follow
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
	if req.Background && state.ID != "" && state.active() && (ctx.Err() != nil || errors.Is(err, errStreamStopped)) {
		// Stopping local reads does not stop billing for a background interaction.
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		target := endpoint.JoinPath(url.PathEscape(state.ID), "cancel")
		if resp, cancelErr := send(cancelCtx, http.MethodPost, target.String(), nil, false); cancelErr == nil {
			_ = resp.Body.Close()
		}
	}
	return state, err
}

func (s *streamState) active() bool {
	return s.Status == "queued" || s.Status == "in_progress"
}

func (s *streamState) shouldFollow(req *request, err error) bool {
	if s.ID == "" || !req.Store {
		return false
	}
	if err == nil {
		return req.Background && s.active()
	}
	if !req.Background && (!req.Stream || s.LastEventID == "") {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	if networkErr, ok := errors.AsType[net.Error](err); ok && networkErr.Timeout() {
		return true
	}
	op, ok := errors.AsType[*net.OpError](err)
	return ok && op.Op == "read"
}
