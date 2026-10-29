package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

// flushedManager returns a persisted manager with one exchange on disk, i.e.
// the state most sessions are in when the user renames them.
func flushedManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "read foo.go"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "done reading"})
	require.NoError(t, err)
	require.FileExists(t, m.File())
	return m
}

func TestSetTitleBeforeFirstFlush(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	require.NoFileExists(t, m.File(), "nothing is written before the first assistant message")

	require.NoError(t, m.SetTitle("  fix   resume  "))
	assert.Equal(t, "fix resume", m.Title())

	_, err = m.Append(llm.Message{Role: llm.RoleUser, Content: "hello"})
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "hi"})
	require.NoError(t, err)
	require.FileExists(t, m.File())

	meta := readMeta(t, dir, m.ID())
	assert.Equal(t, "fix resume", meta.Title)

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, "fix resume", loaded.Title())
}

func TestSetTitleRewritesHeaderOnly(t *testing.T) {
	m := flushedManager(t)
	path := m.File()

	past := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(path, past, past))
	before := readLines(t, path)
	require.Len(t, before, 3, "header plus two messages")

	require.NoError(t, m.SetTitle("resume leak"))
	assert.Equal(t, "resume leak", m.Title())

	after := readLines(t, path)
	require.Len(t, after, len(before))
	assert.Contains(t, after[0], `"title":"resume leak"`)
	assert.Equal(t, before[1:], after[1:], "message lines must survive verbatim")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.WithinDuration(t, past, info.ModTime(), 2*time.Second,
		"ListSessions sorts by mtime, so renaming must not bump it")

	loaded, err := OpenSession(path)
	require.NoError(t, err)
	assert.Equal(t, "resume leak", loaded.Title())
	assert.Equal(t, "done reading", loaded.BuildContext()[1].(SessionMessageEntry).Message.Content)
}

func TestSetTitleUnchangedSkipsWrite(t *testing.T) {
	m := flushedManager(t)
	require.NoError(t, m.SetTitle("same name"))

	path := m.File()
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(path, past, past))
	before := readLines(t, path)

	require.NoError(t, m.SetTitle("same name"))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.WithinDuration(t, past, info.ModTime(), 2*time.Second)
	assert.Equal(t, before, readLines(t, path))
}

func TestSetTitleEmptyClearsName(t *testing.T) {
	m := flushedManager(t)
	require.NoError(t, m.SetTitle("temporary"))
	require.NoError(t, m.SetTitle("   "))

	assert.Empty(t, m.Title())
	header := readLines(t, m.File())[0]
	assert.NotContains(t, header, `"title"`, "an unnamed session omits the field")

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Empty(t, loaded.Title())
}

func TestNormalizeTitle(t *testing.T) {
	long := strings.Repeat("a", MaxTitleRunes+20)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"trim and collapse", "  fix   resume\tleak  ", "fix resume leak"},
		{"newlines become spaces", "fix\nresume", "fix resume"},
		{"control characters", "fix\x00resume", "fix resume"},
		{"empty stays empty", " \t\n ", ""},
		{"long titles are capped", long, strings.Repeat("a", MaxTitleRunes) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeTitle(tt.in))
		})
	}
}

func TestRenameSessionByIDPrefix(t *testing.T) {
	dir := t.TempDir()
	target, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	_, err = target.Append(llm.Message{Role: llm.RoleAssistant, Content: "one"})
	require.NoError(t, err)

	other, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	_, err = other.Append(llm.Message{Role: llm.RoleAssistant, Content: "two"})
	require.NoError(t, err)

	require.NoError(t, RenameSession(dir, target.ID()[:8], "picked by prefix"))

	assert.Equal(t, "picked by prefix", readMeta(t, dir, target.ID()).Title)
	assert.Empty(t, readMeta(t, dir, other.ID()).Title)
}

func TestRenameSessionUnknownID(t *testing.T) {
	dir := t.TempDir()
	m := flushedManagerInto(t, dir)

	err := RenameSession(dir, "nope", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Empty(t, readMeta(t, dir, m.ID()).Title)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), ".tmp"), "no temp file is left behind")
	}
}

// Lock files live beside the sessions; listings must not treat them as
// sessions, and deleting a session takes its lock with it.
func TestLockFilesStayOutOfListings(t *testing.T) {
	dir := t.TempDir()
	m := flushedManagerInto(t, dir)
	require.NoError(t, m.SetTitle("named"))
	require.FileExists(t, m.File()+".lock")

	list, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, DeleteSession(dir, m.ID()))
	assert.NoFileExists(t, m.File()+".lock")
}

func TestRenameSessionRejectsCorruptHeader(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/2024-01-01T00-00-00_deadbeef.jsonl"
	require.NoError(t, os.WriteFile(path, []byte("not json\n"), 0o644))

	require.Error(t, RenameSession(dir, "deadbeef", "x"))
	assert.Equal(t, "not json\n", readFile(t, path), "a failed rewrite leaves the file alone")
}

func TestForkInheritsTitle(t *testing.T) {
	m := flushedManager(t)
	require.NoError(t, m.SetTitle("shared name"))

	fork, err := m.Fork()
	require.NoError(t, err)
	assert.Equal(t, "shared name", fork.Title())
	assert.Equal(t, "shared name", readMeta(t, m.config.sessionDir, fork.ID()).Title)
}

// flushedManagerInto creates one flushed session in dir so tests can rename by
// id from the outside, the way `phi sessions rename` does.
func flushedManagerInto(t *testing.T, dir string) *Manager {
	t.Helper()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "hi"})
	require.NoError(t, err)
	return m
}

func readMeta(t *testing.T, dir, id string) SessionMeta {
	t.Helper()
	list, err := ListSessions(dir)
	require.NoError(t, err)
	for _, meta := range list {
		if meta.ID == id {
			return meta
		}
	}
	t.Fatalf("session %s not listed in %s", id, dir)
	return SessionMeta{}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw := readFile(t, path)
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	for i, line := range lines {
		assert.True(t, json.Valid([]byte(line)), "line %d is not JSON: %s", i+1, line)
	}
	return lines
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
