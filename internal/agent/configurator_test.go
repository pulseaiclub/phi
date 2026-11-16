package agent

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestEngineConfiguratorWiredThroughOption(t *testing.T) {
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)

	h := NewEngineConfigurator()
	managed, err := NewEngine(llm.ModelConfig{Name: "m"}, sess, WithConfigurator(h))
	require.NoError(t, err)
	assert.Same(t, h, managed.Configurator(), "engine keeps the configurator it was built with")

	unmanaged, err := NewEngine(llm.ModelConfig{Name: "m"}, sess)
	require.NoError(t, err)
	assert.Nil(t, unmanaged.Configurator(), "no WithConfigurator = unmanaged engine")
}

func TestEngineConfiguratorClaimsOnce(t *testing.T) {
	h := NewEngineConfigurator()
	noop := func(EngineSpec) {}
	require.NoError(t, h.Register(noop))
	require.ErrorIs(t, h.Register(noop), errConfiguratorAlreadyClaimed)
}

func TestEngineConfiguratorSetAppliesCompleteSpec(t *testing.T) {
	h := NewEngineConfigurator()
	var applied EngineSpec
	require.NoError(t, h.Register(func(spec EngineSpec) { applied = spec }))

	h.Set(EngineSpec{Model: llm.ModelConfig{Name: "primary"}})
	assert.Equal(t, "primary", applied.Model.Name)

	assert.Equal(t, "primary", h.Snapshot().Model.Name)
}

// Concurrent Sets must serialize: each Set's apply runs to completion before
// the next starts, so the callback never sees an interleaved partial update.
func TestEngineConfiguratorSerializesConcurrentSets(t *testing.T) {
	h := NewEngineConfigurator()
	var mu sync.Mutex
	var applied atomic.Int64
	active, overlap := 0, false
	require.NoError(t, h.Register(func(EngineSpec) {
		applied.Add(1)
		mu.Lock()
		active++
		if active > 1 {
			overlap = true
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			active--
			mu.Unlock()
		}()
	}))

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			h.Set(EngineSpec{Model: llm.ModelConfig{Name: "m"}})
		})
	}
	wg.Wait()

	assert.False(t, overlap, "concurrent Sets must not interleave")
	assert.Equal(t, int64(32), applied.Load(), "every Set applied exactly once")
}
