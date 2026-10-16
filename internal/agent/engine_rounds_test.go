package agent

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestNewEngineRoundBudget(t *testing.T) {
	for _, tt := range []struct {
		name     string
		model    int
		opts     []EngineOption
		expected int
	}{
		{name: "default", expected: 64},
		{name: "negative model", model: -1, expected: 64},
		{name: "model", model: 2, expected: 2},
		{name: "override", model: 2, opts: []EngineOption{WithMaxRounds(3)}, expected: 3},
		{name: "zero override inherits", model: 2, opts: []EngineOption{WithMaxRounds(0)}, expected: 2},
		{name: "negative override inherits", model: 2, opts: []EngineOption{WithMaxRounds(-1)}, expected: 2},
		{name: "negative override defaults", opts: []EngineOption{WithMaxRounds(-1)}, expected: 64},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server, requests := fakeToolSequenceServer(-1)
			defer server.Close()
			var runs atomic.Int32
			sess, err := NewSession(WithCwd(t.TempDir()))
			require.NoError(t, err)
			opts := append([]EngineOption{WithTools([]tooldef.Tool{countingTool(&runs)})}, tt.opts...)
			engine, err := NewEngine(llm.ModelConfig{
				Name: "fake", BaseURL: server.URL, APIKey: "x", MaxRounds: tt.model,
			}, sess, opts...)
			require.NoError(t, err)

			var lastErr error
			for _, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
				if err != nil {
					lastErr = err
					break
				}
			}
			require.ErrorIs(t, lastErr, ErrMaxRounds)
			require.EqualError(t, lastErr, fmt.Sprintf("agent: %s (%d)", ErrMaxRounds, tt.expected))
			require.EqualValues(t, tt.expected, runs.Load())
			require.EqualValues(t, tt.expected+1, requests.Load())
		})
	}
}

func TestSetMaxRoundsNormalizesWithoutChangingModel(t *testing.T) {
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	cfg := llm.ModelConfig{Name: "fake", MaxRounds: 7}
	engine, err := NewEngine(cfg, sess, WithTools([]tooldef.Tool{}))
	require.NoError(t, err)
	for _, tt := range []struct{ value, expected int }{{3, 3}, {0, 64}, {-1, 64}} {
		engine.SetMaxRounds(tt.value)
		require.EqualValues(t, tt.expected, engine.maxRounds.Load())
		require.Equal(t, cfg, engine.modelCfg)
	}
}

func TestLoopConcurrentSetMaxRounds(t *testing.T) {
	server, requests := fakeToolSequenceServer(8)
	defer server.Close()
	var runs atomic.Int32
	engine := newRoundTestEngine(t, server.URL, &runs, 8)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				engine.SetMaxRounds(8)
				engine.SetMaxRounds(0)
				engine.SetMaxRounds(-1)
				runtime.Gosched()
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	for _, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
		require.NoError(t, err)
	}
	require.EqualValues(t, 8, runs.Load())
	require.EqualValues(t, 9, requests.Load())
}

func TestLoopRoundBudgetSnapshotSurvivesSetterDuringPrompt(t *testing.T) {
	server, _ := fakeToolSequenceServer(-1)
	defer server.Close()
	var runs atomic.Int32
	engine := newRoundTestEngine(t, server.URL, &runs, 1)
	asked := 0
	engine.SetContinueAsk(func(_ context.Context, budget int) (bool, error) {
		asked = budget
		engine.SetMaxRounds(9)
		return false, nil
	})

	var lastErr error
	for _, err := range engine.Loop(t.Context(), "go", LoopOpts{}) {
		if err != nil {
			lastErr = err
			break
		}
	}
	require.Equal(t, 1, asked)
	require.ErrorIs(t, lastErr, ErrMaxRounds)
	require.EqualError(t, lastErr, "agent: exceeded maximum tool rounds (1)")
	require.EqualValues(t, 1, runs.Load())
	require.EqualValues(t, 9, engine.maxRounds.Load())
}

func TestEngineRunnerInheritsRoundBudget(t *testing.T) {
	for _, useModelFn := range []bool{false, true} {
		t.Run(fmt.Sprintf("modelFn=%t", useModelFn), func(t *testing.T) {
			server, requests := fakeToolSequenceServer(-1)
			defer server.Close()
			var runs atomic.Int32
			cfg := llm.ModelConfig{Name: "fake", BaseURL: server.URL, APIKey: "x", MaxRounds: 2}
			runner := EngineRunner{
				Model: cfg,
				Gate:  permission.AllowAll{},
				Tools: []tooldef.Tool{countingTool(&runs)},
			}
			if useModelFn {
				runner.Model.MaxRounds = 5
				runner.ModelFn = func(job.Role) llm.ModelConfig { return cfg }
			}
			_, err := runner.Run(t.Context(), job.RunEnv{
				Job: job.Meta{Dir: t.TempDir(), WorkDir: t.TempDir(), Prompt: "go"},
				Log: func(string) {},
			})
			require.ErrorIs(t, err, ErrMaxRounds)
			// Unknown tools are denied by the role gate even with an AllowAll
			// runner constraint; rejected calls still consume the round budget.
			require.Zero(t, runs.Load())
			require.EqualValues(t, 3, requests.Load())
		})
	}
}
