package pxb_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
)

// TestWriteGoldenFixtures regenerates ext/go/pxb/testdata/*.bin for TS interop.
// Run with UPDATE_GOLDEN=1 (or pass -update) to rewrite fixtures.
func TestWriteGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" && !updateFlag() {
		t.Skip("set UPDATE_GOLDEN=1 or -update to regenerate")
	}
	dir := "testdata"
	require.NoError(t, os.MkdirAll(dir, 0o755))

	write := func(name string, body []byte) {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, body, 0o644))
	}

	hello := pxb.EncodeHello(pxb.Hello{
		Name: "greet", Version: "1.0.0", Caps: pxb.CapTools | pxb.CapCommands, Protocol: 1,
	})
	write("hello.bin", hello)

	ack := pxb.EncodeHelloAck(pxb.HelloAck{
		Protocol: 1, PhiVersion: "v0.19.0", Cwd: "/tmp", SessionID: "s1", ExtensionDir: "/ext",
	})
	write("hello_ack.bin", ack)

	ix := pxb.EncodeInterceptReq(pxb.InterceptReq{
		Event: pxb.EvToolCall, ToolName: "bash", ToolCallID: "c1",
		Input: []byte(`{"command":"ls"}`),
	})
	write("intercept_req.bin", ix)

	sub := pxb.EncodeSubscribe(pxb.Subscribe{
		Events: []uint16{pxb.EvSessionStart, pxb.EvAgentEnd}, Intercept: []uint16{pxb.EvToolCall},
	})
	write("subscribe.bin", sub)

	reg := pxb.EncodeRegisterModel(pxb.RegisterModel{
		Name: "echo", API: "Anthropic", BaseURL: "http://127.0.0.1:9", APIKey: "k",
		Preset: "glm-5.3-flash", ContextWindow: 1000, ImageEnabled: true,
		ThinkEnabled: true, ThinkLevel: "high", Conn: pxb.ConnExt,
	})
	write("register_model.bin", reg)

	inv := pxb.EncodeModelInvoke(pxb.ModelInvoke{
		Model: "echo", MessagesJSON: []byte(`[{"role":"user","content":"hi"}]`),
		ToolsJSON: []byte(`[{"name":"t"}]`), SystemPrompt: "sys", Think: "high",
	})
	write("model_invoke.bin", inv)

	stream := pxb.EncodeModelStreamEvent(pxb.ModelStreamEvent{
		Type: "delta", Content: "c", Reasoning: "r",
		ToolCalls: []byte(`[{"index":0,"id":"c1"}]`), UsageJSON: []byte(`{"total_tokens":3}`),
	})
	write("model_stream.bin", stream)

	frame := append([]byte(nil), make([]byte, pxb.HeaderSize)...)
	pxb.EncodeHeader(frame, pxb.Header{Type: pxb.TypeHello, Flags: 0, ID: 0, Payload: uint32(len(hello))})
	frame = append(frame[:pxb.HeaderSize], hello...)
	write("hello_frame.bin", frame)
}

func updateFlag() bool {
	return slices.Contains(os.Args, "-update")
}

func TestGoldenFixturesDecode(t *testing.T) {
	dir := "testdata"
	helloRaw, err := os.ReadFile(filepath.Join(dir, "hello.bin"))
	if err != nil {
		t.Skip("testdata missing; run UPDATE_GOLDEN=1 go test ./pxb -run TestWriteGoldenFixtures")
	}
	h, err := pxb.DecodeHello(helloRaw)
	require.NoError(t, err)
	require.Equal(t, "greet", h.Name)
	require.Equal(t, pxb.CapTools|pxb.CapCommands, h.Caps)

	ixRaw, err := os.ReadFile(filepath.Join(dir, "intercept_req.bin"))
	require.NoError(t, err)
	ix, err := pxb.DecodeInterceptReq(ixRaw)
	require.NoError(t, err)
	require.Equal(t, "bash", ix.ToolName)
	require.JSONEq(t, `{"command":"ls"}`, string(ix.Input))

	regRaw, err := os.ReadFile(filepath.Join(dir, "register_model.bin"))
	require.NoError(t, err)
	reg, err := pxb.DecodeRegisterModel(regRaw)
	require.NoError(t, err)
	require.Equal(t, "echo", reg.Name)
	require.Equal(t, pxb.ConnExt, reg.Conn)
	require.Equal(t, uint32(1000), reg.ContextWindow)
}
