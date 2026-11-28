package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
)

func TestSessionPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.Append(core.Message{Role: core.RoleUser, Content: "read foo.go"})
	require.NoError(t, err)
	_, err = m.Append(core.Message{Role: core.RoleAssistant, Content: "done reading"})
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
	_, err = loaded.Append(core.Message{Role: core.RoleUser, Content: "continue"})
	require.NoError(t, err)
	_, err = loaded.Append(core.Message{Role: core.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Positive(t, info.Size())

	reloaded, err := OpenSession(path)
	require.NoError(t, err)
	assert.Equal(t, messageContents(loaded.BuildContext()), messageContents(reloaded.BuildContext()))
}

func TestSessionModelChangesAppendOnly(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true), WithModel("model-a"))
	require.NoError(t, err)
	assert.Equal(t, "model-a", m.ModelName())

	_, err = m.Append(core.Message{Role: core.RoleUser, Content: "hello"})
	require.NoError(t, err)
	_, err = m.Append(core.Message{Role: core.RoleAssistant, Content: "done"})
	require.NoError(t, err)
	before, err := os.ReadFile(m.File())
	require.NoError(t, err)
	var header map[string]any
	require.NoError(t, json.Unmarshal(bytes.SplitN(before, []byte("\n"), 2)[0], &header))
	assert.NotContains(t, header, "model", "new headers contain no mutable model state")

	require.NoError(t, m.SetModel("model-b"))
	assert.Equal(t, "model-b", m.ModelName())
	after, err := os.ReadFile(m.File())
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(after, before), "existing records must remain byte-for-byte intact")
	var change ModelChangeEntry
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(after[len(before):]), &change))
	assert.Equal(t, "EntryModelChange", change.Type)
	assert.Equal(t, "model-b", change.Model)
	require.NoError(t, m.SetModel("model-b"))
	unchanged, err := os.ReadFile(m.File())
	require.NoError(t, err)
	assert.Equal(t, after, unchanged, "selecting the same model must not add a record")

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, "model-b", loaded.ModelName())
	assert.Len(t, ReplaySnapshot(loaded.BuildContext(), nil).Messages, 2, "model changes are not transcript messages")
	assert.Equal(t, messageContents(m.BuildContext()), messageContents(loaded.BuildContext()))

	list, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, m.ID(), list[0].ID)
	assert.Equal(t, "hello", list[0].Preview)
}

func TestSessionWithoutModelRemainsLoadable(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(core.Message{Role: core.RoleAssistant, Content: "done"})
	require.NoError(t, err)

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Empty(t, loaded.ModelName())
}

func TestSessionPersistUsageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.Append(core.Message{
		Role:    core.RoleAssistant,
		Content: "done",
		Usage: core.Usage{
			PromptTokens:        7,
			CompletionTokens:    7,
			TotalTokens:         19,
			PromptTokensDetails: &core.PromptTokensDetails{CachedTokens: 5},
		},
	})
	require.NoError(t, err)

	// In memory the wrapper mirrors msg.Usage (core.Message.Usage is json:"-").
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

// ProviderState, thought signatures, and cache-write usage only survive a
// reload because SessionMessageEntry spells out its own JSON methods.
func TestSessionMessageEntryJSONRoundTrip(t *testing.T) {
	orig := SessionMessageEntry{
		SessionBaseEntry: SessionBaseEntry{
			Type: EntryMessage, ID: "m1",
			Timestamp: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
		},
		Message: core.Message{
			Role:             core.RoleAssistant,
			Content:          "done",
			ReasoningContent: "thinking",
			ToolCalls: []core.ToolCall{{
				Index: 0, ID: "call_1", Type: "function",
				Function:         core.Function{Name: "read", Arguments: `{"path":"foo.go"}`},
				ThoughtSignature: "sig",
			}},
			Images: []core.Image{{Data: "QUJD", MimeType: "image/png"}},
			ProviderState: &core.ProviderState{
				Provider: core.ProviderAnthropic,
				Model:    "claude-sonnet-4-5",
				Endpoint: "https://api.anthropic.com",
				Data: core.ProviderData{
					Prefix: "abc",
					Items:  []json.RawMessage{json.RawMessage(`{"type":"text","text":"hi"}`)},
				},
			},
		},
		Usage: core.Usage{
			CompletionTokens: 3, PromptTokens: 5, TotalTokens: 11,
			PromptTokensDetails: &core.PromptTokensDetails{CachedTokens: 2, CacheWriteTokens: 1},
		},
	}

	body, err := json.Marshal(orig)
	require.NoError(t, err)
	for _, key := range []string{
		`"thought_signature":"sig"`, `"provider_state"`, `"cache_write_tokens":1`, `"mimeType":"image/png"`,
	} {
		assert.Contains(t, string(body), key)
	}

	var got SessionMessageEntry
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, orig, got)
}

