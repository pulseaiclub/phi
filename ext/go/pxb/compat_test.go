package pxb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaggedRoundTripIntercept(t *testing.T) {
	in := InterceptReq{
		Event:      EvToolCall,
		ToolName:   "bash",
		ToolCallID: "c1",
		Input:      []byte(`{"command":"ls"}`),
		TurnIndex:  3,
	}
	out, err := DecodeInterceptReq(EncodeInterceptReq(in))
	require.NoError(t, err)
	assert.Equal(t, in.Event, out.Event)
	assert.Equal(t, in.ToolName, out.ToolName)
	assert.Equal(t, in.Input, out.Input)
	assert.Equal(t, in.TurnIndex, out.TurnIndex)
	assert.Empty(t, out.Prompt) // omitted on wire
}

func TestForwardCompatSkipsUnknownTags(t *testing.T) {
	// Simulate a newer encoder that adds tag 40 (unknown to current decoder)
	// after a normal EventNotify.
	base := EncodeEventNotify(EventNotify{
		Event:     EvTurnStart,
		TurnIndex: 7,
	})
	var fw fieldWriter
	// Re-encode manually: copy base then append unknown field via raw writer.
	// Use PutU64 through a second encode path — craft bytes with fieldWriter.
	fw.putU16(1, EvTurnStart)     // fEvEvent = 1
	fw.putU32(8, 7)               // fEvTurnIndex = 8
	fw.putU64(40, 0xdead)         // future field
	fw.putString(99, "ignore-me") // future string field

	out, err := DecodeEventNotify(fw.bytes())
	require.NoError(t, err)
	assert.Equal(t, EvTurnStart, out.Event)
	assert.Equal(t, uint32(7), out.TurnIndex)
	_ = base
}

func TestBackwardCompatMissingTagsAreZero(t *testing.T) {
	// Older encoder only sent Event + ToolName.
	var fw fieldWriter
	fw.putU16(1, EvToolCall)
	fw.putString(2, "bash")

	out, err := DecodeInterceptReq(fw.bytes())
	require.NoError(t, err)
	assert.Equal(t, EvToolCall, out.Event)
	assert.Equal(t, "bash", out.ToolName)
	assert.Empty(t, out.ToolCallID)
	assert.Nil(t, out.Input)
	assert.False(t, out.IsError)
	assert.Equal(t, uint32(0), out.TurnIndex)
}

func TestHelloOmitsEmpty(t *testing.T) {
	b := EncodeHello(Hello{Name: "x", Protocol: 1})
	h, err := DecodeHello(b)
	require.NoError(t, err)
	assert.Equal(t, "x", h.Name)
	assert.Equal(t, uint16(1), h.Protocol)
	assert.Empty(t, h.Version)
	assert.Equal(t, uint32(0), h.Caps)
}

func TestSubscribeRoundTrip(t *testing.T) {
	in := Subscribe{
		Events:    []uint16{EvAgentStart, EvTurnEnd},
		Intercept: []uint16{EvToolCall},
	}
	out, err := DecodeSubscribe(EncodeSubscribe(in))
	require.NoError(t, err)
	assert.Equal(t, in.Events, out.Events)
	assert.Equal(t, in.Intercept, out.Intercept)
}

func TestRegisterCommandNeedsArgsRoundTrip(t *testing.T) {
	in := RegisterCommand{Name: "plan", Description: "toggle plan", NeedsArgs: true}
	out, err := DecodeRegisterCommand(EncodeRegisterCommand(in))
	require.NoError(t, err)
	assert.Equal(t, in.Name, out.Name)
	assert.Equal(t, in.Description, out.Description)
	assert.True(t, out.NeedsArgs)

	// false omits the wire field (backward compatible with old hosts).
	raw := EncodeRegisterCommand(RegisterCommand{Name: "x", Description: "d"})
	var fw fieldWriter
	fw.putString(1, "x")
	fw.putString(2, "d")
	assert.Equal(t, fw.bytes(), raw)

	old, err := DecodeRegisterCommand(fw.bytes())
	require.NoError(t, err)
	assert.False(t, old.NeedsArgs)
}

func TestRegisterToolTimeoutSecRoundTrip(t *testing.T) {
	in := RegisterTool{
		Name: "fetch", Description: "HTTP GET", SchemaJSON: []byte(`{"type":"object"}`), TimeoutSec: 120,
	}
	out, err := DecodeRegisterTool(EncodeRegisterTool(in))
	require.NoError(t, err)
	assert.Equal(t, in.Name, out.Name)
	assert.Equal(t, in.TimeoutSec, out.TimeoutSec)
	assert.JSONEq(t, string(in.SchemaJSON), string(out.SchemaJSON))

	// Zero timeout omits the wire field (backward compatible with old hosts).
	raw := EncodeRegisterTool(RegisterTool{Name: "x", Description: "d"})
	var fw fieldWriter
	fw.putString(1, "x")
	fw.putString(2, "d")
	assert.Equal(t, fw.bytes(), raw)

	old, err := DecodeRegisterTool(fw.bytes())
	require.NoError(t, err)
	assert.Equal(t, uint32(0), old.TimeoutSec)
	assert.False(t, old.HasDetail)
}

func TestRegisterToolHasDetailRoundTrip(t *testing.T) {
	in := RegisterTool{
		Name: "t", Description: "d", SchemaJSON: []byte(`{}`), HasDetail: true,
	}
	out, err := DecodeRegisterTool(EncodeRegisterTool(in))
	require.NoError(t, err)
	assert.True(t, out.HasDetail)

	// False omits the field.
	raw := EncodeRegisterTool(RegisterTool{Name: "x", Description: "d"})
	var fw fieldWriter
	fw.putString(1, "x")
	fw.putString(2, "d")
	assert.Equal(t, fw.bytes(), raw)
}

func TestRegisterToolReadableRoundTrip(t *testing.T) {
	in := RegisterTool{
		Name: "read", Description: "read a file", SchemaJSON: []byte(`{}`), Readable: true,
	}
	out, err := DecodeRegisterTool(EncodeRegisterTool(in))
	require.NoError(t, err)
	assert.True(t, out.Readable)

	// False omits the field (old hosts stay sequential).
	raw := EncodeRegisterTool(RegisterTool{Name: "x", Description: "d"})
	var fw fieldWriter
	fw.putString(1, "x")
	fw.putString(2, "d")
	assert.Equal(t, fw.bytes(), raw)
}

func TestToolDetailResultRoundTrip(t *testing.T) {
	in := ToolDetailResult{Detail: "foo.go"}
	out, err := DecodeToolDetailResult(EncodeToolDetailResult(in))
	require.NoError(t, err)
	assert.Equal(t, in.Detail, out.Detail)
}

func TestToolResultExpandedRoundTrip(t *testing.T) {
	in := ToolResultMsg{
		Content: "plan body", Detail: "plan", Output: "plan body", Expanded: true,
	}
	out, err := DecodeToolResult(EncodeToolResult(in))
	require.NoError(t, err)
	assert.Equal(t, in, out)

	// False omits the field (old hosts stay collapsed).
	raw := EncodeToolResult(ToolResultMsg{Content: "c"})
	collapsed, err := DecodeToolResult(raw)
	require.NoError(t, err)
	assert.False(t, collapsed.Expanded)
}

func TestUnknownEventCodeMapsEmpty(t *testing.T) {
	assert.Empty(t, EventName(999))
	assert.Equal(t, uint16(0), EventCode("nope"))
}
