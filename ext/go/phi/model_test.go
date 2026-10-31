package phi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

// lockedBuffer lets the model-stream goroutine and the test read frames
// concurrently; the SDK serializes writes, the test only needs a read barrier.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) frames(t *testing.T) []pxb.Frame {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var frames []pxb.Frame
	for b.buf.Len() > 0 {
		f, err := pxb.ReadFrame(&b.buf)
		require.NoError(t, err)
		frames = append(frames, f)
	}
	return frames
}

func TestRegisterModelHandshakeAndInvoke(t *testing.T) {
	e := New("models", "1")
	handlerDone := make(chan struct{})
	e.RegisterModel(ext.Model{
		Name: "echo", Conn: ext.ConnExt, ContextWindow: 99, ImageEnabled: true, ThinkEnabled: true,
		Stream: func(_ context.Context, req ext.ModelRequest, emit func(ext.ModelEvent) error) error {
			if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
				return errors.New("bad messages")
			}
			if req.SystemPrompt != "sys" {
				return errors.New("bad system prompt")
			}
			if err := emit(ext.ModelEvent{Content: "he"}); err != nil {
				return err
			}
			if err := emit(ext.ModelEvent{
				Content: "y",
				Usage:   &ext.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
			}); err != nil {
				return err
			}
			close(handlerDone)
			return nil
		},
	})
	e.RegisterModel(ext.Model{
		Name: "gw", BaseURL: "http://127.0.0.1:9", APIKey: "k", ContextWindow: 42,
	})
	// The stream runs on its own goroutine; park the read loop on this event
	// until the handler finished so its emits precede shutdown.
	e.SubscribeEvent(ext.EventTurnStart, func(_ pxb.EventNotify) {
		<-handlerDone
	})

	msgs, err := json.Marshal([]ext.ChatMessage{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	var in, out lockedBuffer
	inputFrame(t, &in.buf, pxb.TypeHelloAck, 0, pxb.EncodeHelloAck(pxb.HelloAck{}))
	inputFrame(t, &in.buf, pxb.TypeModelInvoke, 7, pxb.EncodeModelInvoke(pxb.ModelInvoke{
		Model: "echo", MessagesJSON: msgs, SystemPrompt: "sys", Think: "high",
	}))
	inputFrame(t, &in.buf, pxb.TypeEvent, 0, pxb.EncodeEventNotify(pxb.EventNotify{Event: pxb.EvTurnStart}))
	inputFrame(t, &in.buf, pxb.TypeShutdown, 0, nil)
	require.NoError(t, e.run(&in.buf, &out))

	// The handler ran off the read loop; wait for it before asserting frames.
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("model handler never finished")
	}

	var registrations []pxb.RegisterModel
	var stream []pxb.ModelStreamEvent
	var streamIDs []uint32
	for _, f := range out.frames(t) {
		switch f.Type {
		case pxb.TypeRegisterModel:
			reg, err := pxb.DecodeRegisterModel(f.Body)
			require.NoError(t, err)
			registrations = append(registrations, reg)
		case pxb.TypeModelStream:
			ev, err := pxb.DecodeModelStreamEvent(f.Body)
			require.NoError(t, err)
			stream = append(stream, ev)
			streamIDs = append(streamIDs, f.ID)
		}
	}
	require.Len(t, registrations, 2)
	byName := map[string]pxb.RegisterModel{}
	for _, r := range registrations {
		byName[r.Name] = r
	}
	assert.Equal(t, pxb.ConnExt, byName["echo"].Conn)
	assert.Equal(t, uint32(99), byName["echo"].ContextWindow)
	assert.Equal(t, "http://127.0.0.1:9", byName["gw"].BaseURL)
	assert.Equal(t, uint32(42), byName["gw"].ContextWindow)

	require.Len(t, stream, 3)
	assert.Equal(t, []uint32{7, 7, 7}, streamIDs)
	assert.Equal(t, "delta", stream[0].Type)
	assert.Equal(t, "he", stream[0].Content)
	assert.Equal(t, "y", stream[1].Content)
	var usage ext.Usage
	require.NoError(t, json.Unmarshal(stream[1].UsageJSON, &usage))
	assert.Equal(t, 3, usage.TotalTokens)
	assert.Equal(t, "done", stream[2].Type)
}

func TestModelInvokeUnknownModelErrorFrame(t *testing.T) {
	e := New("models", "1")
	var in, out lockedBuffer
	inputFrame(t, &in.buf, pxb.TypeHelloAck, 0, pxb.EncodeHelloAck(pxb.HelloAck{}))
	inputFrame(t, &in.buf, pxb.TypeModelInvoke, 9, pxb.EncodeModelInvoke(pxb.ModelInvoke{Model: "ghost"}))
	inputFrame(t, &in.buf, pxb.TypeShutdown, 0, nil)
	require.NoError(t, e.run(&in.buf, &out))

	var stream []pxb.ModelStreamEvent
	for _, f := range out.frames(t) {
		if f.Type == pxb.TypeModelStream {
			ev, err := pxb.DecodeModelStreamEvent(f.Body)
			require.NoError(t, err)
			stream = append(stream, ev)
		}
	}
	require.Len(t, stream, 1)
	assert.Equal(t, "error", stream[0].Type)
	assert.Contains(t, stream[0].Error, "unknown model ghost")
}

func TestModelCancelReachesHandler(t *testing.T) {
	e := New("models", "1")
	cancelled := make(chan struct{})
	e.RegisterModel(ext.Model{
		Name: "slow", Conn: ext.ConnExt,
		Stream: func(ctx context.Context, _ ext.ModelRequest, _ func(ext.ModelEvent) error) error {
			<-ctx.Done()
			close(cancelled)
			return ctx.Err()
		},
	})
	var in, out lockedBuffer
	inputFrame(t, &in.buf, pxb.TypeHelloAck, 0, pxb.EncodeHelloAck(pxb.HelloAck{}))
	inputFrame(t, &in.buf, pxb.TypeModelInvoke, 11, pxb.EncodeModelInvoke(pxb.ModelInvoke{Model: "slow"}))
	// Give the goroutine time to start, then cancel it.
	inputFrame(t, &in.buf, pxb.TypeModelCancel, 11, nil)
	inputFrame(t, &in.buf, pxb.TypeShutdown, 0, nil)
	require.NoError(t, e.run(&in.buf, &out))

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel never reached the handler context")
	}
}