// Session files written before the core split embedded llm.Message with JSON
// tags; the explicit wire DTO must keep decoding that shape byte for byte.
func TestSessionMessageEntryDecodesLegacyFormat(t *testing.T) {
	const legacy = `{"type":"EntryMessage","id":"m1","parentID":null,"timestamp":"2025-01-02T03:04:05Z",` +
		`"message":{"role":"assistant","content":"done","reasoning_content":"thinking",` +
		`"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{}"},` +
		`"thought_signature":"sig"}],"provider_state":{"provider":"Anthropic","model":"claude-sonnet-4-5",` +
		`"endpoint":"https://api.anthropic.com","data":{"prefix":"abc","items":[{"type":"text","text":"hi"}]}}},` +
		`"usage":{"completion_tokens":3,"prompt_tokens":5,"total_tokens":11,` +
		`"prompt_tokens_details":{"cached_tokens":2,"cache_write_tokens":1}}}`

	var entry SessionMessageEntry
	require.NoError(t, json.Unmarshal([]byte(legacy), &entry))
	assert.Equal(t, EntryMessage, entry.GetType())
	assert.Equal(t, "m1", entry.GetID())
	assert.Nil(t, entry.GetParent())
	assert.Equal(t, time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC), entry.Timestamp)

	msg := entry.Message
	assert.Equal(t, core.RoleAssistant, msg.Role)
	assert.Equal(t, "thinking", msg.ReasoningContent)
	require.Len(t, msg.ToolCalls, 1)
	assert.Equal(t, core.ToolCall{
		Index: 0, ID: "call_1", Type: "function",
		Function:         core.Function{Name: "read", Arguments: "{}"},
		ThoughtSignature: "sig",
	}, msg.ToolCalls[0])
	require.NotNil(t, msg.ProviderState)
	assert.Equal(t, core.ProviderAnthropic, msg.ProviderState.Provider)
	assert.Equal(t, "claude-sonnet-4-5", msg.ProviderState.Model)
	assert.Equal(t, core.ProviderData{
		Prefix: "abc",
		Items:  []json.RawMessage{json.RawMessage(`{"type":"text","text":"hi"}`)},
	}, msg.ProviderState.Data)

	assert.Equal(t, 11, entry.Usage.TotalTokens)
	assert.Equal(t, 2, entry.Usage.CachedTokens())
	assert.Equal(t, 1, entry.Usage.CacheWriteTokens())
}

// The manager writes entries through an interface value, so this pins that
// the value-receiver MarshalJSON is reached on the real persist path.
func TestSessionPersistProviderStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.Append(core.Message{
		Role:    core.RoleAssistant,
		Content: "done",
		ToolCalls: []core.ToolCall{{
			ID: "call_1", Type: "function",
			Function:         core.Function{Name: "read", Arguments: "{}"},
			ThoughtSignature: "sig",
		}},
		ProviderState: &core.ProviderState{
			Provider: core.ProviderAnthropic,
			Model:    "claude-sonnet-4-5",
			Endpoint: "https://api.anthropic.com",
			Data: core.ProviderData{
				Prefix: "abc",
				Items:  []json.RawMessage{json.RawMessage(`{"type":"text","text":"hi"}`)},
			},
		},
	})
	require.NoError(t, err)

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	ctx := loaded.BuildContext()
	require.Len(t, ctx, 1)
	assert.Equal(t, m.BuildContext()[0].(SessionMessageEntry).Message, ctx[0].(SessionMessageEntry).Message)
}

