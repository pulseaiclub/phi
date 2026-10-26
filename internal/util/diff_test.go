package util

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateDiffStringHunkHeaders(t *testing.T) {
	for _, tc := range []struct {
		name     string
		old      string
		new      string
		context  int
		expected string
	}{
		{
			name:    "insert into an empty file",
			old:     "",
			new:     "A\nB",
			context: 3,
			expected: "@@ -0,0 +1,2 @@\n" +
				"+A\n" +
				"+B\n",
		},
		{
			name:    "insert at the top of a file",
			old:     "A",
			new:     "B\nA",
			context: 0,
			expected: "@@ -0,0 +1,1 @@\n" +
				"+B\n",
		},
		{
			name:    "insert at the top keeps the following context",
			old:     "A",
			new:     "B\nA",
			context: 3,
			expected: "@@ -1,1 +1,2 @@\n" +
				"+B\n" +
				" A\n",
		},
		{
			name:    "delete every line",
			old:     "A\nB",
			new:     "",
			context: 3,
			expected: "@@ -1,2 +0,0 @@\n" +
				"-A\n" +
				"-B\n",
		},
		{
			name:    "delete the first line",
			old:     "A\nB",
			new:     "B",
			context: 0,
			expected: "@@ -1,1 +0,0 @@\n" +
				"-A\n",
		},
		{
			name:    "delete the last line with context",
			old:     "A\nB",
			new:     "A",
			context: 3,
			expected: "@@ -1,2 +1,1 @@\n" +
				" A\n" +
				"-B\n",
		},
		{
			name:    "append past the context window",
			old:     "A\nB",
			new:     "A\nB\nC\nD\nE",
			context: 3,
			expected: "@@ -1,2 +1,5 @@\n" +
				" A\n" +
				" B\n" +
				"+C\n" +
				"+D\n" +
				"+E\n",
		},
		{
			name:    "two hunks split by unchanged lines",
			old:     "A\nB\nC\nD\nE\nF\nG",
			new:     "a\nB\nC\nD\nE\nF\ng",
			context: 1,
			expected: "@@ -1,2 +1,2 @@\n" +
				"-A\n" +
				"+a\n" +
				" B\n" +
				"@@ -6,2 +6,2 @@\n" +
				" F\n" +
				"-G\n" +
				"+g\n",
		},
		{
			name:     "identical content produces no diff",
			old:      "A\nB",
			new:      "A\nB",
			context:  3,
			expected: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, GenerateDiffString(tc.old, tc.new, tc.context).Diff)
		})
	}
}

// TestGenerateDiffStringZeroLengthRangesAreAnchoredOnThePrecedingLine pins the
// unified diff rule for hunk sides that carry no lines: the start of a
// zero-length range counts the lines before the hunk on that side, so a change
// at the top of a file starts at 0.
func TestGenerateDiffStringZeroLengthRangesAreAnchoredOnThePrecedingLine(t *testing.T) {
	require.Equal(t, "@@ -0,0 +1,1 @@\n+B\n", GenerateDiffString("A", "B\nA", 0).Diff)
	require.Equal(t, "@@ -1,1 +0,0 @@\n-A\n", GenerateDiffString("A\nB", "B", 0).Diff)
	require.Equal(t, "@@ -0,0 +1,2 @@\n+A\n+B\n", GenerateDiffString("", "A\nB", 3).Diff)
	require.Equal(t, "@@ -1,2 +0,0 @@\n-A\n-B\n", GenerateDiffString("A\nB", "", 3).Diff)
}

func TestGenerateDiffStringFirstChangedLine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		old      string
		new      string
		expected int
	}{
		{"no change", "A\nB", "A\nB", 0},
		{"changed first line", "A\nB\nC", "a\nB\nC", 1},
		{"changed middle line", "A\nB\nC", "A\nb\nC", 2},
		{"appended line reports its own number", "A\nB", "A\nB\nC", 3},
		{"prepended line reports the first inserted line", "A\nB", "C\nA\nB", 1},
		{"deletion followed by a change", "A\nB\nC", "A\nc", 2},
		{"deletion of every line has no changed line", "A\nB", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, GenerateDiffString(tc.old, tc.new, 3).FirstChangedLine)
		})
	}
}

