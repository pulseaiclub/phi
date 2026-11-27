package interactions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
)

// DefaultBaseURL is the Gemini Developer API endpoint for Interactions.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type request struct {
	Model                 string            `json:"model"`
	Input                 []json.RawMessage `json:"input,omitempty"`
	SystemInstruction     string            `json:"system_instruction,omitempty"`
	Tools                 []requestTool     `json:"tools,omitempty"`
	GenerationConfig      *generationConfig `json:"generation_config,omitempty"`
	PreviousInteractionID string            `json:"previous_interaction_id,omitempty"`
	ContinuationToken     string            `json:"continuation_token,omitempty"`
	Stream                bool              `json:"stream"`
	Store                 bool              `json:"store"`
	Background            bool              `json:"background,omitempty"`

	endpoint string
	prefix   [sha256.Size]byte
}

type requestTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type generationConfig struct {
	ThinkingLevel     llm.ThinkMode `json:"thinking_level,omitempty"`
	ThinkingSummaries string        `json:"thinking_summaries,omitempty"`
	MaxOutputTokens   int           `json:"max_output_tokens,omitempty"`
}

// NormalizeBaseURL resolves a host, API base or full Interactions endpoint to its versioned API base.
func NormalizeBaseURL(base string) string {
	return interactionsURL(base, "")
}

func endpointURL(base string) string {
	return interactionsURL(base, "/interactions")
}

func interactionsURL(base, resource string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return base // The HTTP client reports malformed URLs.
	}
	path := strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/interactions")
	if !strings.HasSuffix(path, "/v1beta") {
		path += "/v1beta"
	}
	u.Path = path + resource
	return u.String()
}

func buildRequest(
	cfg llm.ModelConfig,
	system string,
	messages []llm.Message,
	tools []llm.ToolDefinition,
	stream bool,
	maxOutputTokens int,
) (request, error) {
	if err := cfg.RequestParameters.Validate(cfg.API); err != nil {
		return request{}, err
	}
	store, ok := cfg.RequestParameters["store"].(bool)
	if !ok {
		store = true
	}
	background, _ := cfg.RequestParameters["background"].(bool)
	r := request{Model: cfg.Name, Stream: stream, Store: store, Background: background}
	endpoint := sha256.Sum256([]byte(endpointURL(cfg.BaseURL)))
	r.endpoint = hex.EncodeToString(endpoint[:])
	systemParts := []string{}
	if system != "" {
		systemParts = append(systemParts, system)
	}
	for _, m := range messages {
		if m.Role == llm.RoleSystem && m.Content != "" {
			systemParts = append(systemParts, m.Content)
		}
	}
	r.SystemInstruction = strings.Join(systemParts, "\n")
	for _, tool := range tools {
		r.Tools = append(r.Tools, requestTool{
			Type: "function", Name: tool.Name, Description: tool.Description,
			Parameters: llm.MarshalToolParams(tool.Params, `{"type":"object"}`),
		})
	}
	generation := generationConfig{MaxOutputTokens: max(0, maxOutputTokens)}
	if cfg.Think.Enabled && cfg.Think.Mode != llm.Off {
		level := cfg.Think.Mode
		if level == llm.Max {
			level = llm.XHigh
		}
		generation.ThinkingLevel = level
		generation.ThinkingSummaries = "auto"
	}
	if generation != (generationConfig{}) {
		r.GenerationConfig = &generation
	}

	return r.withHistory(messages, true)
}

func (r request) withHistory(messages []llm.Message, allowRemote bool) (request, error) {
	r.Input = nil
	r.PreviousInteractionID = ""
	r.prefix = [sha256.Size]byte{}
	// Gemini chains only inputs and outputs. The system instruction and tools apply to this request.
	native := make([]bool, len(messages))
	start := 0
	for i, m := range messages {
		if m.Role == llm.RoleSystem {
			continue
		}
		r.prefix = messageDigest(r.prefix, m)
		if m.Role != llm.RoleAssistant {
			continue
		}
		state := m.ProviderState
		// A newer assistant reply supersedes any older continuation, even after switching providers.
		start, r.PreviousInteractionID = 0, ""
		if state == nil || state.Provider != llm.GeminiInteractions || state.Endpoint != r.endpoint {
			continue
		}
		sameHistory := state.Data.Prefix == hex.EncodeToString(r.prefix[:])
		native[i] = sameHistory
		if state.Data.Prefix == "" && len(state.Data.Items) > 0 {
			// Compaction replaced the prefix, but an unchanged tool turn still needs its original signature.
			native[i] = matchesRetainedTurn(m, state.Data)
		}
		if allowRemote && r.Store && sameHistory && state.Model == r.Model && state.Data.InteractionID != "" {
			start, r.PreviousInteractionID = i+1, state.Data.InteractionID
		}
	}
	calls := make(map[string]replayCall)
	if start > 0 {
		for _, tc := range messages[start-1].ToolCalls {
			calls[tc.ID] = replayCall{name: tc.Function.Name}
		}
	}
	for i := start; i < len(messages); i++ {
		m := messages[i]
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = replayCall{name: tc.Function.Name}
		}
		if native[i] && len(m.ProviderState.Data.Items) > 0 {
			r.Input = append(r.Input, m.ProviderState.Data.Items...)
			continue
		}
		if err := r.appendMessage(m, calls); err != nil {
			return request{}, err
		}
	}
	return r, nil
}

