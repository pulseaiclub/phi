package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/session"
)

func TestActivityConstructorCallback(t *testing.T) {
	var changes int
	h := NewActivityHandler(WithOnChange(func() { changes++ }))
	assert.Zero(t, changes, "construction must not emit an activity event")
	h.Apply(ActivityStreaming)
	h.SyncFromSnap(session.Snapshot{})
	assert.Equal(t, 2, changes)
	assert.Equal(t, ActivityIdle, h.Current)
}
