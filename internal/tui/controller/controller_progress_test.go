package controller

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
)

func TestShouldPublishJobProgressDedup(t *testing.T) {
	c := &EngineController{}
	p := job.Progress{
		JobID:     "j",
		ToolUseID: "t1",
		Name:      "read",
		Status:    "in-progress",
		Detail:    "a.go",
	}
	require.True(t, c.shouldPublishJobProgress(p), "first should publish")
	require.False(t, c.shouldPublishJobProgress(p), "duplicate should drop")
	p.Status = "done"
	require.True(t, c.shouldPublishJobProgress(p), "status change should publish")
	p2 := job.Progress{
		JobID:     "j",
		ToolUseID: "t2",
		Name:      "bash",
		Status:    "done",
		Detail:    "ls",
	}
	require.True(t, c.shouldPublishJobProgress(p2), "new child should publish")
}

// The tool_use slot and the name+detail fallback slot are distinct rows even
// when their parts spell the same string. A tool_use id with a separator inside
// is not reachable from the model, but it pins key injectivity so a later
// refactor cannot merge the branches back into one joined key.
func TestShouldPublishJobProgressKeyKeepsBranchesApart(t *testing.T) {
	c := &EngineController{}
	require.True(t, c.shouldPublishJobProgress(job.Progress{
		JobID: "a", ToolUseID: "n\x00d", Name: "n", Detail: "d", Status: "done",
	}), "tool_use slot should publish")
	require.True(t, c.shouldPublishJobProgress(job.Progress{
		JobID: "a", Name: "n", Detail: "d", Status: "done",
	}), "fallback slot should not dedupe against the tool_use slot")
	require.False(t, c.shouldPublishJobProgress(job.Progress{
		JobID: "a", Name: "n", Detail: "d", Status: "done",
	}), "duplicate fallback slot should still drop")
}
