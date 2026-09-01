package pxb_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/pxb"
)

func TestTaggedRoundTripIntercept(t *testing.T) {
	in := pxb.InterceptReq{
		Event:      pxb.EvToolCall,
		ToolName:   "bash",
		ToolCallID: "c1",
		Input:      []byte(`{"command":"ls"}`),
		TurnIndex:  3,
	}
	out, err := pxb.DecodeInterceptReq(pxb.EncodeInterceptReq(in))
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
	base := pxb.EncodeEventNotify(pxb.EventNotify{
		Event:     pxb.EvTurnStart,
		TurnIndex: 7,
	})
	var fw pxb.FieldWriter
	// Re-encode manually: copy base then append unknown field via raw writer.
	// Use PutU64 through a second encode path — craft bytes with FieldWriter.
	fw.PutU16(1, pxb.EvTurnStart) // fEvEvent = 1
	fw.PutU32(8, 7)               // fEvTurnIndex = 8
	fw.PutU64(40, 0xdead)         // future field
	fw.PutString(99, "ignore-me") // future string field

	out, err := pxb.DecodeEventNotify(fw.Bytes())
	require.NoError(t, err)
	assert.Equal(t, pxb.EvTurnStart, out.Event)
	assert.Equal(t, uint32(7), out.TurnIndex)
	_ = base
}

func TestBackwardCompatMissingTagsAreZero(t *testing.T) {
	// Older encoder only sent Event + ToolName.
	var fw pxb.FieldWriter
	fw.PutU16(1, pxb.EvToolCall)
	fw.PutString(2, "bash")

	out, err := pxb.DecodeInterceptReq(fw.Bytes())
	require.NoError(t, err)
	assert.Equal(t, pxb.EvToolCall, out.Event)
	assert.Equal(t, "bash", out.ToolName)
	assert.Empty(t, out.ToolCallID)
	assert.Nil(t, out.Input)
	assert.False(t, out.IsError)
	assert.Equal(t, uint32(0), out.TurnIndex)
}

func TestHelloOmitsEmpty(t *testing.T) {
	b := pxb.EncodeHello(pxb.Hello{Name: "x", Protocol: 1})
	h, err := pxb.DecodeHello(b)
	require.NoError(t, err)
	assert.Equal(t, "x", h.Name)
	assert.Equal(t, uint16(1), h.Protocol)
	assert.Empty(t, h.Version)
	assert.Equal(t, uint32(0), h.Caps)
}

func TestSubscribeRoundTrip(t *testing.T) {
	in := pxb.Subscribe{
		Events:    []uint16{pxb.EvAgentStart, pxb.EvTurnEnd},
		Intercept: []uint16{pxb.EvToolCall},
	}
	out, err := pxb.DecodeSubscribe(pxb.EncodeSubscribe(in))
	require.NoError(t, err)
	assert.Equal(t, in.Events, out.Events)
	assert.Equal(t, in.Intercept, out.Intercept)
}

func TestUnknownEventCodeMapsEmpty(t *testing.T) {
	assert.Empty(t, pxb.EventName(999))
	assert.Equal(t, uint16(0), pxb.EventCode("nope"))
}
