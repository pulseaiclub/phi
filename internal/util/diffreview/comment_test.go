package diffreview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCommentIndexResolvesSingleLineComment(t *testing.T) {
	rows := []Row{
		{Kind: RowAdd, Code: "one", Review: Anchor{Path: "main.go", Line: 1, Side: SideRight}},
		{Kind: RowAdd, Code: "two", Review: Anchor{Path: "main.go", Line: 2, Side: SideRight}},
	}
	drafts := []CommentDraft{{Path: "main.go", Line: 2, Side: SideRight, Body: "looks good"}}

	idx := BuildCommentIndex(rows, drafts)
	got := idx.DraftsForRow(1)
	require.Len(t, got, 1)
	assert.Equal(t, "looks good", got[0].Body)
	assert.Equal(t, []int{1}, idx.TargetRows())
}

func TestBuildCommentIndexResolvesLeftSideComment(t *testing.T) {
	rows := []Row{
		{Kind: RowDelete, Code: "old", Review: Anchor{Path: "main.go", Line: 7, Side: SideLeft}},
		{Kind: RowAdd, Code: "new", Review: Anchor{Path: "main.go", Line: 8, Side: SideRight}},
	}
	drafts := []CommentDraft{{Path: "main.go", Line: 7, Side: SideLeft, Body: "left note"}}

	idx := BuildCommentIndex(rows, drafts)
	got := idx.DraftsForRow(0)
	require.Len(t, got, 1)
	assert.Equal(t, SideLeft, got[0].Side)
}

func TestBuildCommentIndexSkipsUnmatchedComments(t *testing.T) {
	rows := []Row{{Kind: RowAdd, Code: "one", Review: Anchor{Path: "main.go", Line: 1, Side: SideRight}}}
	drafts := []CommentDraft{
		{Path: "other.go", Line: 1, Side: SideRight, Body: "wrong path"},
		{Path: "main.go", Line: 2, Side: SideRight, Body: "wrong line"},
	}

	idx := BuildCommentIndex(rows, drafts)
	assert.Empty(t, idx.TargetRows())
}

func TestFormatPrompt(t *testing.T) {
	out := FormatPrompt([]CommentDraft{
		{Path: "a.go", Line: 4, Side: SideRight, Body: "rename this"},
		{Path: "b.go", Line: 1, Body: "   "},
	})
	assert.Contains(t, out, "a.go:4 (RIGHT)")
	assert.Contains(t, out, "rename this")
	assert.NotContains(t, out, "b.go")
}

func TestGitArgv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt")

	argv := func(spec ...string) []string {
		t.Helper()
		got, err := GitArgv(dir, spec)
		require.NoError(t, err)
		return got
	}

	assert.Equal(t, []string{"git", "-c", "color.ui=never", "diff"}, argv())
	assert.Equal(t, []string{"git", "-c", "color.ui=never", "diff", "--staged"}, argv("staged"))
	assert.Equal(
		t,
		[]string{"git", "-c", "color.ui=never", "show", "--pretty=medium", "-p", "HEAD"},
		argv("HEAD"),
	)
	assert.Equal(t, []string{"git", "-c", "color.ui=never", "diff", "main"}, argv("main"))
	assert.Equal(t, []string{"git", "show", "HEAD~1"}, argv("--", "git", "show", "HEAD~1"))

	// `@` is the composer's completion prefix, not a revision.
	assert.Equal(t, []string{"git", "-c", "color.ui=never", "diff", "--", "a.txt"}, argv("@a.txt"))
	// HEAD plus a path diffs that path against HEAD; it used to fall into the
	// `git show` branch above and print the whole commit.
	assert.Equal(
		t,
		[]string{"git", "-c", "color.ui=never", "diff", "HEAD", "--", "a.txt"},
		argv("HEAD", "@a.txt"),
	)
	assert.Equal(
		t,
		[]string{"git", "-c", "color.ui=never", "diff", "--staged", "--", "a.txt"},
		argv("staged", "a.txt"),
	)
	assert.Equal(
		t,
		[]string{"git", "-c", "color.ui=never", "diff", "main..feat", "--", "a.txt"},
		argv("main..feat", "a.txt"),
	)

	_, err := GitArgv(dir, []string{"@nope.txt"})
	require.Error(t, err)
	assert.Equal(t, "no such path: nope.txt", err.Error())
}

func TestLabelForSpec(t *testing.T) {
	assert.Equal(t, "working tree", LabelForSpec(nil))
	assert.Equal(t, "staged", LabelForSpec([]string{"staged"}))
	assert.Equal(t, "HEAD", LabelForSpec([]string{"HEAD"}))
	// The title should read like the command, not like the raw completion token.
	assert.Equal(t, "a.txt", LabelForSpec([]string{"@a.txt"}))
	assert.Equal(t, "HEAD a.txt", LabelForSpec([]string{"HEAD", "--", "@a.txt"}))
}

func TestEmptyNote(t *testing.T) {
	assert.Contains(t, EmptyNote("", nil), "No unstaged changes")
	assert.Contains(t, EmptyNote("", []string{"staged"}), "No staged changes")
	assert.Contains(t, EmptyNote("", []string{"HEAD"}), "HEAD has no changes")
	// A plain revision is compared against the working tree, so the note must
	// name it — otherwise an empty overlay looks like a broken /diff.
	assert.Contains(t, EmptyNote("", []string{"HEAD~2"}), "No changes vs HEAD~2")
	assert.Contains(t, EmptyNote("", []string{"HEAD~2"}), "Untracked files are not shown")
}

func writeFile(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644))
}
