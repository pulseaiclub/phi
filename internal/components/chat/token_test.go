package chat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActiveLeadingToken(t *testing.T) {
	tests := []struct {
		name   string
		prefix byte
		value  string
		cursor int
		ok     bool
		query  string
	}{
		{"empty input", '/', "", 0, false, ""},
		{"marker only slash", '/', "/", 1, true, ""},
		{"marker only question", '?', "?", 1, true, ""},
		{"slash prefix", '/', "/resu", 5, true, "resu"},
		{"question prefix", '?', "?ctrl", 5, true, "ctrl"},
		{"cursor at byte zero", '/', "/sessions", 0, false, ""},
		{"cursor before marker", '?', "?help", -4, false, ""},
		{"missing marker", '/', "hello", 5, false, ""},
		{"misplaced marker", '/', "hi /sessions", 12, false, ""},
		{"wrong marker", '?', "/sessions", 9, false, ""},
		{"cursor past end", '/', "/resume", 99, true, "resume"},
		{"cursor at space", '/', "/resume abc", 7, true, "resume"},
		{"cursor after space", '/', "/resume abc", 8, false, ""},
		{"cursor at tab", '?', "?help\tme", 5, true, "help"},
		{"cursor after tab", '?', "?help\tme", 6, false, ""},
		{"cursor at newline", '/', "/cmd\narg", 4, true, "cmd"},
		{"cursor after newline", '/', "/cmd\narg", 5, false, ""},
		{"cursor at carriage return", '/', "/cmd\rarg", 4, true, "cmd"},
		{"cursor after carriage return", '/', "/cmd\rarg", 5, false, ""},
		{"cursor inside token after whitespace", '?', "?help me", 7, false, ""},
		{"multi-byte query", '/', "/héllo", 7, true, "héllo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, start, end, ok := activeLeadingToken(tt.value, tt.cursor, tt.prefix)
			require.Equal(t, tt.ok, ok, "ok")
			if !ok {
				return
			}
			wantEnd := min(max(tt.cursor, 0), len(tt.value))
			assert.Equal(t, tt.query, query, "query")
			assert.Equal(t, 0, start, "start")
			assert.Equal(t, wantEnd, end, "end")
		})
	}
}
