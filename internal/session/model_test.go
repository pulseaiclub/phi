package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestModelChangesBeforeFirstFlush(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true), WithModel("initial"))
	require.NoError(t, err)
	require.NoError(t, m.SetModel("second"))
	require.NoError(t, m.SetModel("third"))
	assert.NoFileExists(t, m.File(), "model selections retain lazy session persistence")
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "done"})
	require.NoError(t, err)
	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, "third", loaded.ModelName())
	var names []string
	for _, entry := range loaded.entries {
		if change, ok := entry.(ModelChangeEntry); ok {
			names = append(names, change.Model)
		}
	}
	assert.Equal(t, []string{"initial", "second", "third"}, names)
}

func TestModelChangePreservesOtherAppends(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true), WithModel("initial"))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "first"})
	require.NoError(t, err)
	other, err := OpenSession(m.File())
	require.NoError(t, err)
	_, err = other.Append(llm.Message{Role: llm.RoleUser, Content: "appended by another reader"})
	require.NoError(t, err)
	before, err := os.ReadFile(m.File())
	require.NoError(t, err)
	require.NoError(t, m.SetModel("next"))
	after, err := os.ReadFile(m.File())
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(after, before), "a stale manager must not overwrite another writer's records")
	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, "next", loaded.ModelName())
	assert.Equal(t, other.Len()+1, loaded.Len())
}

func TestModelChangeAppendFailureIsRetryable(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true), WithModel("initial"))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "done"})
	require.NoError(t, err)
	leaf, count := m.LeafID(), m.Len()
	path := m.File()
	m.sessionFile = filepath.Join(dir, "missing", "session.jsonl")
	require.ErrorContains(t, m.SetModel("next"), "append model change")
	assert.Equal(t, "initial", m.ModelName())
	assert.Equal(t, leaf, m.LeafID())
	assert.Equal(t, count, m.Len())
	m.sessionFile = path
	require.NoError(t, m.SetModel("next"))
	loaded, err := OpenSession(path)
	require.NoError(t, err)
	assert.Equal(t, "next", loaded.ModelName())
	assert.Equal(t, count+1, loaded.Len())
}

func TestModelSelectionFollowsBranchAndSurvivesCompaction(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true), WithModel("initial"))
	require.NoError(t, err)
	base, err := m.Append(llm.Message{Role: llm.RoleAssistant, Content: "shared"})
	require.NoError(t, err)
	require.NoError(t, m.SetModel("other-branch"))
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "other"})
	require.NoError(t, err)
	m.leafID = &base
	assert.Equal(t, "initial", m.ModelName(), "off-branch model changes must be ignored")

	fork, err := m.Fork()
	require.NoError(t, err)
	assert.Equal(t, "initial", fork.ModelName())
	restoredFork, err := OpenSession(fork.File())
	require.NoError(t, err)
	assert.Equal(t, "initial", restoredFork.ModelName())
	require.NoError(t, fork.SetModel("fork-model"))
	_, err = fork.AppendCompaction(Compaction{Summary: "shared history"})
	require.NoError(t, err)
	assert.Equal(t, "fork-model", fork.ModelName(), "model resolution must walk past compaction")
	loaded, err := OpenSession(fork.File())
	require.NoError(t, err)
	assert.Equal(t, "fork-model", loaded.ModelName())
	assert.Equal(t, []string{"compaction:shared history"}, messageContents(loaded.BuildContext()))
	for _, entry := range loaded.entries {
		if change, ok := entry.(ModelChangeEntry); ok {
			assert.NotEqual(t, "other-branch", change.Model)
		}
	}

	// The last physical EntryModelChange is off this new branch. Restoring must
	// derive the model from the leaf's ancestry.
	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "back at base"})
	require.NoError(t, err)
	loaded, err = OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, "initial", loaded.ModelName())
	list, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 2)
	models := make(map[string]string)
	for _, meta := range list {
		models[meta.ID] = meta.Model
	}
	assert.Equal(t, "initial", models[m.ID()])
	assert.Equal(t, "fork-model", models[fork.ID()])
}

func TestModelChangeInSessionWithoutAssistant(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "header only"},
		{name: "user only", body: `{"type":"EntryMessage","id":"first","parentID":null,"message":{"role":"user","content":"hello"}}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "2026-01-01_session.jsonl")
			data := []byte(`{"type":"EntrySession","id":"session","cwd":"/tmp"}` + "\n" + tc.body)
			require.NoError(t, os.WriteFile(path, data, 0o600))
			m, err := OpenSession(path)
			require.NoError(t, err)
			assert.Empty(t, m.ModelName())
			fork, err := m.Fork()
			require.NoError(t, err)
			loaded, err := OpenSession(fork.File())
			require.NoError(t, err)
			assert.Empty(t, loaded.ModelName())
			require.NoError(t, m.SetModel("new-model"))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.True(t, bytes.HasPrefix(after, data))
			loaded, err = OpenSession(path)
			require.NoError(t, err)
			assert.Equal(t, "new-model", loaded.ModelName())
		})
	}
}