func TestGenerateFileDiffAddsHeaders(t *testing.T) {
	require.Equal(t,
		"--- a/file.go\n+++ b/file.go\n@@ -1,1 +1,2 @@\n A\n+B\n",
		GenerateFileDiff("/tmp/dir/file.go", "A\n", "A\nB\n", 3),
	)
	require.Equal(t, "--- a/file.go\n+++ b/file.go\n", GenerateFileDiff("dir/file.go", "A", "A", 3))
}

// TestGenerateDiffStringTrailingNewline pins the git semantics for
// trailing-newline-only differences: the last line is rewritten and the side
// that lacks the final newline carries the "\ No newline at end of file"
// marker, instead of reporting a phantom empty-line deletion.
func TestGenerateDiffStringTrailingNewline(t *testing.T) {
	t.Run("removing the final newline rewrites the last line", func(t *testing.T) {
		require.Equal(t,
			"@@ -1,1 +1,1 @@\n-b\n+b\n\\ No newline at end of file\n",
			GenerateDiffString("b\n", "b", 3).Diff,
		)
	})
	t.Run("adding the final newline rewrites the last line", func(t *testing.T) {
		require.Equal(t,
			"@@ -1,1 +1,1 @@\n-b\n\\ No newline at end of file\n+b\n",
			GenerateDiffString("b", "b\n", 3).Diff,
		)
	})
	t.Run("last line is the one rewritten", func(t *testing.T) {
		require.Equal(t,
			"@@ -2,1 +2,1 @@\n-b\n+b\n\\ No newline at end of file\n",
			GenerateDiffString("a\nb\n", "a\nb", 3).Diff,
		)
	})
	t.Run("identical content with a final newline yields no diff", func(t *testing.T) {
		require.Empty(t, GenerateDiffString("a\nb\n", "a\nb\n", 3).Diff)
	})
	t.Run("first changed line is the rewritten line", func(t *testing.T) {
		require.Equal(t, 2, GenerateDiffString("a\nb\n", "a\nb", 3).FirstChangedLine)
	})
	t.Run("empty file gaining a trailing newline", func(t *testing.T) {
		require.Equal(t,
			"@@ -0,0 +1,1 @@\n+\n",
			GenerateDiffString("", "\n", 3).Diff,
		)
	})
	t.Run("empty file losing a trailing newline", func(t *testing.T) {
		require.Equal(t,
			"@@ -1,1 +0,0 @@\n-\n",
			GenerateDiffString("\n", "", 3).Diff,
		)
	})
	t.Run("content change plus trailing newline change keeps both changes", func(t *testing.T) {
		// When the content also differs we fall back to the normal diff path,
		// so the trailing-newline marker is not emitted yet.
		require.Equal(t,
			"@@ -1,1 +1,2 @@\n-hello\n+hello world\n+\n",
			GenerateDiffString("hello", "hello world\n", 3).Diff,
		)
	})
	t.Run("deleting a single line also removes its trailing newline", func(t *testing.T) {
		require.Equal(t,
			"@@ -1,2 +0,0 @@\n-A\n-\n",
			GenerateDiffString("A\n", "", 3).Diff,
		)
	})
	t.Run("replacing a line with one that lacks a newline", func(t *testing.T) {
		require.Equal(t,
			"@@ -1,2 +1,1 @@\n-A\n-\n+B\n",
			GenerateDiffString("A\n", "B", 3).Diff,
		)
	})
	t.Run("replacing a line with one that has a trailing newline", func(t *testing.T) {
		require.Equal(t,
			"@@ -1,1 +1,2 @@\n-A\n+B\n+\n",
			GenerateDiffString("A", "B\n", 3).Diff,
		)
	})
	t.Run("single trailing newline is not an extra phantom empty line", func(t *testing.T) {
		require.Equal(t,
			"@@ -1,1 +0,0 @@\n-\n",
			GenerateDiffString("\n", "", 3).Diff,
		)
	})
}

// TestGenerateDiffStringAppliesBack is the invariant every unified diff has to
// satisfy: applying the emitted hunks to the old content reproduces the new
// content, with the line ranges the headers advertise.
func TestGenerateDiffStringAppliesBack(t *testing.T) {
	sequences := allLineSequences([]string{"A", "B"}, 3)
	for _, oldLines := range sequences {
		for _, newLines := range sequences {
			for _, context := range []int{0, 1, 3} {
				oldContent := strings.Join(oldLines, "\n")
				newContent := strings.Join(newLines, "\n")
				label := fmt.Sprintf("old=%q new=%q context=%d", oldContent, newContent, context)
				result := GenerateDiffString(oldContent, newContent, context)

				if oldContent == newContent {
					require.Empty(t, result.Diff, label)
					continue
				}
				require.NotEmpty(t, result.Diff, label)
				require.Equal(t, newContent, applyDiff(t, label, oldLines, result.Diff), label)
			}
		}
	}
}

