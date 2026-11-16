package agent

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

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

	snapshot, version := h.Snapshot()
	assert.Equal(t, "primary", snapshot.Model.Name)
	assert.Equal(t, uint64(1), version)
}

// Concurrent Sets must serialize: each Set's apply runs to completion before
// the next starts, so the callback never sees an interleaved partial update.
func TestEngineConfiguratorSerializesConcurrentSets(t *testing.T) {
	h := NewEngineConfigurator()
	var mu sync.Mutex
	active, overlap := 0, false
	require.NoError(t, h.Register(func(EngineSpec) {
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Set(EngineSpec{Model: llm.ModelConfig{Name: "m"}})
		}()
	}
	wg.Wait()

	assert.False(t, overlap, "concurrent Sets must not interleave")
	_, version := h.Snapshot()
	assert.Equal(t, uint64(32), version)
}
