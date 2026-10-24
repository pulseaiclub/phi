package pxb_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
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

// A payload above the default 16 MiB cap must pass once the peer negotiated
// large frames — the ModelInvoke history is exactly this shape.
func TestNegotiatedLargePayload(t *testing.T) {
	body := make([]byte, pxb.MaxPayload+(1<<20))
	for i := range body {
		body[i] = byte(i)
	}

	pr, pw := net.Pipe()
	defer pr.Close()
	go func() {
		defer pw.Close()
		wr := pxb.NewWriter(pw)
		wr.SetMaxPayload(pxb.MaxStreamPayload)
		// The read side asserts byte-exactness, so write failures surface there.
		_ = wr.Write(pxb.TypeModelInvoke, pxb.FlagHasID, 7, body)
		_ = wr.Write(pxb.TypeReady, 0, 0, nil)
	}()

	rd := pxb.NewReader(pr)
	f, err := rd.Read()
	require.NoError(t, err)
	assert.Equal(t, pxb.TypeModelInvoke, f.Type)
	assert.Equal(t, pxb.FlagHasID, f.Flags)
	assert.Equal(t, uint32(7), f.ID)
	require.Len(t, f.Body, len(body))
	assert.True(t, bytes.Equal(body, f.Body), "large payload must round-trip byte-exact")

	small, err := rd.Read()
	require.NoError(t, err)
	assert.Equal(t, pxb.TypeReady, small.Type)
}

// The cap is per-connection policy: a tightened cap rejects what the default
// would allow.
func TestWriterNegotiatedCapIsConnectionLocal(t *testing.T) {
	wr := pxb.NewWriter(io.Discard)
	wr.SetMaxPayload(1 << 20)
	err := wr.Write(pxb.TypeModelInvoke, 0, 0, make([]byte, (3<<20)/2))
	assert.ErrorIs(t, err, pxb.ErrPayloadLarge)
}

func TestWriterDefaultCapRejectsOversize(t *testing.T) {
	wr := pxb.NewWriter(io.Discard)
	body := make([]byte, pxb.MaxPayload+1)
	err := wr.Write(pxb.TypeModelInvoke, 0, 0, body)
	assert.ErrorIs(t, err, pxb.ErrPayloadLarge)
}

func TestDecodeHeaderPayloadBounds(t *testing.T) {
	mk := func(n uint32) []byte {
		var hdr [pxb.HeaderSize]byte
		pxb.EncodeHeader(hdr[:], pxb.Header{Type: 1, Payload: n})
		return hdr[:]
	}
	_, err := pxb.DecodeHeader(mk(pxb.MaxStreamPayload))
	assert.NoError(t, err, "exactly MaxStreamPayload is receivable")
	_, err = pxb.DecodeHeader(mk(pxb.MaxStreamPayload + 1))
	assert.ErrorIs(t, err, pxb.ErrPayloadLarge)
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
