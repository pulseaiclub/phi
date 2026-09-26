package agent

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

func TestSessionPersistFlush(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(
		WithCwd(dir),
		WithSessionDir(dir),
		WithPersist(true),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID())
	assert.NotEmpty(t, sess.File())

	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "hi"}))
	_, err = os.Stat(sess.File())
	assert.True(t, os.IsNotExist(err), "should not flush before first assistant")

	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleAssistant, Content: "hello"}))
	require.FileExists(t, sess.File())

	b, err := os.ReadFile(sess.File())
	require.NoError(t, err)
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	first := splitFirstJSONL(b)
	require.NoError(t, json.Unmarshal(first, &header))
	assert.Equal(t, "EntrySession", header.Type)
	assert.Equal(t, sess.ID(), header.ID)
}

func TestSessionPersistFalseNoDisk(t *testing.T) {
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	assert.Empty(t, sess.File())
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "a"}))
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleAssistant, Content: "b"}))
	assert.Empty(t, sess.File())
}

func TestEngineSetModelKeepsSession(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(
		WithCwd(dir),
		WithSessionDir(dir),
		WithPersist(true),
	)
	require.NoError(t, err)
	eng, err := NewEngine(
		llm.ModelConfig{Name: "model-a", APIKey: "k", BaseURL: "http://example"},
		sess,
	)
	require.NoError(t, err)

	id := eng.SessionID()
	file := eng.SessionFile()
	require.NotEmpty(t, id)
	require.NotEmpty(t, file)

	require.NoError(t, eng.session.Append(llm.Message{Role: llm.RoleUser, Content: "keep me"}))
	require.NoError(t, eng.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"}))
	n := eng.session.Len()

	eng.SetModel(llm.ModelConfig{
		Name:          "model-b",
		APIKey:        "k",
		BaseURL:       "http://example",
		ContextWindow: 8192,
		SkillPath:     dir,
	})
	assert.Equal(t, id, eng.SessionID())
	assert.Equal(t, file, eng.SessionFile())
	assert.Equal(t, n, eng.session.Len())
	assert.Equal(t, 8192, eng.modelCfg.ContextWindow)
	assert.Equal(t, dir, eng.modelCfg.SkillPath)
}

func TestSessionBuildContextLabelsCompactionSummary(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(WithCwd(dir), WithSessionDir(dir))
	require.NoError(t, err)

	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "first"}))
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer"}))
	require.NoError(t, sess.AppendCompaction(session.Compaction{Summary: "we renamed the SDK"}))
	require.NoError(t, sess.Append(llm.Message{Role: llm.RoleUser, Content: "carry on"}))

	msgs := sess.BuildContext()
	require.Len(t, msgs, 2)
	assert.Equal(t, llm.RoleUser, msgs[0].Role)
	assert.Equal(
		t,
		"The conversation history before this point was compacted into the following summary:\n\n<summary>\nwe renamed the SDK\n</summary>",
		msgs[0].Content,
	)
	assert.Equal(t, "carry on", msgs[1].Content)

	// The entry keeps the raw summary: the next round feeds it back to the
	// summarizer as PreviousSummary, which must not see the label.
	found := false
	for _, entry := range sess.PathEntries() {
		if ce, ok := entry.(session.CompactionEntry); ok {
			assert.Equal(t, "we renamed the SDK", ce.Compaction.Summary)
			found = true
		}
	}
	assert.True(t, found, "compaction entry missing from the path")
}

func splitFirstJSONL(b []byte) []byte {
	for i, c := range b {
		if c == '\n' {
			return b[:i]
		}
	}
	return b
}
