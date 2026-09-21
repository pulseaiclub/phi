package chat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActiveBashFindsTheCommandAfterTheBang(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		cursor int
		query  string
		start  int
		end    int
		ok     bool
	}{
		{
			name: "whole command", value: "!git s", cursor: 6,
			query: "git s", start: 1, end: 6, ok: true,
		},
		{
			name: "cursor mid-command", value: "!git status", cursor: 3,
			query: "gi", start: 1, end: 3, ok: true,
		},
		{
			name: "leading spaces before the bang", value: "  !git", cursor: 6,
			query: "git", start: 3, end: 6, ok: true,
		},
		{
			name: "bang alone is still bash mode", value: "!", cursor: 1,
			query: "", start: 1, end: 1, ok: true,
		},
		{
			name: "multi-line command", value: "!for f in *; do\necho $f\ndone", cursor: 28,
			query: "for f in *; do\necho $f\ndone", start: 1, end: 28, ok: true,
		},
		{
			name: "cursor before the bang", value: "!git", cursor: 0,
			ok: false,
		},
		{
			name: "plain prompt text", value: "git status", cursor: 10,
			ok: false,
		},
		{
			name: "bang not at the start", value: "echo hi!", cursor: 8,
			ok: false,
		},
		{
			name: "empty value", value: "", cursor: 0,
			ok: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, start, end, ok := ActiveBash(tt.value, tt.cursor)

			assert.Equal(t, tt.ok, ok)
			if !tt.ok {
				return
			}
			assert.Equal(t, tt.query, query)
			assert.Equal(t, tt.start, start)
			assert.Equal(t, tt.end, end)
			assert.Equal(t, tt.value[start:end], query, "the range is what accept replaces")
		})
	}
}

func TestActiveBashClampsTheCursor(t *testing.T) {
	query, start, end, ok := ActiveBash("!git", 999)

	assert.True(t, ok)
	assert.Equal(t, "git", query)
	assert.Equal(t, 1, start)
	assert.Equal(t, 4, end)

	_, _, _, ok = ActiveBash("!git", -5)
	assert.False(t, ok, "a cursor before the bang is not in the command")
}

func TestNotifyCompletersReportsBashMode(t *testing.T) {
	var active []bool
	var queries []string
	c := &ChatInput{OnBashChange: func(isActive bool, query string) {
		active = append(active, isActive)
		queries = append(queries, query)
	}}

	c.Value, c.Cursor = "!git s", len("!git s")
	c.notifyCompleters()
	c.Value, c.Cursor = "git s", len("git s")
	c.notifyCompleters()

	assert.Equal(t, []bool{true, false}, active)
	assert.Equal(t, []string{"git s", ""}, queries)
}

func TestBashModeDefersNavigationKeysToThePicker(t *testing.T) {
	// Up/Down/Tab/Enter belong to the picker while it is open; without this the
	// composer would move the cursor or submit underneath it.
	c := &ChatInput{}

	assert.False(t, c.completerOpen())
	c.BashOpen = true
	assert.True(t, c.completerOpen())
}
