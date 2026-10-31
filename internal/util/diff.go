package util

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// DiffResult holds a unified diff and the first changed line.
type DiffResult struct {
	Diff             string
	FirstChangedLine int
}

type change struct {
	OldIdx int
	NewIdx int
	Kind   string
	line   string
}

type hunk struct {
	start int
	end   int
}

// splitLines splits content into display lines and reports whether the
// content ends with a newline. A trailing newline is a property of the last
// line, not an extra empty line, so it is not split off.
func splitLines(content string) ([]string, bool) {
	if content == "" {
		return []string{}, false
	}
	endsNL := strings.HasSuffix(content, "\n")
	lines := strings.Split(content, "\n")
	if endsNL {
		lines = lines[:len(lines)-1]
	}
	return lines, endsNL
}

// GenerateDiffString computes a unified diff between oldContent and newContent.
func GenerateDiffString(oldContent, newContent string, contextLines int) DiffResult {
	oldLines, oldEndsNL := splitLines(oldContent)
	newLines, newEndsNL := splitLines(newContent)

	// When the only difference is the trailing newline, git rewrites the last
	// line and marks the side that lacks the newline. Otherwise a trailing
	// newline is represented as an phantom empty line after the last content
	// line, matching git diff semantics.
	trailingOnly := slices.Equal(oldLines, newLines) && oldEndsNL != newEndsNL

	if !trailingOnly && oldEndsNL != newEndsNL {
		// A trailing newline is represented as a phantom empty line after the
		// last content line. When the new side is empty, the old side's trailing
		// newline is still emitted as a phantom line because the entire old
		// content (including its newline) is being removed. When the old side is
		// empty, the new side's content is being added wholesale, so its newline
		// is part of that added line and is not emitted separately.
		oldHasPhantom := oldEndsNL && len(oldLines) > 0 && oldLines[len(oldLines)-1] != ""
		newHasPhantom := newEndsNL && len(oldLines) > 0 && len(newLines) > 0 && newLines[len(newLines)-1] != ""
		if oldHasPhantom {
			oldLines = append(oldLines, "")
		}
		if newHasPhantom {
			newLines = append(newLines, "")
		}
	}

	changes := computeChanges(oldLines, newLines)
	diff := generateUnifiedDiffFromChanges(changes, contextLines)
	firstChangedLine := computeFirstChangedLine(changes)

	if diff == "" && trailingOnly && len(oldLines) > 0 {
		diff, firstChangedLine = trailingNewlineDiff(oldLines, oldEndsNL, newEndsNL)
	}

	return DiffResult{
		Diff:             diff,
		FirstChangedLine: firstChangedLine,
	}
}

// GenerateFileDiff returns a unified diff with ---/+++ headers.
func GenerateFileDiff(filePath, oldContent, newContent string, contextLines int) string {
	result := GenerateDiffString(oldContent, newContent, contextLines)
	baseName := filepath.Base(filePath)
	header := fmt.Sprintf("--- a/%s\n", baseName)
	header += fmt.Sprintf("+++ b/%s\n", baseName)
	return header + result.Diff
}

