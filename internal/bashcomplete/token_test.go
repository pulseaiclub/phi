package bashcomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTokenize(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		want []Token
	}{
		{"empty line is one empty word", "", []Token{{Text: "", Start: 0}}},
		{"one word", "git", []Token{{Text: "git", Start: 0}}},
		{"words carry their offsets", "git status", []Token{
			{Text: "git", Start: 0},
			{Text: "status", Start: 4},
		}},
		{"trailing space adds the next word", "git ", []Token{
			{Text: "git", Start: 0},
			{Text: "", Start: 4},
		}},
		{"leading and doubled spaces are skipped", "  git  st ", []Token{
			{Text: "git", Start: 2},
			{Text: "st", Start: 7},
			{Text: "", Start: 10},
		}},
		{"quoted words stay whole", "git add 'my file'", []Token{
			{Text: "git", Start: 0},
			{Text: "add", Start: 4},
			{Text: "'my file'", Start: 8},
		}},
		{"double quotes hold a word too", `echo "a b"`, []Token{
			{Text: "echo", Start: 0},
			{Text: `"a b"`, Start: 5},
		}},
		{"newline separates words", "for f in *\ndo", []Token{
			{Text: "for", Start: 0},
			{Text: "f", Start: 4},
			{Text: "in", Start: 6},
			{Text: "*", Start: 9},
			{Text: "do", Start: 11},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Tokenize(tt.line))
		})
	}
}
