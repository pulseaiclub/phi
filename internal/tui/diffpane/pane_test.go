package diffpane

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/util/diffreview"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

const sampleDiff = `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package main
-old
+new
`

func TestPaneOpenCommentSearchAndSend(t *testing.T) {
	var submitted string
	p := New(components.DefaultTheme(), t.TempDir(), func(s string) { submitted = s }, nil, nil)
	p.OpenText(sampleDiff, nil)
	require.True(t, p.Active())
	require.NotEmpty(t, p.rows)

	ctx := &components.EventContext{}
	key := func(r rune) {
		p.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
	}

	// land on the added line
	for i := 0; i < 20 && (p.cursor >= len(p.rows) || p.rows[p.cursor].Code != "new"); i++ {
		key('j')
	}
	require.Equal(t, "new", p.rows[p.cursor].Code)

	key('i')
	require.True(t, p.commentEdit)
	for _, r := range "please rename" {
		key(r)
	}
	p.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEnter})
	require.False(t, p.commentEdit)
	require.Len(t, p.drafts, 1)

	key('/')
	for _, r := range "new" {
		key(r)
	}
	p.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEnter})
	assert.False(t, p.searchMode)
	assert.NotEmpty(t, p.searchMatches)

	key('s')
	assert.True(t, p.sideBySide)

	key('a')
	assert.False(t, p.Active())
	assert.Contains(t, submitted, "main.go")
	assert.Contains(t, submitted, "please rename")
}

func TestPaneDropsNotesWhenDiffChanges(t *testing.T) {
	p := New(components.DefaultTheme(), t.TempDir(), nil, nil, nil)
	p.OpenText(sampleDiff, nil)

	ctx := &components.EventContext{}
	key := func(r rune) {
		p.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
	}
	for i := 0; i < 20 && (p.cursor >= len(p.rows) || p.rows[p.cursor].Code != "new"); i++ {
		key('j')
	}
	require.Equal(t, "new", p.rows[p.cursor].Code)

	key('i')
	for _, r := range "stale note" {
		key(r)
	}
	p.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEnter})
	require.Len(t, p.drafts, 1)

	// Reopening the same spec keeps notes...
	p.OpenText(sampleDiff, nil)
	require.Len(t, p.drafts, 1)

	// ...but switching specs must not carry them over, or `a` would resend
	// comments the agent already answered.
	p.OpenText(sampleDiff, []string{"staged"})
	assert.Empty(t, p.drafts)
}

func TestPaneEscCloses(t *testing.T) {
	p := New(components.DefaultTheme(), t.TempDir(), nil, nil, nil)
	p.OpenText(sampleDiff, nil)
	require.True(t, p.Active())
	p.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	assert.False(t, p.Active())
}

func TestPaneDraw(t *testing.T) {
	p := New(components.DefaultTheme(), t.TempDir(), nil, nil, nil)
	p.OpenText(sampleDiff, nil)
	surf := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	text := components.SurfaceText(surf)
	assert.Contains(t, text, "diff")
	assert.Contains(t, text, "main.go")
}

func TestPaneEmptyNoteNamesUntrackedPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644))
	p := New(components.DefaultTheme(), dir, nil, nil, nil)
	p.OpenText("", []string{"@new.go"})
	surf := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	assert.Contains(t, components.SurfaceText(surf), "new.go is untracked")
}

func TestChangeMetadataAndExactCommentPaths(t *testing.T) {
	dir := t.TempDir()
	p := New(components.DefaultTheme(), dir, nil, nil, nil)
	for _, tc := range []struct{ path, content, want string }{
		{"empty", "", "Empty file"},
		{"binary", "\x00\x01", "Binary files"},
		{"space file", "content\n", "content"},
		{"文件.txt", "content\n", "content"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, tc.path), []byte(tc.content), 0o600))
			p.OpenChange(dir, gitx.Change{Status: '?', Path: tc.path})
			require.Empty(t, p.err)
			text := components.SurfaceText(p.Draw(components.DrawContext{
				Max: components.Size{Width: 100, Height: 30}, Method: xui.WidthUnicode,
			}))
			assert.Contains(t, text, tc.want)
			for _, row := range p.rows {
				if diffreview.AnchorValid(row.Review) {
					assert.Equal(t, tc.path, row.Review.Path)
				}
			}
		})
	}
}

