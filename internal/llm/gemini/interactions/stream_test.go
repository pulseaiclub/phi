package interactions

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func streamFrames(rows ...string) io.Reader {
	return strings.NewReader("data: " + strings.Join(rows, "\n\ndata: ") + "\n\n")
}

type streamRecording struct {
	text, thought strings.Builder
	calls         []llm.ToolCall
}

func (r *streamRecording) yield(e llm.StreamEvent, _ error) bool {
	r.text.WriteString(e.Delta.Content)
	r.thought.WriteString(e.Delta.ReasoningContent)
	r.calls = append(r.calls, e.Delta.ToolCalls...)
	return true
}

func TestStreamNormalizesInterleavedStepsAndPreservesReplay(t *testing.T) {
	var s streamState
	var r streamRecording
	require.NoError(t, s.read(streamFrames(
		`{"event_type":"interaction.created","interaction":{"id":"turn","status":"in_progress"}}`,
		`{"event_type":"step.start","index":0,"step":{"type":"thought"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"Think"}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"opaque"}}`,
		`{"event_type":"step.start","index":1,"step":{"type":"function_call","id":"a","name":"read","arguments":{}}}`,
		`{"event_type":"step.start","index":2,"step":{"type":"function_call","id":"b","name":"list","arguments":{}}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":"{\"n\":"}}`,
		`{"event_type":"step.delta","index":2,"delta":{"type":"arguments_delta","arguments":"{}"}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":"999999999999999999}"}}`,
		`{"event_type":"step.start","index":3,"step":{"type":"model_output","content":[{"type":"text","text":"A"}]}}`,
		`{"event_type":"step.delta","index":3,"delta":{"type":"text","text":"B"}}`,
		`{"event_type":"step.stop","index":3}`,
		`{"event_type":"step.start","index":4,"step":{"type":"google_search_call","id":"search"}}`,
		`{"event_type":"step.delta","index":4,"delta":{"type":"google_search_call","signature":"signed","arguments":{"queries":["q"]}}}`,
		`{"event_type":"step.start","index":5,"step":{"type":"function_result"}}`,
		`{"event_type":"step.delta","index":5,"delta":{"type":"function_result","call_id":"handled","result":"done"}}`,
		`{"event_type":"interaction.status_update","interaction_id":"turn","status":"requires_action"}`,
		`{"event_type":"interaction.completed","event_id":"end","interaction":{"id":"turn","status":"requires_action",`+
			`"usage":{"total_input_tokens":12,"total_cached_tokens":7,"total_output_tokens":3,"total_thought_tokens":4,"total_tokens":19}}}`,
	), r.yield))
	done, items, err := s.final()
	require.NoError(t, err)
	assert.Equal(t, "turn", s.ID)
	assert.Equal(t, "end", s.LastEventID)
	assert.Equal(t, "requires_action", s.Status)
	assert.Equal(t, "AB", r.text.String())
	assert.Equal(t, "Think", r.thought.String())
	assert.Equal(t, r.text.String(), done.Final.Content)
	assert.Equal(t, r.thought.String(), done.Final.ReasoningContent)
	assert.Equal(t, r.calls, done.Final.ToolCalls)
	require.Len(t, r.calls, 2)
	assert.Equal(t, "a", r.calls[0].ID)
	assert.Equal(t, `{"n":999999999999999999}`, r.calls[0].Function.Arguments)
	assert.Equal(t, "b", r.calls[1].ID)
	assert.Equal(t, "{}", r.calls[1].Function.Arguments)
	assert.Equal(t, 5, done.Final.Usage.PromptTokens)
	assert.Equal(t, 7, done.Final.Usage.CachedTokens())
	assert.Equal(t, 7, done.Final.Usage.CompletionTokens)
	assert.Equal(t, 19, done.Final.Usage.TotalTokens)
	require.Len(t, items, 6)
	assert.JSONEq(
		t,
		`{"type":"thought","signature":"opaque","summary":[{"type":"text","text":"Think"}]}`,
		string(items[0]),
	)
	assert.JSONEq(
		t,
		`{"type":"google_search_call","id":"search","signature":"signed","arguments":{"queries":["q"]}}`,
		string(items[4]),
	)
	assert.JSONEq(t, `{"type":"function_result","call_id":"handled","result":"done"}`, string(items[5]))
}

