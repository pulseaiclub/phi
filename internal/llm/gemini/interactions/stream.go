package interactions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/util"
)

var errStreamStopped = errors.New("interaction stream consumer stopped")

type streamState struct {
	ID, Status, ContinuationToken, LastEventID string
	steps                                      map[int]*streamStep
	usage                                      llm.Usage
	announced                                  map[string]bool
}

// Keep native fields (including opaque thought signatures) for stateless replay.
type streamStep struct {
	native map[string]any
	text   strings.Builder
	args   strings.Builder
}

type interactionUsage struct {
	Input   int `json:"total_input_tokens"`
	Cached  int `json:"total_cached_tokens"`
	Output  int `json:"total_output_tokens"`
	Thought int `json:"total_thought_tokens"`
	Total   int `json:"total_tokens"`
}

type interactionError struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
}

func (e *interactionError) err() error {
	if e == nil {
		return nil
	}
	return fmt.Errorf("gemini interactions error %s: %s", e.Code, e.Message)
}

type streamFrame struct {
	Type              string            `json:"event_type"`
	EventID           string            `json:"event_id"`
	Interaction       json.RawMessage   `json:"interaction"`
	InteractionID     string            `json:"interaction_id"`
	Status            string            `json:"status"`
	ContinuationToken string            `json:"continuation_token"`
	Index             int               `json:"index"`
	Step              json.RawMessage   `json:"step"`
	Delta             map[string]any    `json:"delta"`
	Usage             *interactionUsage `json:"usage"`
	Error             *interactionError `json:"error"`
}

func decodeFrame(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // Function arguments and opaque replay fields may contain 64-bit integers.
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.InputOffset() < int64(len(bytes.TrimRight(raw, " \t\r\n"))) {
		return errors.New("unexpected trailing JSON data")
	}
	return nil
}

func (s *streamState) read(r io.Reader, yield func(llm.StreamEvent, error) bool) error {
	for raw, err := range util.ParseDataStream(r) {
		if err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
			break
		}
		var frame streamFrame
		if err := decodeFrame(raw, &frame); err != nil {
			return fmt.Errorf("decode Gemini interaction event: %w", err)
		}
		if frame.EventID != "" && frame.EventID == s.LastEventID {
			continue
		}
		if frame.EventID != "" {
			s.LastEventID = frame.EventID
		}

		switch frame.Type {
		case "error":
			if err := frame.Error.err(); err != nil {
				return err
			}
			return errors.New("gemini interactions stream reported an error without details")
		case "interaction.created", "interaction.completed":
			if frame.Type == "interaction.completed" {
				// The final snapshot supersedes preliminary continuation tokens, even when it omits one.
				s.ContinuationToken = ""
			}
			if err := s.snapshot(frame.Interaction, yield); err != nil {
				return err
			}
			if frame.Type == "interaction.completed" {
				return nil
			}
		case "interaction.status_update":
			s.update(frame.InteractionID, frame.Status, frame.ContinuationToken, frame.Usage)
		case "step.start":
			if err := s.setStep(frame.Index, frame.Step, yield); err != nil {
				return err
			}
		case "step.delta":
			step := s.steps[frame.Index]
			if step == nil {
				return fmt.Errorf("gemini interaction delta for unopened step %d", frame.Index)
			}
			if err := step.applyDelta(frame.Delta, yield); err != nil {
				return err
			}
		case "step.stop":
			s.update("", "", "", frame.Usage)
		}
	}
	// Some streams close after a terminal status update. When a completed event
	// follows instead, consume it above to retain its full steps and final usage.
	switch s.Status {
	case "requires_continuation":
		return nil
	case "completed", "requires_action", "failed", "cancelled", "incomplete", "budget_exceeded":
		return s.emitTools(yield)
	}
	return io.ErrUnexpectedEOF
}

