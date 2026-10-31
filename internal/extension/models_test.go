package extension_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/create"
)

const modelExtSrc = `package main

import (
	"context"
	"time"

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("models", "0.0.1")
	m.RegisterModel(ext.Model{
		Name: "echo", Conn: ext.ConnExt,
		ContextWindow: 1000, ImageEnabled: true, ThinkEnabled: true, ThinkLevel: "high",
		Stream: func(ctx context.Context, req ext.ModelRequest, emit func(ext.ModelEvent) error) error {
			if req.SystemPrompt != "sys" {
				return emit(ext.ModelEvent{Content: "bad system prompt"})
			}
			if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
				return emit(ext.ModelEvent{Content: "bad messages"})
			}
			if err := emit(ext.ModelEvent{Reasoning: "thin"}); err != nil {
				return err
			}
			if err := emit(ext.ModelEvent{Content: "hello "}); err != nil {
				return err
			}
			return emit(ext.ModelEvent{
				Content: "world",
				Usage:   &ext.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
			})
		},
	})
	m.RegisterModel(ext.Model{
		Name: "slow", Conn: ext.ConnExt,
		Stream: func(ctx context.Context, req ext.ModelRequest, emit func(ext.ModelEvent) error) error {
			for i := 0; i < 200; i++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
				if err := emit(ext.ModelEvent{Content: "x"}); err != nil {
					return err
				}
			}
			return nil
		},
	})
	// Direct-connect: the host dials BaseURL itself; only registration matters here.
	m.RegisterModel(ext.Model{
		Name: "gw", API: "Anthropic", BaseURL: "http://127.0.0.1:9", APIKey: "k", ContextWindow: 42,
	})
	_ = m.Run()
}
`

func loadModelExtension(t *testing.T) *extension.Runner {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, create.Materialize(t.Context(), root+"/models", "models", "0.0.1", modelExtSrc))
	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warns, "%v", warns)
	t.Cleanup(r.Close)
	return r
}

func collectModelStream(t *testing.T, seq func(yield func(pxb.ModelStreamEvent, error) bool)) []pxb.ModelStreamEvent {
	t.Helper()
	var out []pxb.ModelStreamEvent
	for ev, err := range seq {
		require.NoError(t, err)
		out = append(out, ev)
		if ev.Type == "done" || ev.Type == "error" {
			return out
		}
	}
	return out
}

func TestExtensionModelsRegisterAndStream(t *testing.T) {
	r := loadModelExtension(t)

	routes := r.ExtensionModels()
	require.Len(t, routes, 3)
	byName := map[string]pxb.RegisterModel{}
	for _, route := range routes {
		assert.Equal(t, "models", route.Ext)
		byName[route.Reg.Name] = route.Reg
	}
	echo := byName["echo"]
	assert.Equal(t, pxb.ConnExt, echo.Conn)
	assert.Equal(t, uint32(1000), echo.ContextWindow)
	assert.True(t, echo.ImageEnabled)
	assert.Equal(t, "high", echo.ThinkLevel)
	gw := byName["gw"]
	assert.Equal(t, pxb.ConnDirect, gw.Conn)
	assert.Equal(t, "Anthropic", gw.API)

	route, ok := r.LookupModel("echo")
	require.True(t, ok)

	msgs, err := json.Marshal([]map[string]any{{"role": "user", "content": "hi"}})
	require.NoError(t, err)
	events := collectModelStream(t, func(yield func(pxb.ModelStreamEvent, error) bool) {
		seq, serr := r.ModelStream(t.Context(), route, pxb.ModelInvoke{
			Model: "echo", MessagesJSON: msgs, SystemPrompt: "sys",
		})
		require.NoError(t, serr)
		for ev, eerr := range seq {
			if !yield(ev, eerr) || ev.Type == "done" || ev.Type == "error" {
				return
			}
		}
	})
	require.Len(t, events, 4)
	assert.Equal(t, "delta", events[0].Type)
	assert.Equal(t, "thin", events[0].Reasoning)
	assert.Equal(t, "hello ", events[1].Content)
	assert.Equal(t, "world", events[2].Content)
	var usage struct {
		TotalTokens int `json:"total_tokens"`
	}
	require.NoError(t, json.Unmarshal(events[2].UsageJSON, &usage))
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Equal(t, "done", events[3].Type)
}

