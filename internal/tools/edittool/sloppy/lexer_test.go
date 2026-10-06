package sloppy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// collect runs the lexer over input and returns every token it produced.
func collect(t *testing.T, input string) []Token {
	t.Helper()
	var toks []Token
	lx := NewLexer(input)
	for lx.Next() {
		toks = append(toks, lx.Token())
	}
	return toks
}

func TestLexer(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Token
	}{
		{
			name:  "recognizes section headers",
			input: "*** SM:EDIT src/a.ts all\n*** SM:FIND\nx\n*** SM:PUT\ny\n*** SM:AFTER\nz\n",
			want: []Token{
				{Kind: TokenEdit, Line: 1, Text: "src/a.ts all", Path: "src/a.ts", All: true},
				{Kind: TokenFind, Line: 2},
				{Kind: TokenText, Line: 3, Text: "x"},
				{Kind: TokenPut, Line: 4},
				{Kind: TokenText, Line: 5, Text: "y"},
				{Kind: TokenAfter, Line: 6},
				{Kind: TokenText, Line: 7, Text: "z"},
			},
		},
		{
			name:  "bare edit header continues the current section",
			input: "*** SM:EDIT\n",
			want:  []Token{{Kind: TokenEdit, Line: 1}},
		},
		{
			name:  "all flag without a path",
			input: "*** SM:EDIT all\n",
			want:  []Token{{Kind: TokenEdit, Line: 1, Text: "all", All: true}},
		},
		{
			name:  "path only",
			input: "*** SM:EDIT a.ts\n",
			want:  []Token{{Kind: TokenEdit, Line: 1, Text: "a.ts", Path: "a.ts"}},
		},
		{
			name:  "path with all flag",
			input: "*** SM:EDIT a.ts all\n",
			want:  []Token{{Kind: TokenEdit, Line: 1, Text: "a.ts all", Path: "a.ts", All: true}},
		},
		{
			name:  "path ending in the word all is not a flag",
			input: "*** SM:EDIT call.ts\n",
			want:  []Token{{Kind: TokenEdit, Line: 1, Text: "call.ts", Path: "call.ts"}},
		},
		{
			name:  "quoted path may contain the word all",
			input: `*** SM:EDIT "my file all.ts" all` + "\n",
			want: []Token{{
				Kind: TokenEdit,
				Line: 1,
				Text: `"my file all.ts" all`,
				Path: "my file all.ts",
				All:  true,
			}},
		},
		{
			name:  "empty quoted path falls back to body text",
			input: `*** SM:EDIT ""` + "\n",
			want:  []Token{{Kind: TokenText, Line: 1, Text: `*** SM:EDIT ""`}},
		},
		{
			name:  "malformed quote falls back to body text",
			input: `*** SM:EDIT "unterminated` + "\n",
			want:  []Token{{Kind: TokenText, Line: 1, Text: `*** SM:EDIT "unterminated`}},
		},
		{
			name:  "stray argument falls back to body text",
			input: "*** SM:PUT oops\n",
			want:  []Token{{Kind: TokenText, Line: 1, Text: "*** SM:PUT oops"}},
		},
		{
			// Envelope lines vanish and the end sentinel skips the trailer, but
			// body content between envelope lines passes through untouched.
			name: "strips a foreign patch envelope and its trailer",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n" +
				"*** Begin Patch\n*** Update File: b.ts\nnoise\n*** End Patch\ntrailer prose\n" +
				"*** SM:EDIT c.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n",
			want: []Token{
				{Kind: TokenEdit, Line: 1, Text: "a.ts", Path: "a.ts"},
				{Kind: TokenFind, Line: 2},
				{Kind: TokenText, Line: 3, Text: "x"},
				{Kind: TokenPut, Line: 4},
				{Kind: TokenText, Line: 5, Text: "y"},
				{Kind: TokenText, Line: 8, Text: "noise"},
				{Kind: TokenEdit, Line: 11, Text: "c.ts", Path: "c.ts"},
				{Kind: TokenFind, Line: 12},
				{Kind: TokenText, Line: 13, Text: "x"},
				{Kind: TokenPut, Line: 14},
				{Kind: TokenText, Line: 15, Text: "y"},
			},
		},
		{
			name:  "keeps after bodies raw",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:AFTER\n*** Begin Patch\nliteral\n",
			want: []Token{
				{Kind: TokenEdit, Line: 1, Text: "a.ts", Path: "a.ts"},
				{Kind: TokenFind, Line: 2},
				{Kind: TokenText, Line: 3, Text: "x"},
				{Kind: TokenAfter, Line: 4},
				{Kind: TokenText, Line: 5, Text: "*** Begin Patch"},
				{Kind: TokenText, Line: 6, Text: "literal"},
			},
		},
		{
			name:  "merges an end sentinel split across two lines",
			input: "*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n***\nEnd of patch\nstray\n",
			want: []Token{
				{Kind: TokenEdit, Line: 1, Text: "a.ts", Path: "a.ts"},
				{Kind: TokenFind, Line: 2},
				{Kind: TokenText, Line: 3, Text: "x"},
				{Kind: TokenPut, Line: 4},
				{Kind: TokenText, Line: 5, Text: "y"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, collect(t, tt.input))
		})
	}
}
