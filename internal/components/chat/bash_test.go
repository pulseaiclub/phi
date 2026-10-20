package chat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActiveBashReadsTheCommandBeforeTheCursor(t *testing.T) {
	query, start, ok := ActiveBash("!git st", len("!git st"))

	assert.True(t, ok)
	assert.Equal(t, "git st", query)
	assert.Equal(t, 1, start)
}

func TestActiveBashAllowsLeadingWhitespaceBeforeTheBang(t *testing.T) {
	query, start, ok := ActiveBash("  !ls", len("  !ls"))

	assert.True(t, ok)
	assert.Equal(t, "ls", query)
	assert.Equal(t, 3, start, "start points after the bang, past the leading whitespace")
}

func TestActiveBashAcceptsAnEmptyCommand(t *testing.T) {
	query, start, ok := ActiveBash("!", 1)

	assert.True(t, ok, "a bare bang is still ! mode: the predictor decides what to do")
	assert.Empty(t, query)
	assert.Equal(t, 1, start)
}

func TestActiveBashReadsAMidCommandCursor(t *testing.T) {
	query, start, ok := ActiveBash("!git status", 4)

	assert.True(t, ok)
	assert.Equal(t, "git", query)
	assert.Equal(t, 1, start)
}

func TestActiveBashRejectsNonBashLines(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		cursor int
	}{
		{"plain text", "git status", len("git status")},
		{"bang after text", "echo !", len("echo !")},
		{"cursor before the bang", "!ls", 0},
		{"whitespace before bang with early cursor", " !ls", 0},
		{"empty value", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := ActiveBash(tt.value, tt.cursor)
			assert.False(t, ok)
		})
	}
}

func TestActiveBashClampsAnOutOfRangeCursor(t *testing.T) {
	query, _, ok := ActiveBash("!ls", 99)

	assert.True(t, ok, "an oversized cursor is clamped, not rejected")
	assert.Equal(t, "ls", query)
}

func TestActiveBashSpansMultipleLines(t *testing.T) {
	value := "!for f in *; do\n  echo $f\ndone"
	query, start, ok := ActiveBash(value, len(value))

	assert.True(t, ok)
	assert.Equal(t, "for f in *; do\n  echo $f\ndone", query)
	assert.Equal(t, 1, start)
}

func TestActiveBashSkipsTheSecondBang(t *testing.T) {
	query, start, ok := ActiveBash("!!git st", len("!!git st"))

	assert.True(t, ok, "!! enters the same command mode")
	assert.Equal(t, "git st", query, "the command starts after both bangs")
	assert.Equal(t, 2, start)
}

func TestActiveBashAcceptsABareDoubleBang(t *testing.T) {
	query, start, ok := ActiveBash("!!", 2)

	assert.True(t, ok, "a bare !! is still command mode")
	assert.Empty(t, query)
	assert.Equal(t, 2, start)
}

func TestActiveBashRejectsCursorBetweenBangs(t *testing.T) {
	_, _, ok := ActiveBash("!!ls", 1)

	assert.False(t, ok, "the second bang is not command text")
}
