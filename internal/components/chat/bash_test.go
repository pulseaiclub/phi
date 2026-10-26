package chat

import (
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/components"
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

func TestActiveBashModeTreatsTheWholeBufferAsCommand(t *testing.T) {
	query, start, ok := ActiveBashMode("git status", len("git status"), true)

	assert.True(t, ok)
	assert.Equal(t, "git status", query)
	assert.Equal(t, 0, start, "shell mode has no bang: the buffer starts at 0")
}

func TestActiveBashModeAcceptsAnEmptyBuffer(t *testing.T) {
	query, start, ok := ActiveBashMode("", 0, true)

	assert.True(t, ok, "an empty shell-mode buffer is an empty command")
	assert.Empty(t, query)
	assert.Equal(t, 0, start)
}

func TestActiveBashModeRejectsBangsLikeShellMode(t *testing.T) {
	query, start, ok := ActiveBashMode("!ls", 3, true)

	assert.True(t, ok, "the bang is literal text in shell mode, not a marker")
	assert.Equal(t, "!ls", query)
	assert.Equal(t, 0, start)
}

func TestActiveBashModeWithoutShellModeDelegates(t *testing.T) {
	_, _, ok := ActiveBashMode("git status", len("git status"), false)

	assert.False(t, ok, "without shell mode the bang rules still apply")

	query, start, ok := ActiveBashMode("!git st", len("!git st"), false)
	assert.True(t, ok)
	assert.Equal(t, "git st", query)
	assert.Equal(t, 1, start)
}

// A bare "!" on an empty buffer must stay unconsumed: the shell-mode toggle
// lives in the composer pane one level up, and it only sees keys the input
// lets bubble. With text on the buffer "!" is a literal character again.
func TestBareBangOnEmptyBufferBubblesToThePane(t *testing.T) {
	c := &ChatInput{MinBodyRows: 3}
	ctx := &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})

	assert.Empty(t, c.Value, "the bang is left for the pane, not inserted")
	assert.False(t, ctx.Consume, "an unconsumed key is what bubbles")

	c.Value, c.Cursor = "echo", len("echo")
	ctx = &components.EventContext{}
	c.Handle(ctx, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})

	assert.Equal(t, "echo!", c.Value, "with text the bang is literal")
	assert.True(t, ctx.Consume)
}
