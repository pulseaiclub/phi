package shellhist

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptsEnabledDefaultsOff(t *testing.T) {
	t.Setenv(EnvPrompts, "")
	assert.False(t, PromptsEnabled(), "prompt recall must stay off unless explicitly enabled")
}

func TestPromptsEnabledAcceptsOnValues(t *testing.T) {
	for _, v := range []string{"1", "true", "on", "yes", "ON", " True "} {
		t.Setenv(EnvPrompts, v)
		assert.True(t, PromptsEnabled(), v)
	}
	for _, v := range []string{"0", "false", "off", "no", "enabled"} {
		t.Setenv(EnvPrompts, v)
		assert.False(t, PromptsEnabled(), v)
	}
}

func TestPromptHistoryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	h := NewPromptHistory(dir)
	assert.Empty(t, h.Entries())

	require.NoError(t, h.Append("first prompt"))
	require.NoError(t, h.Append("second prompt"))
	assert.Equal(t, []string{"first prompt", "second prompt"}, NewPromptHistory(dir).Entries())
}

func TestPromptHistorySkipsBlankAndRepeats(t *testing.T) {
	h := NewPromptHistory(t.TempDir())
	require.NoError(t, h.Append("   "))
	require.NoError(t, h.Append("same"))
	require.NoError(t, h.Append("same"))
	require.NoError(t, h.Append("  same  "))
	assert.Equal(t, []string{"same"}, h.Entries())
}

func TestPromptHistoryKeepsNewestTen(t *testing.T) {
	h := NewPromptHistory(t.TempDir())
	for _, p := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"} {
		require.NoError(t, h.Append(p))
	}
	assert.Equal(t, []string{"3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}, h.Entries())
}

func TestPromptHistoryTruncatesLongEntry(t *testing.T) {
	h := NewPromptHistory(t.TempDir())
	require.NoError(t, h.Append(strings.Repeat("字", maxPromptBytes/len("字"))))
	entries := h.Entries()
	require.Len(t, entries, 1)
	// Cut on a rune boundary, so the length is a multiple of the rune width and
	// never exceeds the budget.
	assert.LessOrEqual(t, len(entries[0]), maxPromptBytes)
	assert.NotEmpty(t, entries[0])
	assert.Zero(t, len(entries[0])%len("字"))
	assert.True(t, strings.HasPrefix(entries[0], "字"))
	assert.True(t, utf8.ValidString(entries[0]))
}

func TestPromptHistorySurvivesCorruption(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, promptFile), []byte(`{"v":1,"prompts":[`), 0o600))
	h := NewPromptHistory(dir)
	assert.Empty(t, h.Entries())
	require.NoError(t, h.Append("recovered"))
	assert.Equal(t, []string{"recovered"}, NewPromptHistory(dir).Entries())
}

func TestPromptHistoryConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			h := NewPromptHistory(dir)
			for i := range 3 {
				require.NoError(t, h.Append(string(rune('a'+worker))+string(rune('0'+i))))
			}
		})
	}
	wg.Wait()
	// Twelve appends across four processes must all survive the shared lock and
	// be trimmed to the newest ten — no lost interleaved writes.
	entries := NewPromptHistory(dir).Entries()
	require.Len(t, entries, 10)
	unique := make(map[string]bool, len(entries))
	for _, e := range entries {
		unique[e] = true
	}
	assert.Len(t, unique, 10)
}

func TestPromptHistoryNoTempFilesLeft(t *testing.T) {
	dir := t.TempDir()
	h := NewPromptHistory(dir)
	require.NoError(t, h.Append("one"))
	require.NoError(t, h.Append("two"))
	matches, err := filepath.Glob(filepath.Join(dir, promptFile+".tmp*"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}
