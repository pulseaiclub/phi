package sloppy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanPattern(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Pattern
	}{
		{
			name: "gaps are line-bounded only when text follows on their line",
			body: "a …b\n…\nc",
			want: Pattern{
				Tokens: []PatternToken{
					{Kind: PatternTokenLiteral, Text: "a ", Start: 0, End: 2},
					{Kind: PatternTokenGap, Capture: 0, LineBounded: true, Start: 2, End: 5},
					{Kind: PatternTokenLiteral, Text: "b\n", Start: 5, End: 7},
					{Kind: PatternTokenGap, Capture: 1, Start: 7, End: 10},
					{Kind: PatternTokenLiteral, Text: "\nc", Start: 10, End: 12},
				},
				Body: "a …b\n…\nc",
			},
		},
		{
			// Edge gaps take their joining newline with them.
			name: "drops edge gaps",
			body: "…\nfoo\n…",
			want: Pattern{
				Tokens:   []PatternToken{{Kind: PatternTokenLiteral, Text: "foo", Start: 4, End: 7}},
				EdgeGaps: EdgeGaps{Leading: true, Trailing: true},
				Body:     "…\nfoo\n…",
			},
		},
		{
			name: "renumbers captures after an edge drop",
			body: "…a…b…",
			want: Pattern{
				Tokens: []PatternToken{
					{Kind: PatternTokenLiteral, Text: "a", Start: 3, End: 4},
					{Kind: PatternTokenGap, Capture: 0, LineBounded: true, Start: 4, End: 7},
					{Kind: PatternTokenLiteral, Text: "b", Start: 7, End: 8},
				},
				EdgeGaps: EdgeGaps{Leading: true, Trailing: true},
				Body:     "…a…b…",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scanPattern(tt.body)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestScanPatternSelectionErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "stray close", body: "a ⟫ b", want: "stray"},
		{name: "unterminated", body: "a ⟪b│c", want: "unterminated"},
		{name: "nested open", body: "a ⟪b⟪c│d⟫", want: "nested"},
		{name: "missing divider", body: "a ⟪b⟫ c", want: "divider"},
		{name: "multiple dividers", body: "a ⟪b│c│d⟫", want: "multiple"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scanPattern(tt.body)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}
