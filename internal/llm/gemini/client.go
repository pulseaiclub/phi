package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

const (
	defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	// finishReasonMaxTokens is the Gemini finish reason for a candidate that hit
	// the output cap: the text is a prefix, not a finished answer.
	finishReasonMaxTokens = "MAX_TOKENS"
)

type part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type inlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type functionCall struct {
	Name string `json:"name"`
	// Args is the call's argument object, kept as raw JSON. Gemini wants an
	// object here rather than the encoded string the other providers take, so
	// decoding it into a map and re-encoding would reorder keys and round
	// integers through float64 (a large one would not survive).
	Args json.RawMessage `json:"args,omitempty"`
}

type functionResponse struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type functionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type thinkingConfig struct {
	ThinkingBudget *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel  string `json:"thinkingLevel,omitempty"`
}

type GeminiRequest struct {
	SystemInstruction *content  `json:"systemInstruction,omitempty"`
	Contents          []content `json:"contents"`
	Tools             []struct {
		FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
	} `json:"tools,omitempty"`
	// ThinkingConfig opts Gemini 2.x+ out of default thinking so reasoning text
	// stays out of the visible stream unless the model cannot disable it.
	ThinkingConfig *thinkingConfig `json:"thinkingConfig,omitempty"`
	// GenerationConfig carries the output cap Compaction sets so a runaway
	// summary cannot outgrow the context the summary is meant to free.
	GenerationConfig *generationConfig `json:"generationConfig,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// SetMaxOutputTokens caps how many tokens the model may generate. n <= 0 keeps
// the provider default.
func (req *GeminiRequest) SetMaxOutputTokens(n int) {
	if n <= 0 {
		return
	}
	if req.GenerationConfig == nil {
		req.GenerationConfig = &generationConfig{}
	}
	req.GenerationConfig.MaxOutputTokens = n
}

// ApplyBudgetThinking maps ThinkConfig to Gemini 2.x thinkingBudget.
func (req *GeminiRequest) ApplyBudgetThinking(think llm.ThinkConfig) {
	if !think.Enabled {
		zero := 0
		req.ThinkingConfig = &thinkingConfig{ThinkingBudget: &zero}
		return
	}
	budget := mapThinkModeToBudget(think.Mode)
	req.ThinkingConfig = &thinkingConfig{ThinkingBudget: &budget}
}

// ApplyLevelThinking maps ThinkConfig to Gemini 3.x thinkingLevel.
// offLevel is used when thinking is disabled (models that cannot fully turn off).
func (req *GeminiRequest) ApplyLevelThinking(think llm.ThinkConfig, offLevel string) {
	if !think.Enabled {
		if offLevel == "" {
			offLevel = "MINIMAL"
		}
		req.ThinkingConfig = &thinkingConfig{ThinkingLevel: offLevel}
		return
	}
	req.ThinkingConfig = &thinkingConfig{ThinkingLevel: mapThinkModeToGeminiLevel(think.Mode)}
}

// mapThinkModeToGeminiLevel maps ThinkMode to Gemini 3's thinkingLevel string.
func mapThinkModeToGeminiLevel(mode llm.ThinkMode) string {
	switch mode {
	case llm.Minimal:
		return "MINIMAL"
	case llm.Low:
		return "LOW"
	case llm.Medium:
		return "MEDIUM"
	default: // high, xhigh, max, and anything else
		return "HIGH"
	}
}

// mapThinkModeToBudget maps ThinkMode to a token budget for Gemini 2.x.
func mapThinkModeToBudget(mode llm.ThinkMode) int {
	switch mode {
	case llm.Minimal:
		return 1024
	case llm.Low:
		return 2048
	case llm.Medium:
		return 8192
	default: // high, xhigh, max, and anything else
		return 16384
	}
}

func BuildRequest(
	system string,
	messages []llm.Message,
	tools []llm.ToolDefinition,
) GeminiRequest {
	var req GeminiRequest

	if strings.TrimSpace(system) != "" {
		req.SystemInstruction = &content{
			Parts: []part{
				{
					Text: system,
				},
			},
		}
	}

	for _, m := range messages {
		switch m.Role {
		case llm.RoleUser:
			parts := make([]part, 0, len(m.Images)+1)
			if m.Content != "" {
				parts = append(parts, part{Text: m.Content})
			}
			for _, img := range m.Images {
				parts = append(parts, part{InlineData: &inlineData{MIMEType: img.MimeType, Data: img.Data}})
			}
			req.Contents = append(req.Contents, content{Role: "user", Parts: parts})
		case llm.RoleTool:
			name := "tool"
			if m.ToolCallID != "" {
				name = m.ToolCallID
			}
			resp := part{
				FunctionResponse: &functionResponse{
					Name:     name,
					Response: map[string]any{"output": m.Content},
				},
			}
			// Gemini requires alternating turns; merge consecutive tool results into
			// the same user content so parallel tool calls produce a single user turn.
			if last := lastFunctionResponseContent(req.Contents); last != nil {
				last.Parts = append(last.Parts, resp)
			} else {
				req.Contents = append(req.Contents, content{Role: "user", Parts: []part{resp}})
			}
		case llm.RoleAssistant:
			c := toAssistantMessage(m)
			req.Contents = append(req.Contents, c)
		}
	}
	req.Tools = []struct {
		FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
	}{{FunctionDeclarations: toToolMessage(tools)}}
	return req
}

// lastFunctionResponseContent returns the trailing user content holding
// functionResponse parts, if any, so subsequent tool results can be merged into it.
func lastFunctionResponseContent(contents []content) *content {
	if len(contents) == 0 {
		return nil
	}
	last := &contents[len(contents)-1]
	if last.Role != "user" {
		return nil
	}
	for _, p := range last.Parts {
		if p.FunctionResponse != nil {
			return last
		}
	}
	return nil
}

func toToolMessage(tools []llm.ToolDefinition) []functionDeclaration {
	decls := make([]functionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, functionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  llm.MarshalToolParams(t.Params, `{"type":"object"}`),
		})
	}
	return decls
}

func toAssistantMessage(m llm.Message) content {
	c := content{Role: "model"}
	if m.Content != "" {
		c.Parts = append(c.Parts, part{Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		c.Parts = append(c.Parts, part{
			FunctionCall: &functionCall{
				Name: tc.Function.Name,
				Args: toolArgs(tc.Function.Arguments),
			},
		})
	}
	if len(c.Parts) == 0 {
		c.Parts = []part{{Text: ""}}
	}
	return c
}

// toolArgs passes the tool call's arguments through as the object Gemini
// requires. The validity check is not optional: an invalid raw message fails
// the encode of the whole request, so a call whose arguments are empty,
// truncated, or not an object gets no args field at all instead — which is
// what decoding into a map used to do.
func toolArgs(arguments string) json.RawMessage {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || trimmed[0] != '{' || !json.Valid([]byte(trimmed)) {
		return nil
	}
	return json.RawMessage(trimmed)
}

func getURL(model, baseURL, apiKey string, stream bool) string {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if model == "" {
		model = "gemini-2.5-flash"
	}
	if !strings.Contains(model, "/") {
		model = "models/" + model
	}
	action := ":generateContent"
	if stream {
		action = ":streamGenerateContent"
	}
	u, _ := url.Parse(baseURL + "/" + model + action)
	q := u.Query()
	if stream {
		q.Set("alt", "sse")
	}
	if apiKey != "" && !strings.Contains(baseURL, "aiplatform.googleapis.com") {
		q.Set("key", apiKey)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func setGeminiAuth(req *http.Request, baseURL, apiKey string) {
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" && strings.Contains(strings.ToLower(baseURL), "aiplatform.googleapis.com") {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func Stream(
	ctx context.Context,
	client *http.Client,
	config llm.ModelConfig,
	req *GeminiRequest,
) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		body, err := llm.MarshalRequest(req, config)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}

		httpReq, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			getURL(config.Name, config.BaseURL, config.APIKey, true),
			bytes.NewReader(body),
		)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		setGeminiAuth(httpReq, config.BaseURL, config.APIKey)

		httpResp, err := util.DoWithRetry(client, httpReq)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		defer httpResp.Body.Close()
		if httpResp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(httpResp.Body)
			yield(llm.StreamEvent{}, llm.FormatAPIError("gemini", httpResp.StatusCode, raw))
			return
		}
		processStream(httpResp.Body, yield)
	}
}

type chunk struct {
	Candidates []struct {
		Content struct {
			Parts []part `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`

	UsageMetadata struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
		ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
		TotalTokenCount         int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

func processStream(body io.Reader, yield func(llm.StreamEvent, error) bool) {
	var (
		text      strings.Builder
		reasoning strings.Builder
		toolCalls []llm.ToolCall
		usage     llm.Usage
	)
	for data, parseErr := range util.ParseDataStream(body) {
		if parseErr != nil {
			yield(llm.StreamEvent{Type: llm.StreamEventTypeError, Err: parseErr.Error()}, parseErr)
			return
		}
		line := bytes.TrimSpace(data)
		if len(line) == 0 {
			continue
		}

		var ck chunk
		if err := json.Unmarshal(line, &ck); err != nil {
			continue
		}

		u := ck.UsageMetadata
		if u.TotalTokenCount > 0 || u.PromptTokenCount > 0 {
			// promptTokenCount includes the cached content, so the cached part is
			// subtracted to leave the uncached bucket. Gemini bills no separate
			// cache write, so that bucket stays empty.
			usage.PromptTokens = max(u.PromptTokenCount-u.CachedContentTokenCount, 0)
			usage.CompletionTokens = u.CandidatesTokenCount + u.ThoughtsTokenCount
			usage.TotalTokens = u.TotalTokenCount
			if u.CachedContentTokenCount > 0 {
				usage.PromptTokensDetails = &llm.PromptTokensDetails{CachedTokens: u.CachedContentTokenCount}
			}
		}

		for _, cand := range ck.Candidates {
			for _, p := range cand.Content.Parts {
				// Thinking parts (Gemini 2.x+ with thoughts enabled) must not leak
				// into visible content; surface them as reasoning instead.
				if p.Thought && p.Text != "" {
					reasoning.WriteString(p.Text)
					if !yield(llm.StreamEvent{
						Type:  llm.StreamEventTypeDelta,
						Delta: llm.StreamDelta{ReasoningContent: p.Text},
					}, nil) {
						return
					}
					continue
				}
				if p.Text != "" {
					text.WriteString(p.Text)
					if !yield(llm.StreamEvent{
						Type:  llm.StreamEventTypeDelta,
						Delta: llm.StreamDelta{Content: p.Text},
					}, nil) {
						return
					}
				}

				if p.FunctionCall != nil && p.FunctionCall.Name != "" {
					call := toLLMToolCall(p, len(toolCalls))
					toolCalls = append(toolCalls, call)
					if !yield(llm.StreamEvent{
						Type:  llm.StreamEventTypeDelta,
						Delta: llm.StreamDelta{ToolCalls: []llm.ToolCall{call}},
					}, nil) {
						return
					}
				}
			}
		}
	}
	yield(llm.AssistantDone(text.String(), reasoning.String(), toolCalls, usage), nil)
}

func toLLMToolCall(p part, index int) llm.ToolCall {
	args, _ := json.Marshal(p.FunctionCall.Args)
	toolCall := llm.ToolCall{
		Index: index,
		ID:    p.FunctionCall.Name,
		Type:  "function",
		Function: llm.Function{
			Name:      p.FunctionCall.Name,
			Arguments: string(args),
		},
	}
	return toolCall
}

// CompactRequest POSTs a non-streaming Gemini body and returns assistant text.
func CompactRequest(
	ctx context.Context,
	client *http.Client,
	cfg llm.ModelConfig,
	req *GeminiRequest,
) (llm.CompactResult, error) {
	body, err := llm.MarshalRequest(req, cfg)
	if err != nil {
		return llm.CompactResult{}, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		getURL(cfg.Name, cfg.BaseURL, cfg.APIKey, false),
		bytes.NewReader(body),
	)
	if err != nil {
		return llm.CompactResult{}, err
	}
	setGeminiAuth(request, cfg.BaseURL, cfg.APIKey)
	httpResp, err := util.DoWithRetry(client, request)
	if err != nil {
		return llm.CompactResult{}, err
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return llm.CompactResult{}, err
	}
	if httpResp.StatusCode != http.StatusOK {
		return llm.CompactResult{}, llm.FormatAPIError("gemini", httpResp.StatusCode, raw)
	}
	var resp chunk
	if err := json.Unmarshal(raw, &resp); err != nil {
		return llm.CompactResult{}, err
	}

	var b strings.Builder
	truncated := false
	for _, c := range resp.Candidates {
		for _, p := range c.Content.Parts {
			b.WriteString(p.Text)
		}
		truncated = truncated || c.FinishReason == finishReasonMaxTokens
	}
	if b.Len() == 0 {
		return llm.CompactResult{}, errors.New("gemini API error: empty response")
	}
	return llm.CompactResult{Text: b.String(), Truncated: truncated}, nil
}