func generateUnifiedDiffFromChanges(changes []change, contextLines int) string {
	if len(changes) == 0 {
		return ""
	}

	var diff strings.Builder
	var hunks []hunk
	for idx := range changes {
		if changes[idx].Kind != "equal" {
			start := max(0, idx-contextLines)
			end := min(len(changes)-1, idx+contextLines)
			if len(hunks) == 0 || start > hunks[len(hunks)-1].end+1 {
				hunks = append(hunks, hunk{start: start, end: end})
			} else if end > hunks[len(hunks)-1].end {
				hunks[len(hunks)-1].end = end
			}
		}
	}

	for _, h := range hunks {
		hunkChanges := changes[h.start : h.end+1]

		hasOldLines, hasNewLines := false, false
		for _, c := range hunkChanges {
			if (c.Kind == "delete" || c.Kind == "equal") && c.OldIdx >= 0 {
				hasOldLines = true
			}
			if (c.Kind == "add" || c.Kind == "equal") && c.NewIdx >= 0 {
				hasNewLines = true
			}
		}

		oldStart, oldCount := 1, 0
		newStart, newCount := 1, 0

		switch {
		case hasOldLines && hasNewLines:
			for _, c := range hunkChanges {
				if (c.Kind == "delete" || c.Kind == "equal") && c.OldIdx >= 0 {
					if oldCount == 0 {
						oldStart = c.OldIdx + 1
					}
					oldCount++
				}
				if (c.Kind == "add" || c.Kind == "equal") && c.NewIdx >= 0 {
					if newCount == 0 {
						newStart = c.NewIdx + 1
					}
					newCount++
				}
			}
		case !hasOldLines && hasNewLines:
			for _, c := range hunkChanges {
				if c.Kind == "add" && c.NewIdx >= 0 {
					if newCount == 0 {
						newStart = c.NewIdx + 1
					}
					newCount++
				}
			}
			// A zero-length old range is anchored on the old line that
			// precedes the hunk, so its start is the number of old lines
			// before the insertion (0 at the top of the file).
			anchorOld := 0
			for i := h.start - 1; i >= 0; i-- {
				if changes[i].OldIdx >= 0 {
					anchorOld = changes[i].OldIdx + 1
					break
				}
			}
			oldStart = anchorOld
			oldCount = 0
		case hasOldLines && !hasNewLines:
			for _, c := range hunkChanges {
				if c.OldIdx >= 0 {
					if oldCount == 0 {
						oldStart = c.OldIdx + 1
					}
					oldCount++
				}
			}
			// Same anchor rule for the new side: with no new lines in the
			// hunk, the start counts the new lines before it.
			anchorNew := 0
			for i := h.start - 1; i >= 0; i-- {
				if changes[i].NewIdx >= 0 {
					anchorNew = changes[i].NewIdx + 1
					break
				}
			}
			newStart = anchorNew
			newCount = 0
		}

		fmt.Fprintf(&diff, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)

		for _, c := range hunkChanges {
			switch c.Kind {
			case "equal":
				fmt.Fprintf(&diff, " %s\n", c.line)
			case "delete":
				fmt.Fprintf(&diff, "-%s\n", c.line)
			case "add":
				fmt.Fprintf(&diff, "+%s\n", c.line)
			}
		}
	}

	return diff.String()
}

// trailingNewlineDiff renders a trailing-newline-only change the way git
// does: the last line is rewritten and the side that lacks the final
// newline carries the "\ No newline at end of file" marker.
func trailingNewlineDiff(lines []string, oldEndsNL, newEndsNL bool) (string, int) {
	last := len(lines)
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", last, 1, last, 1)
	fmt.Fprintf(&b, "-%s\n", lines[last-1])
	if !oldEndsNL {
		b.WriteString("\\ No newline at end of file\n")
	}
	fmt.Fprintf(&b, "+%s\n", lines[last-1])
	if !newEndsNL {
		b.WriteString("\\ No newline at end of file\n")
	}
	return b.String(), last
}

func computeChanges(oldLines, newLines []string) []change {
	n, m := len(oldLines), len(newLines)
	if n == 0 && m == 0 {
		return nil
	}

	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	var changes []change
	var backtrack func(i, j int)
	backtrack = func(i, j int) {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			backtrack(i-1, j-1)
			changes = append(changes, change{
				Kind: "equal", line: oldLines[i-1], OldIdx: i - 1, NewIdx: j - 1,
			})
			return
		}
		if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			backtrack(i, j-1)
			changes = append(changes, change{
				Kind: "add", line: newLines[j-1], OldIdx: -1, NewIdx: j - 1,
			})
			return
		}
		if i > 0 && (j == 0 || dp[i][j-1] < dp[i-1][j]) {
			backtrack(i-1, j)
			changes = append(changes, change{
				Kind: "delete", line: oldLines[i-1], OldIdx: i - 1, NewIdx: -1,
			})
			return
		}
	}
	backtrack(n, m)
	return changes
}

func computeFirstChangedLine(changes []change) int {
	for i, c := range changes {
		if c.Kind != "equal" {
			if c.NewIdx >= 0 {
				return c.NewIdx + 1
			}
			for j := i + 1; j < len(changes); j++ {
				if changes[j].NewIdx >= 0 {
					return changes[j].NewIdx + 1
				}
			}
			return 0
		}
	}
	return 0
}
