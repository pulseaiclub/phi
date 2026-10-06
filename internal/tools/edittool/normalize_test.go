package edittool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeTextDropsWhitespaceAndFoldsPunctuation(t *testing.T) {
	got := normalizeText("\tconst  x = “a” ≠ b;\n")
	assert.Equal(t, `constx="a"!=b;`, got.text)
}

func TestNormalizeTextMapsNormalizedBytesBackToSource(t *testing.T) {
	source := "a\u00a0😀 b"
	got := normalizeText(source)

	require.Equal(t, "a😀b", got.text)
	assert.Equal(t, 0, got.sourceStart(0, -1))
	assert.Equal(t, 1, got.sourceEnd(1, -1))
	// The emoji occupies 4 source bytes but folds to itself byte-for-byte.
	emojiStart := got.sourceStart(1, -1)
	assert.Equal(t, 3, emojiStart)
	assert.Equal(t, 7, got.sourceEnd(5, -1))
	assert.Equal(t, "😀", source[emojiStart:got.sourceEnd(5, -1)])
	assert.Equal(t, 8, got.sourceStart(5, -1))
}

func TestNormalizeTextKeepsNonASCIICharactersVerbatim(t *testing.T) {
	got := normalizeText("中文 = 1")
	assert.Equal(t, "中文=1", got.text)
	require.Len(t, got.starts, len(got.text))
	for i, start := range got.starts {
		assert.Less(t, start, got.ends[i])
	}
}

func TestSourceOffsetsFallBackAtEnds(t *testing.T) {
	got := normalizeText("ab")
	assert.Equal(t, 42, got.sourceStart(len(got.text), 42))
	assert.Equal(t, 0, got.sourceEnd(0, 42))
	assert.Equal(t, 2, got.sourceEnd(len(got.text), 42))
}

func TestLevenshteinDistance(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{name: "identical", left: "kitten", right: "kitten", want: 0},
		{name: "classic", left: "kitten", right: "sitting", want: 3},
		{name: "empty against text", left: "", right: "abc", want: 3},
		{name: "astral counts once", left: "😀a", right: "😀b", want: 1},
		{name: "shared edges trimmed", left: "prefix-middle-suffix", right: "prefix-middl-suffix", want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, levenshteinDistance(tc.left, tc.right))
			assert.Equal(t, tc.want, levenshteinDistance(tc.right, tc.left))
		})
	}
}