func allLineSequences(alphabet []string, maxLen int) [][]string {
	out := [][]string{{}}
	for n := 1; n <= maxLen; n++ {
		indexes := make([]int, n)
		for {
			lines := make([]string, n)
			for i, v := range indexes {
				lines[i] = alphabet[v]
			}
			out = append(out, lines)
			pos := n - 1
			for pos >= 0 {
				indexes[pos]++
				if indexes[pos] < len(alphabet) {
					break
				}
				indexes[pos] = 0
				pos--
			}
			if pos < 0 {
				break
			}
		}
	}
	return out
}

// applyDiff replays the hunks of diff onto oldLines, checking that each header
// agrees with the lines its body carries. Content is a "\n" join of its lines,
// which is how GenerateDiffString splits it, so the replay returns lines too.
func applyDiff(t *testing.T, label string, oldLines []string, diff string) string {
	t.Helper()
	var out []string
	cursor := 0
	for _, h := range parseDiffHunks(t, label, diff) {
		// A zero-length range is anchored on the line after which the hunk
		// goes, so it holds one more line than a range that carries lines.
		start := h.oldStart - 1
		if h.oldCount == 0 {
			start = h.oldStart
		}
		require.GreaterOrEqual(t, start, cursor, "%s: hunk at %d overlaps the previous hunk", label, h.oldStart)
		for ; cursor < start && cursor < len(oldLines); cursor++ {
			out = append(out, oldLines[cursor])
		}
		for _, line := range h.body {
			switch {
			case line[0] == ' ' || line[0] == '+':
				out = append(out, line[1:])
			case line[0] == '-':
				// Deletion: consume from old without emitting.
			case strings.HasPrefix(line, "\\"):
				// "\ No newline at end of file" is metadata, not a content line.
			default:
				require.Failf(t, "unexpected diff line", "%s: %q", label, line)
			}
		}
		cursor += h.oldCount
	}
	for ; cursor < len(oldLines); cursor++ {
		out = append(out, oldLines[cursor])
	}
	return strings.Join(out, "\n")
}

type diffHunk struct {
	oldStart, oldCount int
	newStart, newCount int
	body               []string
}

func parseDiffHunks(t *testing.T, label, diff string) []diffHunk {
	t.Helper()
	var hunks []diffHunk
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	for i := 0; i < len(lines); {
		require.True(t, strings.HasPrefix(lines[i], "@@"), "%s: expected a hunk header, got %q", label, lines[i])
		var oldRange, newRange string
		_, err := fmt.Sscanf(lines[i], "@@ -%s +%s @@", &oldRange, &newRange)
		require.NoError(t, err, "%s: malformed hunk header %q", label, lines[i])

		var h diffHunk
		h.oldStart, h.oldCount = parseDiffRange(oldRange)
		h.newStart, h.newCount = parseDiffRange(newRange)
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@") {
			require.NotEmpty(t, lines[i], "%s: hunk body has a line without a marker", label)
			require.Contains(t, " +-\\", lines[i][:1], "%s: unexpected marker in %q", label, lines[i])
			h.body = append(h.body, lines[i])
			i++
		}
		require.Equal(t, h.oldCount, countMarked(h.body, " -"), "%s: header advertises %d old lines", label, h.oldCount)
		require.Equal(t, h.newCount, countMarked(h.body, " +"), "%s: header advertises %d new lines", label, h.newCount)
		hunks = append(hunks, h)
	}
	return hunks
}

func parseDiffRange(s string) (int, int) {
	parts := strings.SplitN(s, ",", 2)
	start, _ := strconv.Atoi(parts[0])
	count := 1
	if len(parts) == 2 {
		count, _ = strconv.Atoi(parts[1])
	}
	return start, count
}

func countMarked(body []string, markers string) int {
	n := 0
	for _, line := range body {
		if strings.ContainsRune(markers, rune(line[0])) {
			n++
		}
	}
	return n
}
