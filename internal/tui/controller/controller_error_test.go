package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestFinishLoopClassifiesCancellationAndProviderFailure(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		err       error
		wantText  string
		cancelled bool
	}{
		{
			name:      "wrapped cancellation",
			ctx:       t.Context(),
			err:       fmt.Errorf("request stopped: %w", context.Canceled),
			cancelled: true,
		},
		{
			name:      "cancelled context",
			ctx:       cancelledContext(t),
			err:       context.Canceled,
			cancelled: true,
		},
		{
			name:     "provider deadline",
			ctx:      t.Context(),
			err:      fmt.Errorf("provider request: %w", context.DeadlineExceeded),
			wantText: "provider request: context deadline exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := NewBus(nil)
			ctrl := &EngineController{bus: bus, streamGen: 7}

			ctrl.finishLoop(tt.ctx, 7, tt.err)

			batch := bus.Drain()
			require.Len(t, batch, 1)
			finished, ok := batch[0].(LoopFinishedMsg)
			require.True(t, ok)
			assert.Equal(t, 7, finished.Gen)
			assert.Equal(t, tt.wantText, finished.ErrorText)
			assert.Equal(t, tt.cancelled, finished.Cancelled)
			assert.Equal(t, tt.wantText != "", retryReady(ctrl))
		})
	}
}

func TestFinishLoopDropsStaleGeneration(t *testing.T) {
	bus := NewBus(nil)
	ctrl := &EngineController{bus: bus, streamGen: 2}

	ctrl.finishLoop(t.Context(), 1, errors.New("stale provider failure"))

	assert.Empty(t, bus.Drain())
	assert.True(t, ctrl.CurrentLoop(2))
	assert.False(t, ctrl.CurrentLoop(1))
}

func TestCancelInvalidatesQueuedLoopResult(t *testing.T) {
	bus := NewBus(nil)
	ctrl := &EngineController{bus: bus, streamGen: 4}

	ctrl.finishLoop(t.Context(), 4, errors.New("provider failure"))
	ctrl.Cancel()

	batch := bus.Drain()
	require.Len(t, batch, 1)
	finished, ok := batch[0].(LoopFinishedMsg)
	require.True(t, ok)
	assert.Equal(t, 4, finished.Gen)
	assert.False(t, ctrl.CurrentLoop(finished.Gen), "Cancel must invalidate a queued completion")
	assert.False(t, retryReady(ctrl))
}

func TestRetryLastPromptResumesWithoutDuplicateUserMessage(t *testing.T) {
	var requests chan struct{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		http.Error(w, "provider unavailable", http.StatusUnauthorized)
	}))
	defer server.Close()

	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, server.URL))
	_ = ctrl.bus.Drain()
	requests = make(chan struct{}, 4)

	ctrl.StartPrompt("keep this once", nil, nil, nil)
	first := waitLoopFinished(t, ctrl.bus, 1)
	require.NotEmpty(t, first.ErrorText)
	assert.False(t, first.Cancelled)
	assert.True(t, retryReady(ctrl))
	assert.Len(t, userMessages(ctrl), 1)

	require.True(t, ctrl.RetryLastPrompt())
	assert.False(t, retryReady(ctrl), "starting a retry must consume retryReady")
	second := waitLoopFinished(t, ctrl.bus, 2)
	require.NotEmpty(t, second.ErrorText)
	assert.False(t, second.Cancelled)
	assert.Len(t, userMessages(ctrl), 1, "a resumed retry must not append the prompt again")
	assert.True(t, retryReady(ctrl))

	ctrl.Cancel()
	assert.False(t, retryReady(ctrl))
	assert.False(t, ctrl.RetryLastPrompt(), "a cancelled retry must not be retriable")
}

func TestCancelInFlightPromptPublishesSilentCompletion(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(requestStarted) })
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(requestCancelled)
	}))
	defer server.Close()

	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, server.URL))
	_ = ctrl.bus.Drain()
	ctrl.StartPrompt("cancel this", nil, nil, nil)

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "provider request did not start")
	}

	ctrl.Cancel()
	select {
	case <-requestCancelled:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "provider request was not cancelled")
	}
	assert.False(t, ctrl.CurrentLoop(1), "manual cancellation must invalidate the request generation")
	assert.False(t, retryReady(ctrl))
	assert.False(t, ctrl.RetryLastPrompt(), "a manually cancelled request must not be retriable")
	for _, msg := range ctrl.bus.Drain() {
		finished, ok := msg.(LoopFinishedMsg)
		if ok {
			assert.NotEqual(t, 1, finished.Gen, "cancelled request must not publish a current completion")
		}
	}
}

func cancelledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

func waitLoopFinished(t *testing.T, bus *Bus, gen int) LoopFinishedMsg {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-bus.Chan():
			for _, msg := range bus.Drain() {
				finished, ok := msg.(LoopFinishedMsg)
				if ok && finished.Gen == gen {
					return finished
				}
			}
		case <-timer.C:
			require.FailNowf(t, "loop completion timeout", "timed out waiting for loop %d completion", gen)
		}
	}
}

func retryReady(ctrl *EngineController) bool {
	ctrl.streamMu.Lock()
	defer ctrl.streamMu.Unlock()
	return ctrl.retryReady
}

func userMessages(ctrl *EngineController) []llm.Message {
	var users []llm.Message
	for _, entry := range ctrl.engine.Session().PathEntries() {
		message, ok := entry.(session.SessionMessageEntry)
		if ok && message.Message.Role == llm.RoleUser {
			users = append(users, message.Message)
		}
	}
	return users
}
