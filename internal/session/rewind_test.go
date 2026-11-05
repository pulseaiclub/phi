package session

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestRewindPersistsAndBranches(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithShouldFlush(true), WithSessionDir(dir))
	require.NoError(t, err)
	first, err := m.Append(llm.Message{Role: llm.RoleUser, Content: "first"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer"})
	require.NoError(t, err)
	second, err := m.Append(llm.Message{Role: llm.RoleUser, Content: "second"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "wrong answer"})
	require.NoError(t, err)
	text, err := m.RewindTo(second)
	require.NoError(t, err)
	assert.Equal(t, "second", text)
	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, m.LeafID(), loaded.LeafID())
	require.Len(t, loaded.UserTurns(), 1)
	assert.Equal(t, first, loaded.UserTurns()[0].ID)
	list, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "first", list[0].Preview)
	_, err = loaded.RewindTo(second)
	require.ErrorContains(t, err, "current branch")
	retry, err := loaded.Append(llm.Message{Role: llm.RoleUser, Content: "retry"})
	require.NoError(t, err)
	loaded, err = OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, retry, loaded.LeafID())
	list, err = ListSessions(dir)
	require.NoError(t, err)
	assert.Equal(t, "retry", list[0].Preview)
	require.Len(t, loaded.UserTurns(), 2)
	_, err = loaded.RewindTo(first)
	require.NoError(t, err)
	loaded, err = OpenSession(m.File())
	require.NoError(t, err)
	assert.Empty(t, loaded.LeafID())
	assert.Empty(t, loaded.BuildContext())
	list, err = ListSessions(dir)
	require.NoError(t, err)
	assert.Empty(t, list[0].Preview)
}

func TestRewindOriginalInputSurvivesReload(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"original", "  user text\n"},
		{"empty", ""},
		{"legacy", "processed text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			m, err := NewSessionManager(dir, WithShouldFlush(true), WithSessionDir(dir))
			require.NoError(t, err)
			msg := llm.Message{Role: llm.RoleUser, Content: "processed text"}
			var user string
			if tc.name == "legacy" {
				user, err = m.Append(msg)
			} else {
				user, err = m.AppendUser(msg, tc.want)
			}
			require.NoError(t, err)
			_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "reply"})
			require.NoError(t, err)
			loaded, err := OpenSession(m.File())
			require.NoError(t, err)
			text, err := loaded.RewindTo(user)
			require.NoError(t, err)
			assert.Equal(t, tc.want, text)
		})
	}
}

func TestRewindPreservesCurrentModelAcrossResume(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithShouldFlush(true), WithSessionDir(dir), WithModel("model-a"))
	require.NoError(t, err)
	user, err := m.Append(llm.Message{Role: llm.RoleUser, Content: "prompt"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "reply"})
	require.NoError(t, err)
	require.NoError(t, m.SetModel("model-b"))
	_, err = m.RewindTo(user)
	require.NoError(t, err)
	assert.Equal(t, "model-b", m.ModelName())
	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, "model-b", loaded.ModelName())
}

func TestRewindBeforeCompactionAndRejectsNonUser(t *testing.T) {
	m := NewManager(t.TempDir())
	user, err := m.Append(llm.Message{Role: llm.RoleUser, Content: "before compaction"})
	require.NoError(t, err)
	assistant, err := m.Append(llm.Message{Role: llm.RoleAssistant, Content: "reply"})
	require.NoError(t, err)
	bash, err := m.AppendBash("pwd", "cwd", 0, false)
	require.NoError(t, err)
	compact, err := m.AppendCompaction(Compaction{Summary: "summary", FirstKeptEntryID: assistant})
	require.NoError(t, err)
	require.Len(t, m.UserTurns(), 1)
	leaf := m.LeafID()
	for _, id := range []string{assistant, bash, compact, "missing"} {
		require.ErrorContains(t, m.CanRewindTo(id), "user message")
		_, err := m.RewindTo(id)
		require.ErrorContains(t, err, "user message")
		assert.Equal(t, leaf, m.LeafID())
	}
	require.NoError(t, m.CanRewindTo(user))
	assert.Equal(t, leaf, m.LeafID())
	_, err = m.RewindTo(user)
	require.NoError(t, err)
	assert.Empty(t, m.BuildContext())
}

func TestRewindPersistenceFailureKeepsCursor(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithShouldFlush(true), WithSessionDir(dir))
	require.NoError(t, err)
	user, err := m.Append(llm.Message{Role: llm.RoleUser, Content: "prompt"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "reply"})
	require.NoError(t, err)
	leaf := m.LeafID()
	require.NoError(t, os.Remove(m.File()))
	require.NoError(t, os.Mkdir(m.File(), 0o700))
	_, err = m.RewindTo(user)
	require.Error(t, err)
	assert.Equal(t, leaf, m.LeafID())
}