func TestStreamResumesAndDeduplicatesFinalSnapshot(t *testing.T) {
	var s streamState
	var r streamRecording
	const partial = `{"event_type":"step.delta","event_id":"cursor","index":0,"delta":{"type":"text","text":"one"}}`
	require.ErrorIs(t, s.read(streamFrames(
		`{"event_type":"interaction.created","interaction":{"id":"turn","status":"in_progress"}}`,
		`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`,
		partial,
	), r.yield), io.ErrUnexpectedEOF)
	assert.Equal(t, "cursor", s.LastEventID)
	require.NoError(t, s.read(streamFrames(
		partial,
		`{"event_type":"interaction.completed","interaction":{"id":"turn","status":"completed","output_text":"one two",`+
			`"steps":[{"type":"model_output","content":[{"type":"text","text":"one two","annotations":[{"id":"keep"}]}]}]}}`,
		`this frame must not be read after completion`,
	), r.yield))
	done, items, err := s.final()
	require.NoError(t, err)
	assert.Equal(t, "one two", r.text.String())
	assert.Equal(t, r.text.String(), done.Final.Content)
	require.Len(t, items, 1)
	assert.Contains(t, string(items[0]), `"annotations"`)
}

func TestStreamReconcilesOutputTextWithNativeSteps(t *testing.T) {
	var s streamState
	var r streamRecording
	for _, snapshot := range []string{
		`{"id":"turn","status":"in_progress","output_text":"A"}`,
		`{"id":"turn","status":"in_progress","output_text":"AB"}`,
	} {
		require.NoError(t, s.snapshot([]byte(snapshot), r.yield))
	}
	assert.Equal(t, "AB", r.text.String())
	require.NoError(t, s.read(streamFrames(
		`{"event_type":"step.start","index":0,"step":{"type":"thought","summary":[{"type":"text","text":"why"}]}}`,
		`{"event_type":"step.start","index":1,"step":{"type":"model_output","content":[{"type":"text","text":"A"}]}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"text","text":"BC"}}`,
		`{"event_type":"interaction.completed","interaction":{"id":"turn","status":"completed","steps":[`+
			`{"type":"thought","summary":[{"type":"text","text":"why"}]},`+
			`{"type":"model_output","content":[{"type":"text","text":"AB"}]},`+
			`{"type":"model_output","content":[{"type":"text","text":"CD"}]}]}}`,
	), r.yield))
	done, items, err := s.final()
	require.NoError(t, err)
	assert.Equal(t, "ABCD", r.text.String())
	assert.Equal(t, "why", r.thought.String())
	assert.Equal(t, r.text.String(), done.Final.Content)
	require.Len(t, items, 3)
	require.NoError(
		t,
		s.snapshot([]byte(`{"status":"completed","steps":[{"type":"thought","summary":[{"type":"text","text":"why"}]},
		{"type":"model_output","content":[{"type":"text","text":"ABCD!"}]}]}`), r.yield),
	)
	done, items, err = s.final()
	require.NoError(t, err)
	assert.Equal(t, "ABCD!", r.text.String())
	assert.Equal(t, r.text.String(), done.Final.Content)
	assert.Len(t, items, 2)
}

func TestThoughtSummaryReplaysOrderedContent(t *testing.T) {
	var s streamState
	var r streamRecording
	require.NoError(t, s.read(streamFrames(
		`{"event_type":"step.start","index":0,"step":{"type":"thought","summary":[{"type":"text","text":"look "}]}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"image","mime_type":"image/png","data":"opaque"}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"again","annotations":[]}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"signed"}}`,
		`{"event_type":"interaction.completed","interaction":{"status":"completed"}}`,
	), r.yield))
	done, items, err := s.final()
	require.NoError(t, err)
	assert.Equal(t, "look again", r.thought.String())
	assert.Equal(t, r.thought.String(), done.Final.ReasoningContent)
	require.Len(t, items, 1)
	assert.JSONEq(t, `{"type":"thought","signature":"signed","summary":[{"type":"text","text":"look "},
		{"type":"image","mime_type":"image/png","data":"opaque"},{"type":"text","text":"again","annotations":[]}]}`, string(items[0]))
}