func (s *streamState) snapshot(raw []byte, yield func(llm.StreamEvent, error) bool) error {
	var snapshot struct {
		ID                string             `json:"id"`
		Status            string             `json:"status"`
		ContinuationToken string             `json:"continuation_token"`
		Steps             []json.RawMessage  `json:"steps"`
		Usage             *interactionUsage  `json:"usage"`
		Error             *interactionError  `json:"error"`
		Errors            []interactionError `json:"errors"`
		OutputText        string             `json:"output_text"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("decode Gemini interaction: %w", err)
	}
	if err := snapshot.Error.err(); err != nil {
		return err
	}
	if len(snapshot.Errors) > 0 {
		var details []error
		for _, failure := range snapshot.Errors {
			details = append(details, failure.err())
		}
		return errors.Join(details...)
	}
	s.update(snapshot.ID, snapshot.Status, snapshot.ContinuationToken, snapshot.Usage)
	for i, step := range snapshot.Steps {
		if err := s.setStep(i, step, yield); err != nil {
			return err
		}
	}
	if snapshot.OutputText != "" {
		if err := s.fallbackText(snapshot.OutputText, yield); err != nil {
			return err
		}
	}
	switch s.Status {
	case "completed", "requires_action":
		return s.emitTools(yield)
	}
	return nil
}

func (s *streamState) fallbackText(value string, yield func(llm.StreamEvent, error) bool) error {
	next := 0
	for i, step := range s.steps {
		if step.native["type"] == "model_output" {
			return nil
		}
		next = max(next, i+1)
	}
	if s.steps == nil {
		s.steps = make(map[int]*streamStep)
	}
	step := &streamStep{native: map[string]any{"type": "model_output"}}
	s.steps[next] = step
	step.text.WriteString(value)
	return step.emitText(value, yield)
}

func (s *streamState) emitTools(yield func(llm.StreamEvent, error) bool) error {
	calls, err := s.toolCalls()
	if err != nil {
		return err
	}
	for _, call := range calls {
		if s.announced[call.ID] {
			continue
		}
		if s.announced == nil {
			s.announced = make(map[string]bool)
		}
		s.announced[call.ID] = true
		event := llm.StreamEvent{
			Type: llm.StreamEventTypeDelta, Delta: llm.StreamDelta{ToolCalls: []llm.ToolCall{call}},
		}
		if !yield(event, nil) {
			return errStreamStopped
		}
	}
	return nil
}

func (s *streamState) update(id, status, token string, u *interactionUsage) {
	if id != "" {
		s.ID = id
	}
	if status != "" {
		s.Status = status
	}
	if token != "" {
		s.ContinuationToken = token
	}
	if u != nil {
		s.usage = llm.Usage{
			PromptTokens: max(0, u.Input-u.Cached), CompletionTokens: u.Output + u.Thought, TotalTokens: u.Total,
		}
		if u.Cached > 0 {
			s.usage.PromptTokensDetails = &llm.PromptTokensDetails{CachedTokens: u.Cached}
		}
	}
}

func (s *streamState) setStep(index int, raw json.RawMessage, yield func(llm.StreamEvent, error) bool) error {
	step := new(streamStep)
	if err := decodeFrame(raw, &step.native); err != nil {
		return fmt.Errorf("decode Gemini interaction step %d: %w", index, err)
	}
	if step.native["type"] == nil {
		return fmt.Errorf("gemini interaction step %d is missing its type", index)
	}
	if failure, ok := step.native["error"].(map[string]any); ok {
		return fmt.Errorf("gemini interaction step %d failed: %v", index, failure["message"])
	}
	var delivered string
	if old := s.steps[index]; old != nil {
		delivered = old.visibleText()
	}
	if s.steps == nil {
		s.steps = make(map[int]*streamStep)
	}
	s.steps[index] = step
	// The completed resource can repeat the same steps already streamed to the caller.
	if remaining, ok := strings.CutPrefix(step.visibleText(), delivered); ok {
		return step.emitText(remaining, yield)
	}
	return nil
}

func (p *streamStep) visibleText() string {
	var text strings.Builder
	key := "content"
	switch p.native["type"] {
	case "thought":
		key = "summary"
	case "model_output":
	default:
		return ""
	}
	parts, _ := p.native[key].([]any)
	for _, part := range parts {
		content, _ := part.(map[string]any)
		if content["type"] == "text" {
			value, _ := content["text"].(string)
			text.WriteString(value)
		}
	}
	text.WriteString(p.text.String())
	return text.String()
}

func (p *streamStep) emitText(value string, yield func(llm.StreamEvent, error) bool) error {
	if value == "" {
		return nil
	}
	var d llm.StreamDelta
	switch p.native["type"] {
	case "thought":
		d.ReasoningContent = value
	case "model_output":
		d.Content = value
	default:
		return nil
	}
	if !yield(llm.StreamEvent{Type: llm.StreamEventTypeDelta, Delta: d}, nil) {
		return errStreamStopped
	}
	return nil
}

func (p *streamStep) applyDelta(delta map[string]any, yield func(llm.StreamEvent, error) bool) error {
	var value string
	switch delta["type"] {
	case "text":
		value, _ = delta["text"].(string)
	case "thought_summary":
		content, _ := delta["content"].(map[string]any)
		if content["type"] == "text" {
			value, _ = content["text"].(string)
		}
	case "thought_signature":
		p.native["signature"] = delta["signature"]
	case "arguments_delta":
		arg, _ := delta["arguments"].(string)
		p.args.WriteString(arg)
	default:
		// Native tool steps can deliver IDs, results and signatures after step.start.
		if kind, ok := delta["type"].(string); ok && kind == p.native["type"] {
			maps.Copy(p.native, delta)
		}
	}
	p.text.WriteString(value)
	return p.emitText(value, yield)
}

func (p *streamStep) replay() (map[string]any, error) {
	item := maps.Clone(p.native)
	if p.text.Len() > 0 {
		key := "content"
		if item["type"] == "thought" {
			key = "summary"
		}
		parts, _ := item[key].([]any)
		item[key] = append(slices.Clone(parts), map[string]any{"type": "text", "text": p.text.String()})
	}
	if item["type"] == "function_call" {
		if p.args.Len() > 0 {
			var args map[string]any
			if err := decodeFrame([]byte(p.args.String()), &args); err != nil || args == nil {
				return nil, fmt.Errorf("invalid streamed arguments for Gemini function call %v", item["id"])
			}
			item["arguments"] = args
		} else if item["arguments"] == nil {
			item["arguments"] = map[string]any{}
		}
	}
	return item, nil
}

func (s *streamState) final() (llm.StreamEvent, []json.RawMessage, error) {
	var text, thought strings.Builder
	var items []json.RawMessage
	calls, err := s.toolCalls()
	if err != nil {
		return llm.StreamEvent{}, nil, err
	}
	for _, index := range slices.Sorted(maps.Keys(s.steps)) {
		step := s.steps[index]
		if step.native["type"] == "user_input" {
			continue
		}
		item, err := step.replay()
		if err != nil {
			return llm.StreamEvent{}, nil, err
		}
		switch item["type"] {
		case "model_output":
			text.WriteString(step.visibleText())
		case "thought":
			thought.WriteString(step.visibleText())
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return llm.StreamEvent{}, nil, fmt.Errorf("encode Gemini interaction replay step %d: %w", index, err)
		}
		items = append(items, raw)
	}
	return llm.AssistantDone(text.String(), thought.String(), calls, s.usage), items, nil
}

func (s *streamState) toolCalls() ([]llm.ToolCall, error) {
	resolved := make(map[string]bool)
	for _, step := range s.steps {
		if step.native["type"] == "function_result" {
			id, _ := step.native["call_id"].(string)
			resolved[id] = true
		}
	}
	var calls []llm.ToolCall
	for _, index := range slices.Sorted(maps.Keys(s.steps)) {
		step := s.steps[index]
		if step.native["type"] != "function_call" {
			continue
		}
		id, _ := step.native["id"].(string)
		name, _ := step.native["name"].(string)
		if id == "" || name == "" {
			return nil, fmt.Errorf("gemini function call at step %d lacks an id or name", index)
		}
		if resolved[id] {
			continue
		}
		item, err := step.replay()
		if err != nil {
			return nil, err
		}
		args, _ := json.Marshal(item["arguments"])
		calls = append(calls, llm.ToolCall{
			Index: len(calls), ID: id, Type: "function", Function: llm.Function{Name: name, Arguments: string(args)},
		})
	}
	return calls, nil
}
