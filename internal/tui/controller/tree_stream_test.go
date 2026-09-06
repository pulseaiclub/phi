package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools"
)

func receiveTreeResult(t *testing.T, c *EngineController) TreeResultMsg {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		for _, msg := range c.bus.Drain() {
			if result, ok := msg.(TreeResultMsg); ok {
				return result
			}
		}
		select {
		case <-c.bus.Chan():
		case <-timeout.C:
			require.FailNow(t, "tree navigation did not finish")
		}
	}
}

func TestTreeNavigationWaitsForCancelledToolToActuallyExit(t *testing.T) {
	started, cancelled, exit := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(
			w,
			"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"slow\",\"arguments\":\"{}\"}}]}}]}\n\ndata: [DONE]\n\n",
		)
	}))
	defer server.Close()
	s, err := agent.NewSession()
	require.NoError(t, err)
	require.NoError(t, s.AddUser("base question"))
	u := s.LastID()
	require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: "base answer"}))
	engine, err := agent.NewEngine(llm.ModelConfig{Name: "test", BaseURL: server.URL, APIKey: "key"}, s,
		agent.WithGate(permission.AllowAll{}), agent.WithTools([]tools.Tool{{
			Definition: llm.ToolDefinition{Name: "slow", Params: &llm.FunctionParameters{Type: "object"}},
			Run: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
				close(started)
				<-ctx.Done()
				close(cancelled)
				<-exit
				return tools.Result{Content: "cancelled tool result"}, ctx.Err()
			},
		}}))
	require.NoError(t, err)
	c := &EngineController{engine: engine, bus: NewBus(nil)}
	c.StartPrompt("run slow tool", nil, nil)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		require.FailNow(t, "tool did not start")
	}
	old := c.TreeSnapshot().LeafID
	require.True(t, c.StartTreeNavigation(u, agent.TreeOptions{}, nil))
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		require.FailNow(t, "tool was not cancelled")
	}
	assert.True(t, c.TreeBusy())
	assert.Equal(t, old, c.TreeSnapshot().LeafID)
	close(exit)
	result := receiveTreeResult(t, c)
	require.NoError(t, result.Err)
	assert.Empty(t, c.TreeSnapshot().LeafID)
	assert.Empty(t, result.Snapshot.Messages)
	assert.False(t, c.Running())
	assert.False(t, c.Alive(1))
	c.FinishTreeNavigation()
}
