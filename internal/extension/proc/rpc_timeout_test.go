package proc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/ext/go/pxb"
)

func TestRPCWaitDurationDefault(t *testing.T) {
	d := rpcWaitDuration(t.Context(), 0)
	assert.Equal(t, rpcTimeout, d)
}

func TestRPCWaitDurationOverride(t *testing.T) {
	d := rpcWaitDuration(t.Context(), 120*time.Second)
	assert.Equal(t, 120*time.Second, d)
}

func TestRPCWaitDurationClampsMax(t *testing.T) {
	d := rpcWaitDuration(t.Context(), 10_000*time.Second)
	assert.Equal(t, rpcTimeoutMax, d)
}

func TestRPCWaitDurationOverrideCappedByCtx(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	d := rpcWaitDuration(ctx, 120*time.Second)
	assert.Less(t, d, 45*time.Second)
	assert.Greater(t, d, 30*time.Second)
}

func TestRPCWaitDurationLegacyCtxReplacesDefault(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	d := rpcWaitDuration(ctx, 0)
	assert.Greater(t, d, 80*time.Second)
	assert.LessOrEqual(t, d, 90*time.Second)
}

func TestToolRPCTimeoutLookup(t *testing.T) {
	p := &Proc{tools: []pxb.RegisterTool{
		{Name: "fetch", TimeoutSec: 180},
		{Name: "ping"},
	}}
	assert.Equal(t, 180*time.Second, p.toolRPCTimeout("fetch"))
	assert.Equal(t, time.Duration(0), p.toolRPCTimeout("ping"))
	assert.Equal(t, time.Duration(0), p.toolRPCTimeout("missing"))
}