// A conversation history past the default 16 MiB frame cap must reach an
// extension that advertised CapLargeFrames, intact, through the real
// subprocess pipe. This is the shape of a long session's ModelInvoke.
func TestExtensionModelLargeHistoryStream(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, create.Materialize(t.Context(), root+"/big", "big", "0.0.1", bigHistoryExtSrc))
	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warns, "%v", warns)
	t.Cleanup(r.Close)

	route, ok := r.LookupModel("big")
	require.True(t, ok)

	// One user message just over MaxPayload, with sentinels at both ends so a
	// truncation or aliasing bug cannot pass a length-only check.
	const fill = pxb.MaxPayload + (1 << 20)
	content := "HEAD" + strings.Repeat("x", fill) + "TAIL"
	msgs, err := json.Marshal([]map[string]any{{"role": "user", "content": content}})
	require.NoError(t, err)

	events := collectModelStream(t, func(yield func(pxb.ModelStreamEvent, error) bool) {
		seq, serr := r.ModelStream(t.Context(), route, pxb.ModelInvoke{
			Model: "big", MessagesJSON: msgs,
		})
		require.NoError(t, serr)
		for ev, eerr := range seq {
			if !yield(ev, eerr) || ev.Type == "done" || ev.Type == "error" {
				return
			}
		}
	})
	require.NotEmpty(t, events)
	assert.Equal(t, "ok", events[0].Content)
}

const bigHistoryExtSrc = `package main

import (
	"context"
	"strings"

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("big", "0.0.1")
	m.RegisterModel(ext.Model{
		Name: "big", Conn: ext.ConnExt,
		Stream: func(ctx context.Context, req ext.ModelRequest, emit func(ext.ModelEvent) error) error {
			if len(req.Messages) != 1 {
				return emit(ext.ModelEvent{Content: "bad message count"})
			}
			c := req.Messages[0].Content
			if !strings.HasPrefix(c, "HEAD") || !strings.HasSuffix(c, "TAIL") {
				return emit(ext.ModelEvent{Content: "truncated payload"})
			}
			return emit(ext.ModelEvent{Content: "ok"})
		},
	})
	_ = m.Run()
}
`

func TestExtensionModelUnknownConnFailsHandshake(t *testing.T) {
	root := t.TempDir()
	// The SDK drops invalid Conn values client-side, so this extension writes
	// the register frame directly — the host must still reject it (the Rust
	// and TS SDKs mirror the wire by hand and could get it wrong).
	src := `package main

import (
	"os"

	"github.com/pulseaiclub/phi/ext/go/pxb"
)

func main() {
	w := pxb.NewWriter(os.Stdout)
	rd := pxb.NewReader(os.Stdin)
	_ = w.Write(pxb.TypeHello, 0, 0, pxb.EncodeHello(pxb.Hello{
		Name: "bogus", Version: "0.0.1", Protocol: pxb.ProtocolVersion,
	}))
	if _, err := rd.Read(); err != nil {
		return
	}
	_ = w.Write(pxb.TypeRegisterModel, 0, 0,
		pxb.EncodeRegisterModel(pxb.RegisterModel{Name: "x", Conn: "bogus"}))
	_ = w.Write(pxb.TypeReady, 0, 0, nil)
	for {
		if _, err := rd.Read(); err != nil {
			return
		}
	}
}
`
	require.NoError(t, create.Materialize(t.Context(), root+"/bogus", "bogus", "0.0.1", src))
	// Load is fail-open: a rejected registration becomes a warning, and the
	// extension is not loaded rather than poisoning the host.
	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0].Message, "unknown conn")
	assert.Empty(t, r.Loaded())
}

func TestExtensionModelCancelKeepsProcessAlive(t *testing.T) {
	r := loadModelExtension(t)
	route, ok := r.LookupModel("slow")
	require.True(t, ok)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	seq, err := r.ModelStream(ctx, route, pxb.ModelInvoke{Model: "slow"})
	require.NoError(t, err)
	count := 0
	for ev, serr := range seq {
		require.NoError(t, serr)
		count++
		if ev.Type == "done" {
			t.Fatal("cancel should end the stream before done")
		}
	}
	assert.Positive(t, count, "some deltas should arrive before the cancel lands")
	assert.Less(t, count, 200, "stream should end well before the handler finishes")

	// The extension survived the cancel: a fresh invoke still streams.
	echoRoute, ok := r.LookupModel("echo")
	require.True(t, ok)
	msgs, err := json.Marshal([]map[string]any{{"role": "user", "content": "hi"}})
	require.NoError(t, err)
	events := collectModelStream(t, func(yield func(pxb.ModelStreamEvent, error) bool) {
		seq, serr := r.ModelStream(t.Context(), echoRoute, pxb.ModelInvoke{
			Model: "echo", MessagesJSON: msgs, SystemPrompt: "sys",
		})
		require.NoError(t, serr)
		for ev, eerr := range seq {
			if !yield(ev, eerr) || ev.Type == "done" || ev.Type == "error" {
				return
			}
		}
	})
	require.NotEmpty(t, events)
	assert.Equal(t, "done", events[len(events)-1].Type)
}
