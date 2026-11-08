package controller

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
)

// pathIDs names the entries of the active branch, so a fork can be compared
// with its source without depending on in-memory timestamps.
func pathIDs(s *agent.Session) []string {
	entries := s.PathEntries()
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.GetID())
	}
	return ids
}

// forkTestController seeds one user/assistant round so the session file exists
// and the transcript has something to copy.
func forkTestController(t *testing.T) *EngineController {
	t.Helper()
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, "http://127.0.0.1:9"))
	require.NoError(t, ctrl.engine.Session().Append(
		llm.Message{Role: llm.RoleUser, Content: "hello"},
		llm.Message{Role: llm.RoleAssistant, Content: "hi"},
	))
	return ctrl
}

func TestForkSessionSwitchesToDistinctSessionWithSameContext(t *testing.T) {
	ctrl := forkTestController(t)
	srcID, srcFile := ctrl.SessionID(), ctrl.SessionFile()
	srcPath := pathIDs(ctrl.engine.Session())
	srcBytes, err := os.ReadFile(srcFile)
	require.NoError(t, err)

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)

	assert.NotEqual(t, srcID, newID)
	assert.Equal(t, newID, ctrl.SessionID())
	assert.NotEqual(t, srcFile, ctrl.SessionFile())
	assert.Equal(t, srcPath, pathIDs(ctrl.engine.Session()), "the fork starts on the source's branch")
	assert.Equal(t, "primary", ctrl.ModelName(), "the fork keeps the active model")
	assert.NotEmpty(t, ctrl.ReplaySnapshot().Messages, "the transcript survives the fork")

	after, err := os.ReadFile(srcFile)
	require.NoError(t, err)
	assert.Equal(t, srcBytes, after, "forking must not touch the source file")
}

func TestForkSessionPersistsForkAndParent(t *testing.T) {
	ctrl := forkTestController(t)
	srcID := ctrl.SessionID()

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)

	loaded, err := session.OpenSession(ctrl.SessionFile())
	require.NoError(t, err)
	assert.Equal(t, newID, loaded.ID())
	assert.Equal(t, pathIDs(ctrl.engine.Session()), pathIDsOf(loaded))
	assert.Equal(t, srcID, forkHeader(t, ctrl.SessionFile()).ParentSession, "the fork records its source")

	list, err := session.ListSessions(ctrl.SessionDir())
	require.NoError(t, err)
	assert.Len(t, ids(list), 2, "both sessions stay available")
	assert.Contains(t, ids(list), srcID)
	assert.Contains(t, ids(list), newID)
}

func TestForkSessionLeavesSourceResumable(t *testing.T) {
	ctrl := forkTestController(t)
	srcID := ctrl.SessionID()
	srcPath := pathIDs(ctrl.engine.Session())

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)
	require.Equal(t, newID, ctrl.SessionID())

	_, err = ctrl.Resume(srcID)
	require.NoError(t, err)
	assert.Equal(t, srcID, ctrl.SessionID())
	assert.Equal(t, srcPath, pathIDs(ctrl.engine.Session()))

	_, err = ctrl.Resume(newID)
	require.NoError(t, err)
	assert.Equal(t, srcPath, pathIDs(ctrl.engine.Session()), "the fork reopens with the same branch")
}

func TestForkSessionFailureKeepsSourceActive(t *testing.T) {
	ctrl := forkTestController(t)
	srcID := ctrl.SessionID()
	path := pathIDs(ctrl.engine.Session())

	// A file where the session directory belongs makes the fork write fail; it
	// is the only failure this package can force without a fake session store.
	dir := ctrl.SessionDir()
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))

	id, err := ctrl.ForkSession()
	require.Error(t, err)
	assert.Empty(t, id)
	assert.Equal(t, srcID, ctrl.SessionID(), "a failed fork must not switch sessions")
	assert.Equal(t, path, pathIDs(ctrl.engine.Session()), "the source context is untouched")
	assert.NotEmpty(t, ctrl.ReplaySnapshot().Messages)
}

func TestForkSessionOfEmptySession(t *testing.T) {
	ctrl := newThinkingTestController(t, setupThinkingTest(t, llm.OpenAI, "http://127.0.0.1:9"))
	require.Empty(t, ctrl.ReplaySnapshot().Messages)

	newID, err := ctrl.ForkSession()
	require.NoError(t, err)
	assert.Equal(t, newID, ctrl.SessionID())
	assert.Empty(t, ctrl.ReplaySnapshot().Messages, "an empty session forks empty")
	assert.Equal(t, ctrl.cwd, ctrl.engine.SessionCwd())
}

// forkHeader decodes the first line of a session file, which is where a fork
// records the session it came from.
func forkHeader(t *testing.T, path string) session.SessionHeader {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	line, _, _ := bytes.Cut(data, []byte("\n"))
	var header session.SessionHeader
	require.NoError(t, json.Unmarshal(line, &header))
	return header
}

func pathIDsOf(m *session.Manager) []string {
	entries := m.BuildContext()
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.GetID())
	}
	return ids
}

func ids(list []session.SessionMeta) []string {
	out := make([]string, 0, len(list))
	for _, meta := range list {
		out = append(out, meta.ID)
	}
	return out
}
