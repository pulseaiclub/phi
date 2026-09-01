package pxb_test

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/pxb"
)

func TestFrameRoundTrip(t *testing.T) {
	body := pxb.EncodeHello(pxb.Hello{
		Name: "greet", Version: "1.0.0", Caps: pxb.CapTools | pxb.CapCommands, Protocol: 1,
	})
	var buf bytes.Buffer
	require.NoError(t, pxb.WriteFrame(&buf, pxb.TypeHello, 0, 0, body))

	f, err := pxb.ReadFrame(&buf)
	require.NoError(t, err)
	assert.Equal(t, pxb.TypeHello, f.Type)
	h, err := pxb.DecodeHello(f.Body)
	require.NoError(t, err)
	assert.Equal(t, "greet", h.Name)
	assert.Equal(t, pxb.CapTools|pxb.CapCommands, h.Caps)
}

func TestReaderReusesBuffer(t *testing.T) {
	var buf bytes.Buffer
	for range 3 {
		require.NoError(t, pxb.WriteFrame(&buf, pxb.TypeReady, 0, 0, nil))
	}
	rd := pxb.NewReader(&buf)
	for range 3 {
		f, err := rd.Read()
		require.NoError(t, err)
		assert.Equal(t, pxb.TypeReady, f.Type)
	}
	_, err := rd.Read()
	assert.ErrorIs(t, err, io.EOF)
}

func TestInterceptRoundTrip(t *testing.T) {
	in := pxb.InterceptReq{
		Event:      pxb.EvToolCall,
		ToolName:   "bash",
		ToolCallID: "c1",
		Input:      []byte(`{"command":"ls"}`),
	}
	b := pxb.EncodeInterceptReq(in)
	out, err := pxb.DecodeInterceptReq(b)
	require.NoError(t, err)
	assert.Equal(t, in.ToolName, out.ToolName)
	assert.Equal(t, in.Input, out.Input)
}

func BenchmarkPXBHello(b *testing.B) {
	body := pxb.EncodeHello(pxb.Hello{Name: "x", Version: "1", Caps: 7, Protocol: 1})
	var buf bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		_ = pxb.WriteFrame(&buf, pxb.TypeHello, 0, 0, body)
		_, _ = pxb.ReadFrame(&buf)
	}
}

func BenchmarkJSONLHello(b *testing.B) {
	type hello struct {
		Type     string   `json:"type"`
		Name     string   `json:"name"`
		Version  string   `json:"version"`
		Caps     []string `json:"capabilities"`
		Protocol int      `json:"protocol_version"`
	}
	msg := hello{Type: "hello", Name: "x", Version: "1", Caps: []string{"tools", "commands", "events"}, Protocol: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		raw, _ := json.Marshal(msg)
		raw = append(raw, '\n')
		var out hello
		_ = json.Unmarshal(raw[:len(raw)-1], &out)
	}
}