// A resumed session must show its own token counts, so replay has to read
// usage from the entry (core.Message.Usage does not survive a reload).
func TestReplaySnapshotUsageAfterReload(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	_, err = m.Append(core.Message{Role: core.RoleUser, Content: "hello"})
	require.NoError(t, err)
	_, err = m.Append(core.Message{
		Role:    core.RoleAssistant,
		Content: "done",
		Usage: core.Usage{
			PromptTokens:        7,
			CompletionTokens:    7,
			TotalTokens:         19,
			PromptTokensDetails: &core.PromptTokensDetails{CachedTokens: 5},
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

	_, err = m.Append(core.Message{Role: core.RoleUser, Content: "old"})
	require.NoError(t, err)
	keptID, err := m.Append(core.Message{Role: core.RoleAssistant, Content: "kept"})
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
	_, err = m.Append(core.Message{Role: core.RoleUser, Content: "after"})
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

	keptID, err := m.Append(core.Message{Role: core.RoleAssistant, Content: "kept"})
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

// The summary survives the round trip the same way: a resumed marker must still
// reveal what replaced the cut history.
func TestReplaySnapshotCompactionSummary(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)

	keptID, err := m.Append(core.Message{Role: core.RoleAssistant, Content: "kept"})
	require.NoError(t, err)
	compactionID, err := m.AppendCompaction(Compaction{
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
	assert.Equal(t, "conversation summary", marker.Summary)

	items := Project(snap)
	require.NotEmpty(t, items)
	assert.Equal(t, "conversation summary", items[0].Summary)
	assert.Equal(t, compactionID, items[0].ID, "the marker keeps the persisted entry ID across a resume")
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
	_, err = m.Append(core.Message{Role: core.RoleUser, Content: "list me please"})
	require.NoError(t, err)
	_, err = m.Append(core.Message{Role: core.RoleAssistant, Content: "ok"})
	require.NoError(t, err)

	list, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, m.ID(), list[0].ID)
	assert.Equal(t, "list me please", list[0].Preview)
	assert.Equal(t, dir, list[0].Cwd)
}

func TestDeleteSession(t *testing.T) {
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

	gone := write("deadbeef00001111")
	_ = write("abcdef1111111111")
	_ = write("abcdef2222222222")
	_ = write("abcdef9999999999")

	// Exact id reports the id it deleted.
	resolved, err := DeleteSession(dir, "deadbeef00001111")
	require.NoError(t, err)
	assert.Equal(t, "deadbeef00001111", resolved)
	_, err = os.Stat(gone)
	require.ErrorIs(t, err, os.ErrNotExist)

	// Unique prefix reports the full resolved id, not the prefix.
	resolved, err = DeleteSession(dir, "abcdef1")
	require.NoError(t, err)
	assert.Equal(t, "abcdef1111111111", resolved)
	list, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 2)

	// Ambiguous prefix is an error and deletes nothing.
	resolved, err = DeleteSession(dir, "abcdef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.Empty(t, resolved)
	list, err = ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 2, "ambiguous prefix must not delete")

	// Unknown id is an error, not a silent no-op.
	resolved, err = DeleteSession(dir, "abcdef3333333333")
	require.Error(t, err)
	assert.Empty(t, resolved)
	assert.Contains(t, err.Error(), "not found")
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

// A byte-budget cut inside a CJK rune used to leave orphan continuation bytes
// in the preview; the sessions picker then wrote them to the tty raw and the
// terminal's parser desynced (garbled rows, stale columns).
func TestTruncatePreviewStaysValidUTF8(t *testing.T) {
	msg := "示例 fake preview 文本：ASCII 与 中文 mixed，验证 UTF-8 截断边界保持合法"
	got := truncatePreview(msg, 72)
	assert.True(t, utf8.ValidString(got), "preview must stay valid UTF-8: %q", got)
	assert.Equal(t, "示例 fake preview 文本：ASCII 与 中文 mixed，验证 UTF-8 截…", got)
	assert.Equal(t, msg, truncatePreview(msg, len(msg)))
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
