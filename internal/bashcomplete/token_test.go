package bashcomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSegment(t *testing.T) {
	for _, tt := range []struct {
		name   string
		line   string
		want   string
		offset int
	}{
		{"one command is its own segment", "git status", "git status", 0},
		{"&& starts the next command", "cd doc && git sta", "git sta", 10},
		{"|| starts it too", "make || go test", "go test", 8},
		{"a pipe starts it", "ls | grep go", "grep go", 5},
		{"a semicolon starts it", "cd doc; git sta", "git sta", 8},
		{"a lone & starts it", "serve & git sta", "git sta", 8},
		{"the spaces after a separator belong to neither", "cd doc &&   git sta", "git sta", 12},
		{"a separator inside quotes is text", `echo "a && b" git`, `echo "a && b" git`, 0},
		{"a separator inside a substitution is text", "echo $(a && b) gi", "echo $(a && b) gi", 0},
		{"a trailing separator leaves an empty segment", "git status &&", "", 13},
	} {
		t.Run(tt.name, func(t *testing.T) {
			segment, offset := Segment(tt.line)
			assert.Equal(t, tt.want, segment)
			assert.Equal(t, tt.offset, offset)
		})
	}
}

func TestTokenize(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		want []Token
	}{
		{"empty line is one empty word", "", []Token{{Text: "", Start: 0}}},
		{"one word", "git", []Token{{Text: "git", Start: 0}}},
		{"words carry their offsets", "git status", []Token{
			{Text: "git", Start: 0, Complete: true},
			{Text: "status", Start: 4},
		}},
		{"trailing space adds the next word", "git ", []Token{
			{Text: "git", Start: 0, Complete: true},
			{Text: "", Start: 4},
		}},
		{"leading and doubled spaces are skipped", "  git  st ", []Token{
			{Text: "git", Start: 2, Complete: true},
			{Text: "st", Start: 7, Complete: true},
			{Text: "", Start: 10},
		}},
		{"quoted words stay whole", "git add 'my file'", []Token{
			{Text: "git", Start: 0, Complete: true},
			{Text: "add", Start: 4, Complete: true},
			{Text: "'my file'", Start: 8},
		}},
		{"double quotes hold a word too", `echo "a b"`, []Token{
			{Text: "echo", Start: 0, Complete: true},
			{Text: `"a b"`, Start: 5},
		}},
		{"newline separates words", "for f in *\ndo", []Token{
			{Text: "for", Start: 0, Complete: true},
			{Text: "f", Start: 4, Complete: true},
			{Text: "in", Start: 6, Complete: true},
			{Text: "*", Start: 9, Complete: true},
			{Text: "do", Start: 11},
		}},
		{"a trailing separator closes the last word", "git status;", []Token{
			{Text: "git", Start: 0, Complete: true},
			{Text: "status;", Start: 4, Complete: true},
			{Text: "", Start: 11},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Tokenize(tt.line))
		})
	}
}

func TestWordEnd(t *testing.T) {
	for _, tt := range []struct {
		name   string
		line   string
		cursor int
		want   int
	}{
		{"the cursor past the word ends it there", "git commit", 10, 10},
		{"the cursor inside a word takes the rest", "git commit -m", 8, 10},
		{"a space ends the word", "git commit -m", 4, 10},
		{"a separator ends it", "git commit&&", 8, 10},
		{"a quoted word ends at its closing quote", `git "a b" x`, 6, 9},
		{"a word after a quoted one ends at the next space", `git "a b" x`, 10, 11},
		{"the cursor clamps to the line", "git", 99, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, WordEnd(tt.line, tt.cursor))
		})
	}
}
