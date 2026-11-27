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
	stored                                     bool
	steps                                      map[int]*streamStep
	output                                     strings.Builder
	stepOutputLen                              int
	usage                                      llm.Usage
	initialInput                               int
	announced                                  map[string]bool
}

// Keep native fields (including opaque thought signatures) for stateless replay.
type streamStep struct {
	native map[string]any
	text   strings.Builder
	args   strings.Builder
}

type interactionUsage struct {
	InitialInput int `json:"initial_input_tokens"`
	Input        int `json:"total_input_tokens"`
	Cached       int `json:"total_cached_tokens"`
	Output       int `json:"total_output_tokens"`
	Thought      int `json:"total_thought_tokens"`
	Total        int `json:"total_tokens"`
}

type interactionError struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Details []struct {
		Type         string `json:"@type"`
		ResourceName string `json:"resourceName"`
	} `json:"details"`
}

func (e *interactionError) Error() string {
	return fmt.Sprintf("gemini interactions error %s: %s", e.Code, e.Message)
}

// An error as the first event can reject a stored anchor before work starts.
type initialStreamError struct{ *interactionError }

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
	first := true
	terminated := false
	for raw, err := range util.ParseDataStream(r) {
		if err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
			terminated = true
			break
		}
		var frame streamFrame
		if err := decodeFrame(raw, &frame); err != nil {
			return fmt.Errorf("decode Gemini interaction event: %w", err)
		}
		if frame.EventID != "" && frame.EventID == s.LastEventID {
			continue
		}
		// Cursorless changes cannot be resumed from an older event without replaying them.
		switch frame.Type {
		case "interaction.created",
			"interaction.completed",
			"interaction.status_update",
			"step.start",
			"step.delta",
			"step.stop":
			s.LastEventID = frame.EventID
		}

		switch frame.Type {
		case "error":
			if frame.Error == nil {
				return errors.New("gemini interactions stream reported an error without details")
			}
			if first && frame.InteractionID == "" && len(frame.Interaction) == 0 {
				return initialStreamError{frame.Error}
			}
			return frame.Error
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
			if s.steps[frame.Index].native["type"] == "model_output" {
				if err := s.syncStepOutput(yield); err != nil {
					return err
				}
			}
		case "step.delta":
			step := s.steps[frame.Index]
			if step == nil {
				return fmt.Errorf("gemini interaction delta for unopened step %d", frame.Index)
			}
			if err := s.applyDelta(step, frame.Delta, yield); err != nil {
				return err
			}
		case "step.stop":
			s.update("", "", "", frame.Usage)
		}
		first = false
	}
	// A status update can precede the final usage and steps. A stored stream can recover them.
	if !terminated && s.stored && s.ID != "" && s.LastEventID != "" {
		return io.ErrUnexpectedEOF
	}
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
	if snapshot.Error != nil {
		return snapshot.Error
	}
	if len(snapshot.Errors) > 0 {
		var details []error
		for i := range snapshot.Errors {
			details = append(details, &snapshot.Errors[i])
		}
		return errors.Join(details...)
	}
	s.update(snapshot.ID, snapshot.Status, snapshot.ContinuationToken, snapshot.Usage)
	for i, step := range snapshot.Steps {
		if err := s.setStep(i, step, yield); err != nil {
			return err
		}
	}
	if len(snapshot.Steps) > 0 {
		// A full snapshot may group the same output into fewer steps than the stream.
		for i := range s.steps {
			if i >= len(snapshot.Steps) {
				delete(s.steps, i)
			}
		}
		if err := s.syncStepOutput(yield); err != nil {
			return err
		}
	}
	if snapshot.OutputText != "" && (len(snapshot.Steps) == 0 || s.stepOutputLen == 0) {
		if err := s.emitOutput(0, snapshot.OutputText, yield); err != nil {
			return err
		}
	}
	switch s.Status {
	case "completed", "requires_action":
		return s.emitTools(yield)
	}
	return nil
}

func (s *streamState) syncStepOutput(yield func(llm.StreamEvent, error) bool) error {
	var text strings.Builder
	for _, i := range slices.Sorted(maps.Keys(s.steps)) {
		step := s.steps[i]
		if step.native["type"] == "model_output" {
			text.WriteString(step.visibleText())
		}
	}
	s.stepOutputLen = text.Len()
	return s.emitOutput(0, text.String(), yield)
}

// A snapshot's output_text can arrive before the indexed steps for the same text.
// Only the suffix beyond the response-wide text already delivered is new.
func (s *streamState) emitOutput(offset int, value string, yield func(llm.StreamEvent, error) bool) error {
	prior := s.output.String()[offset:]
	overlap := min(len(prior), len(value))
	if prior[:overlap] != value[:overlap] {
		return errors.New("gemini interactions: model output differs from previously streamed text")
	}
	if suffix := value[overlap:]; suffix != "" {
		s.output.WriteString(suffix)
		if !yield(llm.StreamEvent{Type: llm.StreamEventTypeDelta, Delta: llm.StreamDelta{Content: suffix}}, nil) {
			return errStreamStopped
		}
	}
	return nil
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
		if u.InitialInput > 0 {
			s.initialInput = u.InitialInput
		}
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
	if old := s.steps[index]; old != nil && old.native["type"] == "thought" {
		delivered = old.visibleText()
	}
	if s.steps == nil {
		s.steps = make(map[int]*streamStep)
	}
	s.steps[index] = step
	// The completed resource can repeat the same steps already streamed to the caller.
	if step.native["type"] == "thought" {
		if remaining, ok := strings.CutPrefix(step.visibleText(), delivered); ok {
			return emitThought(remaining, yield)
		}
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

func emitThought(value string, yield func(llm.StreamEvent, error) bool) error {
	if value == "" {
		return nil
	}
	if !yield(llm.StreamEvent{Type: llm.StreamEventTypeDelta, Delta: llm.StreamDelta{ReasoningContent: value}}, nil) {
		return errStreamStopped
	}
	return nil
}

func (s *streamState) applyDelta(p *streamStep, delta map[string]any, yield func(llm.StreamEvent, error) bool) error {
	switch delta["type"] {
	case "text":
		value, _ := delta["text"].(string)
		p.text.WriteString(value)
		if p.native["type"] == "model_output" {
			offset := s.stepOutputLen
			s.stepOutputLen += len(value)
			return s.emitOutput(offset, value, yield)
		}
		if p.native["type"] == "thought" {
			return emitThought(value, yield)
		}
	case "thought_summary":
		content, ok := delta["content"].(map[string]any)
		if !ok {
			return errors.New("gemini interactions: thought summary delta is missing its content item")
		}
		parts, _ := p.native["summary"].([]any)
		p.native["summary"] = append(parts, content)
		if content["type"] == "text" {
			value, _ := content["text"].(string)
			return emitThought(value, yield)
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
	return nil
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
	var thought strings.Builder
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
		if item["type"] == "thought" {
			thought.WriteString(step.visibleText())
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return llm.StreamEvent{}, nil, fmt.Errorf("encode Gemini interaction replay step %d: %w", index, err)
		}
		items = append(items, raw)
	}
	if suffix := s.output.String()[s.stepOutputLen:]; suffix != "" {
		// Some snapshots only contain output_text. Synthesize only what the native steps omit.
		raw, err := json.Marshal(
			inputStep{Type: "model_output", Content: []map[string]string{{"type": "text", "text": suffix}}},
		)
		if err != nil {
			return llm.StreamEvent{}, nil, err
		}
		items = append(items, raw)
	}
	return llm.AssistantDone(s.output.String(), thought.String(), calls, s.usage), items, nil
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
