package session

import (
	"testing"

	"github.com/pulseaiclub/phi/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBashContextText(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		output    string
		exitCode  int
		cancelled bool
		want      string
	}{
		{
			name:    "output",
			command: "git status",
			output:  "clean\n",
			want:    "Ran `git status`\n```\nclean\n```",
		},
		{
			name:    "no output",
			command: "true",
			want:    "Ran `true`\n(no output)",
		},
		{
			name:     "exit code",
			command:  "false",
			exitCode: 1,
			want:     "Ran `false`\n(no output)\n\nCommand exited with code 1",
		},
		{
			name:      "cancelled",
			command:   "sleep 9",
			cancelled: true,
			want:      "Ran `sleep 9`\n(no output)\n\n(command cancelled)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BashContextText(tt.command, tt.output, tt.exitCode, tt.cancelled))
		})
	}
}

func TestAppendBashAddsUserContextAndReplaysAsBashRow(t *testing.T) {
	m := NewManager(t.TempDir())

	id, err := m.AppendBash("git status", "clean\n", 0, false)
	require.NoError(t, err)

	entries := m.BuildContext()
	require.Len(t, entries, 1)
	entry := entries[0].(SessionMessageEntry)
	assert.Equal(t, id, entry.GetID())
	assert.Equal(t, llm.RoleUser, entry.Message.Role, "the model sees a user message")
	assert.Equal(t, "Ran `git status`\n```\nclean\n```", entry.Message.Content)
	require.NotNil(t, entry.Bash)

	snap := ReplaySnapshot(entries, nil)
	require.Len(t, snap.Messages, 1)
	assert.Equal(t, RoleLocalBash, snap.Messages[0].Role, "the transcript keeps the bash row")
	assert.Equal(t, "git status", snap.Messages[0].Text)

	run := snap.Tools[id]
	assert.True(t, run.Local)
	assert.True(t, run.InContext)
	assert.Equal(t, ToolDone, run.Status)
	assert.Equal(t, "clean\n", run.Output)
}

func TestAppendBashReplayStatus(t *testing.T) {
	t.Run("exit code", func(t *testing.T) {
		m := NewManager(t.TempDir())
		id, err := m.AppendBash("false", "", 1, false)
		require.NoError(t, err)

		snap := ReplaySnapshot(m.BuildContext(), nil)
		assert.Equal(t, ToolError, snap.Tools[id].Status)
		assert.Equal(t, 1, snap.Tools[id].ExitCode)
	})

	t.Run("cancelled", func(t *testing.T) {
		m := NewManager(t.TempDir())
		id, err := m.AppendBash("sleep 9", "", 0, true)
		require.NoError(t, err)

		snap := ReplaySnapshot(m.BuildContext(), nil)
		assert.Equal(t, ToolCancelled, snap.Tools[id].Status)
	})
}

func TestAppendBashSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.AppendBash("echo hi", "hi\n", 0, false)
	require.NoError(t, err)
	// A session with no assistant turn has not been flushed yet; force the
	// write so the reload path sees the recorded bash entry.
	require.NoError(t, m.flushAllEntries())

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)

	snap := ReplaySnapshot(loaded.BuildContext(), nil)
	require.Len(t, snap.Messages, 1)
	assert.Equal(t, RoleLocalBash, snap.Messages[0].Role)
	assert.Equal(t, "echo hi", snap.Messages[0].Text)
	assert.True(t, snap.Tools[snap.Messages[0].ID].InContext)
	assert.Equal(t, "hi\n", snap.Tools[snap.Messages[0].ID].Output)
}
