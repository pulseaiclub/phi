package responses

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/core"
)

const responsesPath = "/responses"

// Request is the OpenAI Responses API create body.
type Request struct {
	Model     string      `json:"model"`
	Input     []InputItem `json:"input"`
	Tools     []Tool      `json:"tools,omitempty"`
	Stream    bool        `json:"stream,omitempty"`
	Store     bool        `json:"store"`
	Reasoning *Reasoning  `json:"reasoning,omitempty"`
	// MaxOutputTokens caps the completion length. Compaction sets it so a
	// runaway summary cannot outgrow the context the summary is meant to free.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

// Reasoning maps ThinkConfig onto Responses reasoning.effort / summary.
type Reasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// Tool is a Responses function tool (flat name/description, not nested under function).
type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict"`
}

// InputItem is one Responses input entry. Role-only messages and typed items
// (function_call / function_call_output / message) share this shape.
type InputItem struct {
	Type    string `json:"type,omitempty"`
	Role    string `json:"role,omitempty"`
	Content any    `json:"content,omitempty"`
	Status  string `json:"status,omitempty"`
	ID      string `json:"id,omitempty"`

	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    any    `json:"output,omitempty"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Detail   string `json:"detail,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// BuildRequest converts normalized messages into a Responses create body.
// System text uses role "developer" when thinking is enabled (OpenAI Responses behavior).
func BuildRequest(cfg core.ModelConfig, system string, messages []core.Message, tools []core.ToolDefinition) *Request {
	input := make([]InputItem, 0, len(messages)+1)
	if s := strings.TrimSpace(system); s != "" {
		role := "system"
		if cfg.Think.Enabled {
			role = "developer"
		}
		input = append(input, InputItem{Role: role, Content: s})
	}

	msgIndex := 0
	for _, m := range messages {
		switch m.Role {
		case core.RoleSystem:
			role := "system"
			if cfg.Think.Enabled {
				role = "developer"
			}
			input = append(input, InputItem{Role: role, Content: m.Content})
		case core.RoleUser:
			input = append(input, userItem(m))
		case core.RoleAssistant:
			input = append(input, assistantItems(m, msgIndex)...)
		case core.RoleTool:
			input = append(input, InputItem{
				Type:   "function_call_output",
				CallID: callID(m.ToolCallID),
				Output: m.Content,
			})
		}
		msgIndex++
	}

	apiTools := make([]Tool, len(tools))
	for i, t := range tools {
		apiTools[i] = Tool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  marshalToolParams(t.Params),
			Strict:      false,
		}
	}

	req := &Request{
		Model:  cfg.Name,
		Input:  input,
		Tools:  apiTools,
		Stream: true,
		Store:  false,
	}
	req.ApplyThink(cfg.Think)
	return req
}

// ApplyThink keeps streaming and compaction reasoning mappings identical.
func (r *Request) ApplyThink(think core.ThinkConfig) {
	r.Reasoning = nil
	if think.Enabled {
		r.Reasoning = &Reasoning{Effort: string(think.Mode), Summary: "auto"}
	}
}

func userItem(m core.Message) InputItem {
	if len(m.Images) == 0 {
		return InputItem{
			Role: "user",
			Content: []contentPart{{
				Type: "input_text",
				Text: m.Content,
			}},
		}
	}
	parts := make([]contentPart, 0, len(m.Images)+1)
	if m.Content != "" {
		parts = append(parts, contentPart{Type: "input_text", Text: m.Content})
	}
	for _, img := range m.Images {
		parts = append(parts, contentPart{
			Type:     "input_image",
			Detail:   "auto",
			ImageURL: "data:" + img.MimeType + ";base64," + img.Data,
		})
	}
	return InputItem{Role: "user", Content: parts}
}

func assistantItems(m core.Message, msgIndex int) []InputItem {
	out := make([]InputItem, 0, 1+len(m.ToolCalls))
	if m.Content != "" {
		out = append(out, InputItem{
			Type:   "message",
			Role:   "assistant",
			Status: "completed",
			ID:     fmt.Sprintf("msg_phi_%d", msgIndex),
			Content: []contentPart{{
				Type: "output_text",
				Text: m.Content,
			}},
		})
	}
	for _, tc := range m.ToolCalls {
		out = append(out, InputItem{
			Type:      "function_call",
			CallID:    callID(tc.ID),
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out
}

// callID strips an optional "|item_id" suffix (some providers store call_id|fc_xxx).
func callID(id string) string {
	if before, _, ok := strings.Cut(id, "|"); ok {
		return before
	}
	return id
}

func marshalToolParams(params *core.FunctionParameters) json.RawMessage {
	if params == nil {
		return nil
	}
	raw, err := json.Marshal(struct {
		Type       string      `json:"type"`
		Properties core.Object `json:"properties"`
		Required   []string    `json:"required,omitempty"`
	}{params.Type, params.Properties, params.Required})
	if err != nil {
		return nil
	}
	return raw
}

// NewCompactRequest builds a summary request with the streaming reasoning settings.
func NewCompactRequest(cfg core.ModelConfig, prompt string, maxTokens int) *Request {
	req := &Request{
		Model: cfg.Name,
		Input: []InputItem{{
			Role: "user",
			Content: []contentPart{{
				Type: "input_text",
				Text: prompt,
			}},
		}},
		Store:           false,
		MaxOutputTokens: maxTokens,
	}
	req.ApplyThink(cfg.Think)
	return req
}
