package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestSessionPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "read foo.go"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "done reading"})
	require.NoError(t, err)

	path := m.File()
	require.FileExists(t, path)

	loaded, err := OpenSession(path)
	require.NoError(t, err)
	assert.Equal(t, m.ID(), loaded.ID())
	assert.Equal(t, path, loaded.File())
	assert.Equal(t, dir, loaded.Cwd())

	orig := messageContents(m.BuildContext())
	got := messageContents(loaded.BuildContext())
	assert.Equal(t, orig, got)

	// Append continues on the same file.
	_, err = loaded.Append(llm.Message{Role: llm.RoleUser, Content: "continue"})
	require.NoError(t, err)
	_, err = loaded.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Positive(t, info.Size())

	reloaded, err := OpenSession(path)
	require.NoError(t, err)
	assert.Equal(t, messageContents(loaded.BuildContext()), messageContents(reloaded.BuildContext()))
}

func TestSessionPersistUsageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.Append(llm.Message{
		Role:    llm.RoleAssistant,
		Content: "done",
		Usage: llm.Usage{
			PromptTokens:        7,
			CompletionTokens:    7,
			TotalTokens:         19,
			PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 5},
		},
	})
	require.NoError(t, err)

	// In memory the wrapper mirrors msg.Usage (llm.Message.Usage is json:"-").
	inMem := m.BuildContext()[0].(SessionMessageEntry)
	assert.Equal(t, 19, inMem.Usage.TotalTokens)

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)

	ctx := loaded.BuildContext()
	require.Len(t, ctx, 1)
	entry := ctx[0].(SessionMessageEntry)
	assert.Equal(t, 7, entry.Usage.PromptTokens)
	assert.Equal(t, 7, entry.Usage.CompletionTokens)
	assert.Equal(t, 19, entry.Usage.TotalTokens)
	assert.Equal(t, 5, entry.Usage.CachedTokens())
}

// A resumed session must show its own token counts, so replay has to read
// usage from the entry (llm.Message.Usage does not survive a reload).
func TestReplaySnapshotUsageAfterReload(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "hello"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{
		Role:    llm.RoleAssistant,
		Content: "done",
		Usage: llm.Usage{
			PromptTokens:        7,
			CompletionTokens:    7,
			TotalTokens:         19,
			PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 5},
		},
	})
	require.NoError(t, err)

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)

	snap := ReplaySnapshot(loaded.BuildContext(), nil)
	require.Len(t, snap.Messages, 2)
	want := TokenUsage{PromptTokens: 7, CompletionTokens: 7, CachedTokens: 5, TotalTokens: 19}
	assert.Equal(t, 19, want.ContextTokens(), "buckets sum to the reported total")
	assert.Equal(t, want, snap.Messages[1].Usage)
	assert.Equal(t, want, snap.LastUsage())
}

func TestSnapshotLastUsageEmpty(t *testing.T) {
	snap := Snapshot{Messages: []Message{{ID: "1", Role: RoleUser}, {ID: "2", Role: RoleAssistant}}}
	assert.False(t, snap.LastUsage().Reported())
}

func TestSessionPersistCompaction(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "old"})
	require.NoError(t, err)
	keptID, err := m.Append(llm.Message{Role: llm.RoleAssistant, Content: "kept"})
	require.NoError(t, err)
	_, err = m.AppendCompaction(Compaction{
		Summary:          "conversation summary",
		FirstKeptEntryID: keptID,
		Details: CompactionDetails{
			ReadFiles:     []string{"a.go"},
			ModifiedFiles: []string{"b.go"},
		},
	})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "after"})
	require.NoError(t, err)

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)

	ctx := loaded.BuildContext()
	require.GreaterOrEqual(t, len(ctx), 2)
	assert.Equal(t, EntryCompaction, ctx[0].GetType())
	ce := ctx[0].(CompactionEntry)
	assert.Equal(t, "conversation summary", ce.Compaction.Summary)
	assert.Equal(t, []string{"a.go"}, ce.Compaction.Details.ReadFiles)
	assert.Equal(t, []string{"b.go"}, ce.Compaction.Details.ModifiedFiles)

	got := messageContents(ctx)
	want := messageContents(m.BuildContext())
	assert.Equal(t, want, got)
}

// A resumed session rebuilds its marker from the persisted entry, so the
// pre-cut token count has to survive the round trip too.
func TestReplaySnapshotCompactionTokensBefore(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	keptID, err := m.Append(llm.Message{Role: llm.RoleAssistant, Content: "kept"})
	require.NoError(t, err)
	_, err = m.AppendCompaction(Compaction{
		Summary:          "conversation summary",
		FirstKeptEntryID: keptID,
		TokensBefore:     15000,
	})
	require.NoError(t, err)

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)

	snap := ReplaySnapshot(loaded.BuildContext(), nil)
	require.NotEmpty(t, snap.Messages)
	marker := snap.Messages[0]
	assert.Equal(t, RoleCompaction, marker.Role)
	assert.Equal(t, 15000, marker.TokensBefore)

	items := Project(snap)
	require.NotEmpty(t, items)
	assert.Equal(t, ItemCompaction, items[0].Kind)
	assert.Equal(t, 15000, items[0].TokensBefore)
}

func TestFindSessionFilePrefix(t *testing.T) {
	dir := t.TempDir()

	write := func(id string) string {
		name := "2026-01-01T00-00-00_" + id + ".jsonl"
		path := filepath.Join(dir, name)
		header := SessionHeader{
			Type:      EntrySession,
			ID:        id,
			Timestamp: "2026-01-01T00-00-00",
			Cwd:       dir,
		}
		f, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, jsonEncode(f, header))
		require.NoError(t, f.Close())
		return path
	}

	p1 := write("abcdef1234567890")
	_ = write("abcdef9999999999")
	p3 := write("deadbeef00001111")

	got, err := FindSessionFile(dir, "deadbeef00001111")
	require.NoError(t, err)
	assert.Equal(t, p3, got)

	got, err = FindSessionFile(dir, "deadbeef")
	require.NoError(t, err)
	assert.Equal(t, p3, got)

	_, err = FindSessionFile(dir, "abcdef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")

	got, err = FindSessionFile(dir, "abcdef1234567890")
	require.NoError(t, err)
	assert.Equal(t, p1, got)

	_, err = FindSessionFile(dir, "nope")
	require.Error(t, err)
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "list me please"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	list, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, m.ID(), list[0].ID)
	assert.Equal(t, "list me please", list[0].Preview)
	assert.Equal(t, dir, list[0].Cwd)
}

func TestOpenSessionFailFastBadLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-01-01T00-00-00_badbadbadbadbadb.jsonl")
	content := `{"type":"EntrySession","id":"badbadbadbadbadb","timestamp":"t","cwd":"/tmp"}
{not-json}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	_, err := OpenSession(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")
}

func messageContents(entries []MessageEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		switch v := e.(type) {
		case CompactionEntry:
			out = append(out, "compaction:"+v.Compaction.Summary)
		case SessionMessageEntry:
			out = append(out, string(v.Message.Role)+":"+v.Message.Content)
		default:
			out = append(out, e.GetType()+":"+e.GetID())
		}
	}
	return out
}

func jsonEncode(f *os.File, v any) error {
	return json.NewEncoder(f).Encode(v)
}
