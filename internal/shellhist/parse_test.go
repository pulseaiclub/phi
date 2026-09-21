package shellhist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReadsExtendedHistoryLinesAsCommands(t *testing.T) {
	got := Parse([]byte(": 1700000000:0;git status\n: 1700000001:5;npm test\n"))

	assert.Equal(t, []string{"git status", "npm test"}, got)
}

func TestParseJoinsBackslashContinuedLines(t *testing.T) {
	// zsh writes an embedded newline as a trailing backslash, so a command that
	// itself ends a line with a backslash appears as two of them.
	body := ": 1:0;gcloud foo \\\\\n  --reason=x && \\\\\namp threads continue T-1\n: 2:0;ls\n"

	got := Parse([]byte(body))

	assert.Equal(t, []string{"gcloud foo \\\n  --reason=x && \\\namp threads continue T-1", "ls"}, got)
}

func TestParseFallsBackToPlainHistoryLines(t *testing.T) {
	got := Parse([]byte("ls -la\ncd ..\n"))

	assert.Equal(t, []string{"ls -la", "cd .."}, got)
}

func TestParseSkipsBlankLinesAndTrailingWhitespace(t *testing.T) {
	got := Parse([]byte(": 1:0;echo hi   \n\n: 2:0;   \n"))

	assert.Equal(t, []string{"echo hi"}, got)
}

func TestParseSkipsBashTimestampMarkers(t *testing.T) {
	// With HISTTIMEFORMAT set, bash precedes each entry with a `#<epoch>` line;
	// the marker is not a command the user typed.
	got := Parse([]byte("#1700000000\ngit status\n#1700000001\nnpm test\n"))

	assert.Equal(t, []string{"git status", "npm test"}, got)
}

func TestParseKeepsDuplicatesInOrder(t *testing.T) {
	// Deduplication belongs to ranking, which keeps the newest occurrence.
	got := Parse([]byte("git status\nnpm test\ngit status\n"))

	assert.Equal(t, []string{"git status", "npm test", "git status"}, got)
}

func TestParseKeepsCommandsThatOnlyLookExtended(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"no separator", ": not a timestamp;git status"},
		{"non-numeric epoch", ": epoch:0;git status"},
		{"non-numeric duration", ": 1:dur;git status"},
		{"plain colon command", ":(){ :|:& };:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, []string{tt.line}, Parse([]byte(tt.line+"\n")))
		})
	}
}

func TestUnmetafyRestoresBytesZshEscaped(t *testing.T) {
	// "ü" is 0xC3 0xBC in UTF-8; zsh stores each byte as metaByte followed by
	// the byte XORed with 0x20.
	metafied := []byte{0x65, metaByte, 0xc3 ^ 0x20, metaByte, 0xbc ^ 0x20}

	assert.Equal(t, "eü", string(unmetafy(metafied)))
}

func TestParseRestoresMetafiedCommands(t *testing.T) {
	body := append([]byte(": 10:0;echo "), metaByte, 0xc3^0x20, metaByte, 0xbc^0x20)
	body = append(body, '\n')

	assert.Equal(t, []string{"echo ü"}, Parse(body))
}

func TestReadDecodesAMetafiedMultiLineFile(t *testing.T) {
	// Mirrors a real zsh file: a metafied byte pair, then a multi-line command.
	body := append([]byte(": 10:0;echo "), metaByte, 0xc3^0x20, metaByte, 0xbc^0x20)
	body = append(body, []byte("\n: 11:0;for f in *; do\\\n  echo $f\\\ndone\n")...)
	path := filepath.Join(t.TempDir(), "history")
	require.NoError(t, os.WriteFile(path, body, 0o600))

	got, err := Read(path)
	require.NoError(t, err)

	assert.Equal(t, []string{"echo ü", "for f in *; do\n  echo $f\ndone"}, got)
}

func TestReadReturnsAnErrorForAMissingFile(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "nope"))

	require.Error(t, err)
}

func TestReadReadsOnlyTheTailOfALongHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	// The ancient entry sits well outside the tail bound, so proving it is
	// absent also proves the file is not read in full.
	body := ": 1:0;ancient\n" + strings.Repeat("x", 2*tailBytes) + "\n: 9:0;recent\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	got, err := Read(path)
	require.NoError(t, err)

	assert.Equal(t, []string{"recent"}, got, "the ancient entry and the cut line are both dropped")
}

func TestTrimPartialEntryDropsTheCutLine(t *testing.T) {
	// A plain file has no entry marker, so only the partial line goes.
	got := trimPartialEntry([]byte("partial line\nfull line\n"))

	assert.Equal(t, "full line\n", string(got))
}

func TestTrimPartialEntrySkipsContinuationLinesOfACutEntry(t *testing.T) {
	// The cut lands after the first physical line of a multi-line entry, so the
	// continuation line that follows belongs to the dropped entry.
	body := []byte(": 1:0;cut \\\n  continued\n: 2:0;kept\n")

	got := trimPartialEntry(body)

	assert.Equal(t, ": 2:0;kept\n", string(got))
}

func TestTrimPartialEntryOfAnEmptyTail(t *testing.T) {
	assert.Empty(t, trimPartialEntry(nil))
}

func TestDefaultPathPrefersHistfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".bash_history"), nil, 0o600))
	configured := filepath.Join(home, "custom_history")
	t.Setenv("HISTFILE", configured)

	assert.Equal(t, configured, DefaultPath())
}

func TestDefaultPathFallsBackToAConventionalFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HISTFILE", "")

	assert.Empty(t, DefaultPath(), "nothing exists yet")

	require.NoError(t, os.WriteFile(filepath.Join(home, ".zsh_history"), nil, 0o600))
	assert.Equal(t, filepath.Join(home, ".zsh_history"), DefaultPath())

	require.NoError(t, os.WriteFile(filepath.Join(home, ".bash_history"), nil, 0o600))
	assert.Equal(t, filepath.Join(home, ".bash_history"), DefaultPath(), "bash wins when both exist")
}

func TestDefaultPathIgnoresADirectoryNamedLikeHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HISTFILE", "")
	require.NoError(t, os.Mkdir(filepath.Join(home, ".bash_history"), 0o700))

	assert.Empty(t, DefaultPath())
}
