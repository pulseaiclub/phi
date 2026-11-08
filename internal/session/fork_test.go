package session

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

// forkSource builds a persisted session with one user/assistant round so the
// file exists and later appends flush immediately.
func forkSource(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true), WithModel("model-a"))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "hello"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "hi"})
	require.NoError(t, err)
	return m
}

// entryIDs names context entries so a fork can be compared with a session read
// back from disk. Timestamps lose their in-memory monotonic clock on the way
// through JSON, so whole-entry equality only holds before a round trip.
func entryIDs(entries []MessageEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.GetID())
	}
	return ids
}

func TestForkCopiesBranchAndRecordsParent(t *testing.T) {
	src := forkSource(t)
	fork, err := src.Fork()
	require.NoError(t, err)

	assert.NotEqual(t, src.ID(), fork.ID(), "a fork is a distinct session")
	assert.NotEqual(t, src.File(), fork.File())
	assert.Equal(t, 4, fork.Len(), "header, model selection, and the two copied messages")
	assert.Equal(t, src.LeafID(), fork.LeafID(), "the fork keeps the source leaf")
	assert.Equal(t, src.Cwd(), fork.Cwd())
	assert.Equal(t, src.ModelName(), fork.ModelName(), "model selection rides on the copied branch")
	assert.Equal(t, entryIDs(src.BuildContext()), entryIDs(fork.BuildContext()))

	loaded, err := OpenSession(fork.File())
	require.NoError(t, err)
	assert.Equal(t, src.ID(), loaded.config.parentID, "the fork records its source session")
	header, ok := loaded.entries[0].(SessionHeader)
	require.True(t, ok, "the first entry is the session header")
	assert.Equal(t, src.ID(), header.ParentSession)
	assert.Equal(t, fork.ID(), loaded.ID())
	assert.Equal(t, entryIDs(src.BuildContext()), entryIDs(loaded.BuildContext()))
}

func TestForkKeepsCompactionBoundary(t *testing.T) {
	src := forkSource(t)
	kept := src.LeafID()
	_, err := src.AppendCompaction(Compaction{Summary: "summarized", FirstKeptEntryID: kept, TokensBefore: 4096})
	require.NoError(t, err)
	_, err = src.Append(llm.Message{Role: llm.RoleUser, Content: "after compaction"})
	require.NoError(t, err)

	fork, err := src.Fork()
	require.NoError(t, err)

	srcCtx, forkCtx := src.BuildContext(), fork.BuildContext()
	assert.Equal(t, entryIDs(srcCtx), entryIDs(forkCtx))
	require.NotEmpty(t, forkCtx)
	comp, ok := forkCtx[0].(CompactionEntry)
	require.True(t, ok, "the newest compaction leads the context")
	assert.Equal(t, kept, comp.Compaction.FirstKeptEntryID)
	assert.Equal(t, "summarized", comp.Compaction.Summary)
}

func TestForkExcludesEntriesOffTheBranch(t *testing.T) {
	src := forkSource(t)
	base := src.LeafID()
	abandoned, err := src.Append(llm.Message{Role: llm.RoleUser, Content: "abandoned branch"})
	require.NoError(t, err)
	baseID := base
	src.leafID = &baseID // diverge: the abandoned message is no longer on the leaf path
	_, err = src.Append(llm.Message{Role: llm.RoleUser, Content: "kept branch"})
	require.NoError(t, err)

	fork, err := src.Fork()
	require.NoError(t, err)

	assert.Equal(t, src.Len()-1, fork.Len(), "header plus the branch only; the abandoned message stays behind")
	assert.Equal(t, entryIDs(src.BuildContext()), entryIDs(fork.BuildContext()))
	for _, entry := range fork.entries {
		assert.NotEqual(t, abandoned, entry.GetID(), "off-branch entries must not be copied")
	}
}

func TestForkEmptySession(t *testing.T) {
	dir := t.TempDir()
	src, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	require.Nil(t, src.leafID)
	assert.NoFileExists(t, src.File(), "an empty session stays unflushed")

	fork, err := src.Fork()
	require.NoError(t, err)

	assert.Equal(t, 1, fork.Len(), "the fork is a header and nothing else")
	assert.Empty(t, fork.BuildContext())
	assert.FileExists(t, fork.File(), "an empty fork is still listed")
	assert.NoFileExists(t, src.File(), "forking must not persist the source")

	loaded, err := OpenSession(fork.File())
	require.NoError(t, err)
	assert.Equal(t, src.ID(), loaded.config.parentID)
	assert.Equal(t, dir, loaded.Cwd())
	assert.Empty(t, loaded.BuildContext())

	list, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, fork.ID(), list[0].ID)
	assert.Equal(t, src.ID(), list[0].Parent, "the listing exposes the fork source")
}

func TestForkOfForkKeepsLineage(t *testing.T) {
	src := forkSource(t)
	first, err := src.Fork()
	require.NoError(t, err)
	second, err := first.Fork()
	require.NoError(t, err)

	loaded, err := OpenSession(second.File())
	require.NoError(t, err)
	assert.Equal(t, first.ID(), loaded.config.parentID)
	assert.Equal(t, entryIDs(src.BuildContext()), entryIDs(second.BuildContext()))
}

func TestForkRequiresPersistedSession(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithShouldFlush(false))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "in memory"})
	require.NoError(t, err)

	_, err = m.Fork()
	require.ErrorContains(t, err, "requires a persisted session")

	// Nothing may be written next to the process cwd or the session dir.
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestForkRejectsUnresolvableLeaf(t *testing.T) {
	src := forkSource(t)
	missing := "not-an-entry"
	src.leafID = &missing

	_, err := src.Fork()
	require.ErrorContains(t, err, "could not resolve branch")
}

func TestForkKeepsSessionsIndependent(t *testing.T) {
	src := forkSource(t)
	before, err := os.ReadFile(src.File())
	require.NoError(t, err)

	fork, err := src.Fork()
	require.NoError(t, err)

	afterFork, err := os.ReadFile(src.File())
	require.NoError(t, err)
	assert.Equal(t, before, afterFork, "forking must not touch the source file")

	_, err = fork.Append(llm.Message{Role: llm.RoleUser, Content: "only in the fork"})
	require.NoError(t, err)
	untouched, err := os.ReadFile(src.File())
	require.NoError(t, err)
	assert.Equal(t, before, untouched, "fork appends stay out of the source")

	_, err = src.Append(llm.Message{Role: llm.RoleUser, Content: "only in the source"})
	require.NoError(t, err)
	forkLoaded, err := OpenSession(fork.File())
	require.NoError(t, err)
	assert.Equal(t, entryIDs(fork.BuildContext()), entryIDs(forkLoaded.BuildContext()))
	assert.NotEqual(t, src.BuildContext(), fork.BuildContext(), "the source kept its own tail")
}
