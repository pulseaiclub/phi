package transcript_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tui/transcript"
)

func TestSubagentStoreProgressAndResult(t *testing.T) {
	s := transcript.NewSubagentStore()
	s.Bind("job1", "parent1")
	s.ApplyProgress(job.Progress{
		JobID:           "job1",
		ParentToolUseID: "parent1",
		ToolUseID:       "c1",
		Name:            "read",
		Status:          "in-progress",
		Detail:          "a.go",
	})
	s.ApplyProgress(job.Progress{
		JobID:           "job1",
		ParentToolUseID: "parent1",
		ToolUseID:       "c1",
		Name:            "read",
		Status:          "done",
		Detail:          "a.go",
	})
	s.ApplyProgress(job.Progress{
		JobID:           "job1",
		ParentToolUseID: "parent1",
		ToolUseID:       "c2",
		Name:            "bash",
		Status:          "done",
		Detail:          "test",
	})

	kids := s.Children("parent1")
	require.Len(t, kids, 2)
	require.Equal(t, status.ToolDone, kids[0].Status)
	require.Equal(t, "read", kids[0].Name)
	byJob := s.ChildrenByJob("job1")
	require.Len(t, byJob, 2)

	s.ApplyResult("parent1", agenttool.ParseAgentResult(`{
		"job_id":"job1","status":"completed","label":"explore: find auth","summary":"## Ok"
	}`))
	// Summary / label are stored; Children unchanged.
	require.Len(t, s.Children("parent1"), 2, "children should not be cleared")
	require.Equal(t, "explore: find auth", s.Label("job1"))
	require.Empty(t, s.Label("job_missing"))
}

// A child keyed by tool_use id and a child keyed by the name+detail fallback
// are separate rows even when their parts spell the same string. A tool_use id
// with a separator inside is not reachable from the model, but it pins key
// injectivity so a later refactor cannot merge the branches back.
func TestSubagentStoreChildKeyKeepsBranchesApart(t *testing.T) {
	s := transcript.NewSubagentStore()
	require.True(t, s.ApplyProgress(job.Progress{
		JobID: "job1", ParentToolUseID: "parent1", ToolUseID: "n\x00d", Name: "n", Detail: "d", Status: "in-progress",
	}), "tool_use child should add a row")
	require.True(t, s.ApplyProgress(job.Progress{
		JobID: "job1", ParentToolUseID: "parent1", Name: "n", Detail: "d", Status: "in-progress",
	}), "fallback child should add a second row")
	require.Len(t, s.Children("parent1"), 2)

	// Same slot + same status is a no-op; a status change updates in place.
	require.False(t, s.ApplyProgress(job.Progress{
		JobID: "job1", ParentToolUseID: "parent1", Name: "n", Detail: "d", Status: "in-progress",
	}), "unchanged fallback child should be a no-op")
	require.True(t, s.ApplyProgress(job.Progress{
		JobID: "job1", ParentToolUseID: "parent1", Name: "n", Detail: "d", Status: "done",
	}), "status change should update in place")
	kids := s.Children("parent1")
	require.Len(t, kids, 2)
	require.Equal(t, status.ToolDone, kids[1].Status)
}