func TestStreamClearsStaleCursor(t *testing.T) {
	var s streamState
	var r streamRecording
	require.ErrorIs(t, s.read(streamFrames(
		`{"event_type":"step.start","index":0,"event_id":"e1","step":{"type":"model_output"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"hi"}}`,
	), r.yield), io.ErrUnexpectedEOF)
	assert.Empty(t, s.LastEventID)
	require.ErrorIs(t, s.read(streamFrames(
		`{"event_type":"step.stop","event_id":"e3","index":0}`,
	), r.yield), io.ErrUnexpectedEOF)
	assert.Equal(t, "e3", s.LastEventID)
}

func TestSnapshotKeepsServerResultsOutOfPendingCalls(t *testing.T) {
	var s streamState
	var r streamRecording
	raw := []byte(`{"id":"one","status":"completed","output_text":"done","steps":[
		{"type":"user_input","content":[{"type":"text","text":"question"}]},
		{"type":"function_call","id":"finished","name":"lookup","arguments":{}},
		{"type":"function_result","call_id":"finished","name":"lookup","result":"found"}
	]}`)
	require.NoError(t, s.snapshot(raw, r.yield))
	require.NoError(t, s.snapshot(raw, r.yield))
	done, items, err := s.final()
	require.NoError(t, err)
	assert.Empty(t, done.Final.ToolCalls)
	assert.Empty(t, r.calls)
	assert.Equal(t, "done", r.text.String())
	assert.Equal(t, r.text.String(), done.Final.Content)
	require.Len(t, items, 3)
	assert.JSONEq(
		t,
		`{"type":"function_result","call_id":"finished","name":"lookup","result":"found"}`,
		string(items[1]),
	)
}

func TestStreamContinuationAndErrors(t *testing.T) {
	var s streamState
	var r streamRecording
	require.NoError(t, s.read(streamFrames(
		`{"event_type":"interaction.status_update","interaction_id":"seg","status":"requires_continuation","continuation_token":"tok"}`,
		`[DONE]`,
	), r.yield))
	assert.Equal(t, "seg", s.ID)
	assert.Equal(t, "requires_continuation", s.Status)
	assert.Equal(t, "tok", s.ContinuationToken)
	assert.ErrorContains(t, s.read(streamFrames(
		`{"event_type":"error","error":{"code":"INVALID_ARGUMENT","message":"bad schema"}}`,
	), r.yield), "bad schema")
	assert.ErrorContains(
		t,
		s.snapshot([]byte(`{"status":"failed","errors":[{"code":"FAILED","message":"budget"}]}`), r.yield),
		"budget",
	)
	assert.ErrorContains(
		t,
		s.snapshot([]byte(`{"steps":[{"type":"model_output","error":{"code":400,"message":"bad input"}}]}`), r.yield),
		"bad input",
	)
	assert.ErrorContains(
		t,
		s.read(streamFrames(`{"event_type":"step.delta","index":99,"delta":{"type":"text","text":"x"}}`), r.yield),
		"unopened step",
	)
}

func TestCompletedSnapshotSupersedesContinuationCheckpoint(t *testing.T) {
	const update = `{"event_type":"interaction.status_update","status":"requires_continuation","continuation_token":"old"}`
	for _, tc := range []struct{ name, snapshot, wantToken string }{
		{"replacement", `{"status":"requires_continuation","continuation_token":"next"}`, "next"},
		{"missing token", `{"status":"requires_continuation"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s streamState
			var r streamRecording
			require.NoError(t, s.read(streamFrames(update,
				`{"event_type":"interaction.completed","event_id":"final","interaction":`+tc.snapshot+`}`,
				`[DONE]`,
			), r.yield))
			assert.Equal(t, "final", s.LastEventID)
			assert.Equal(t, tc.wantToken, s.ContinuationToken)
		})
	}
}

func TestStreamStopsWhenConsumerStops(t *testing.T) {
	var s streamState
	stop := func(llm.StreamEvent, error) bool { return false }
	input := `{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"x"}]}}`
	require.ErrorIs(t, s.read(streamFrames(input), stop), errStreamStopped)
	require.ErrorIs(
		t,
		s.snapshot(
			json.RawMessage(
				`{"status":"completed","steps":[{"type":"function_call","id":"c","name":"read","arguments":{}}]}`,
			),
			stop,
		),
		errStreamStopped,
	)
}