func matchesRetainedTurn(m llm.Message, data llm.ProviderData) bool {
	digest := messageDigest([sha256.Size]byte{}, m)
	if data.MessageDigest != "" {
		return data.MessageDigest == hex.EncodeToString(digest[:])
	}
	// Older sessions only saved the prefix. Recover the original assistant message from their saved steps.
	raw, err := json.Marshal(struct {
		Steps []json.RawMessage `json:"steps"`
	}{data.Items})
	if err != nil {
		return false
	}
	var captured streamState
	if err := captured.snapshot(raw, func(llm.StreamEvent, error) bool { return true }); err != nil {
		return false
	}
	event, _, err := captured.final()
	return err == nil && messageDigest([sha256.Size]byte{}, *event.Final) == digest
}

// Keep the continuation bound to the local branch, including edits to the assistant reply itself.
// Omitting ProviderState keeps opaque output out of the hash and its cost linear in normalized history.
func messageDigest(prefix [sha256.Size]byte, m llm.Message) [sha256.Size]byte {
	m.ProviderState = nil
	h := sha256.New()
	_, _ = h.Write(prefix[:])
	_ = json.NewEncoder(h).Encode(m) // The normalized message contains only JSON primitives.
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	return sum
}

func (r *request) providerState(id string, items []json.RawMessage, final llm.Message) *llm.ProviderState {
	if !r.Store {
		id = ""
	}
	if id == "" && len(items) == 0 {
		return nil
	}
	sum := messageDigest(r.prefix, final)
	digest := messageDigest([sha256.Size]byte{}, final)
	return &llm.ProviderState{
		Provider: llm.GeminiInteractions, Model: r.Model, Endpoint: r.endpoint,
		Data: llm.ProviderData{
			Prefix:        hex.EncodeToString(sum[:]),
			InteractionID: id,
			Items:         items,
			MessageDigest: hex.EncodeToString(digest[:]),
		},
	}
}

type inputStep struct {
	Type      string              `json:"type"`
	Content   []map[string]string `json:"content,omitempty"`
	ID        string              `json:"id,omitempty"`
	CallID    string              `json:"call_id,omitempty"`
	Name      string              `json:"name,omitempty"`
	Arguments json.RawMessage     `json:"arguments,omitempty"`
	Result    *string             `json:"result,omitempty"`
}

func (r *request) appendStep(step inputStep) error {
	raw, err := json.Marshal(step)
	if err == nil {
		r.Input = append(r.Input, raw)
	}
	return err
}

type replayCall struct {
	name    string
	skipped bool
}

func (r *request) appendMessage(m llm.Message, calls map[string]replayCall) error {
	switch m.Role {
	case llm.RoleUser, llm.RoleAssistant:
		step := inputStep{Type: "user_input"}
		if m.Role == llm.RoleAssistant {
			step.Type = "model_output"
		}
		if m.Content != "" || (m.Role == llm.RoleUser && len(m.Images) == 0) {
			step.Content = append(step.Content, map[string]string{"type": "text", "text": m.Content})
		}
		for _, image := range m.Images {
			step.Content = append(step.Content, map[string]string{
				"type": "image", "data": image.Data, "mime_type": image.MimeType,
			})
		}
		if len(step.Content) > 0 {
			if err := r.appendStep(step); err != nil {
				return err
			}
		}
		for _, tc := range m.ToolCalls {
			args := strings.TrimSpace(tc.Function.Arguments)
			if args == "" {
				args = "{}"
			}
			if args[0] != '{' || !json.Valid([]byte(args)) {
				// Another provider can persist partial arguments after an interrupted turn.
				calls[tc.ID] = replayCall{skipped: true}
				continue
			}
			if err := r.appendStep(inputStep{
				Type: "function_call", ID: tc.ID, Name: tc.Function.Name, Arguments: json.RawMessage(args),
			}); err != nil {
				return err
			}
		}
	case llm.RoleTool:
		call := calls[m.ToolCallID]
		if call.skipped {
			return nil
		}
		if m.ToolCallID == "" {
			return errors.New("gemini interactions: function result is missing a call ID")
		}
		return r.appendStep(inputStep{
			Type: "function_result", CallID: m.ToolCallID, Name: call.name, Result: &m.Content,
		})
	}
	return nil
}