func TestChangeNotesAndBackNavigation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a"), []byte("content\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b"), []byte("other\n"), 0o600))
	var submitted string
	p := New(components.DefaultTheme(), dir, func(s string) { submitted = s }, nil, nil)
	a := gitx.Change{Status: '?', Path: "a"}
	b := gitx.Change{Status: '?', Path: "b"}
	p.OpenChange(dir, a)
	require.Empty(t, p.err)
	p.drafts = []diffreview.CommentDraft{{Body: "note for a"}}
	p.OpenChange(dir, b)
	assert.Empty(t, p.drafts)
	p.drafts = []diffreview.CommentDraft{{Body: "note for b"}}
	p.OpenChange(dir, a)
	require.Len(t, p.drafts, 1)
	assert.Equal(t, "note for a", p.drafts[0].Body)

	p.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'r'})
	assert.Equal(t, a, *p.change)
	require.Empty(t, p.err)
	require.Len(t, p.drafts, 1)
	p.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	assert.True(t, p.ReturnToList())
	p.OpenChange(dir, a)
	assert.False(t, p.ReturnToList())
	p.sendToAgent()
	assert.False(t, p.ReturnToList())
	assert.Contains(t, submitted, "note for a")

	p.OpenText(sampleDiff, nil)
	assert.Empty(t, p.drafts, "explicit diff must not inherit a selected change's notes")
	p.OpenChange(dir, b)
	require.Len(t, p.drafts, 1)
	assert.Equal(t, "note for b", p.drafts[0].Body)
}

// TestChangeQuotedPathKeepsExactCommentPath covers the control-character path
// without a control-character file: neither windows nor the filesystem is
// needed, because git's C-quoted header is what the rewrite consumes.
func TestChangeQuotedPathKeepsExactCommentPath(t *testing.T) {
	path := "line\nfile"
	// Byte-for-byte what `git diff --no-index /dev/null "./line\nfile"` prints.
	quoted := `diff --git "a/./line\nfile" "b/./line\nfile"` + "\n" +
		"new file mode 100644\n" +
		"index 0000000..d95f3ad\n" +
		"--- /dev/null\n" +
		`+++ "b/./line\nfile"` + "\n" +
		"@@ -0,0 +1 @@\n" +
		"+content\n"
	p := New(components.DefaultTheme(), t.TempDir(), nil, nil, nil)
	p.change = &gitx.Change{Status: '?', Path: path}
	p.openParsed(quoted, "", true)
	require.Empty(t, p.err)
	require.NotEmpty(t, p.rows)
	anchored := 0
	for _, row := range p.rows {
		assert.Equal(t, path, row.FileName, "the quoted form must not reach the status bar")
		if diffreview.AnchorValid(row.Review) {
			assert.Equal(t, path, row.Review.Path, "comments must anchor on the status path")
			anchored++
		}
	}
	require.NotZero(t, anchored, "the fixture must produce a commentable row")
}

// TestChangeControlCharPathIsQuotedInLabel keeps the label quoting covered on
// every platform; the file is absent on all of them, so the load error is part
// of the expectation rather than an accident.
func TestChangeControlCharPathIsQuotedInLabel(t *testing.T) {
	path := "line\nfile"
	p := New(components.DefaultTheme(), t.TempDir(), nil, nil, nil)
	p.OpenChange(p.cwd, gitx.Change{Status: '?', Path: path})
	assert.Equal(t, strconv.Quote(path), p.label)
	assert.NotEmpty(t, p.err)
}
