package greptool

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pulseaiclub/phi/internal/tools/tooldef"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunGrep_CwdRelativeHeaders(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\nfunc Hello() {}\n"), 0o644))
	t.Chdir(root)

	out, err := runGrep(t.Context(), grepInput{Pattern: "Hello", Path: "src"})
	if err != nil && strings.Contains(err.Error(), "ripgrep") {
		t.Skip(err.Error())
	}
	require.NoError(t, err)
	assert.Equal(t, "@file src/main.go\n2|func Hello() {}", out.Content)
	assert.Equal(t, out.Content, out.Output)
}

func TestRunGrep_DefaultPathUsesCwdRelative(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\nfunc Hello() {}\n"), 0o644))
	t.Chdir(root)

	out, err := runGrep(t.Context(), grepInput{Pattern: "Hello"})
	if err != nil && strings.Contains(err.Error(), "ripgrep") {
		t.Skip(err.Error())
	}
	require.NoError(t, err)
	assert.Contains(t, out.Content, "@file src/main.go\n")
}

// A matched line bigger than the read cap used to end the scan while ripgrep was
// still writing; Wait then blocked forever on the full stdout pipe. Now the event
// is skipped and the rest of the search still comes back.
func TestRunGrep_OversizedMatchDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	big := append([]byte("needle"), bytes.Repeat([]byte("x"), 3<<20)...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bundle.min.js"), big, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "small.js"), []byte("var a = 1;\nneedle here\n"), 0o644))

	out, err := runGrepBounded(t, grepInput{Pattern: "needle", Path: dir})

	require.NoError(t, err)
	require.Contains(t, out.Content, "small.js\n2|needle here")
	assert.NotContains(t, out.Content, "bundle.min.js")
	assert.Contains(t, out.Content, "1 match lines exceeded 2048KB and were skipped")
}

// Only oversized events: report the skip instead of claiming there was no match.
func TestRunGrep_AllOversizedMatches(t *testing.T) {
	dir := t.TempDir()
	big := append([]byte("needle"), bytes.Repeat([]byte("x"), 3<<20)...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bundle.min.js"), big, 0o644))

	out, err := runGrepBounded(t, grepInput{Pattern: "needle", Path: dir})

	require.NoError(t, err)
	assert.Equal(t, "0 matches", out.Detail)
	assert.Contains(t, out.Content, "No matches found: 1 match lines exceeded 2048KB and were skipped")
}

// A minified bundle between good matches must not truncate the result set.
func TestRunGrep_OversizedMatchKeepsLaterMatches(t *testing.T) {
	dir := t.TempDir()
	big := append([]byte("needle"), bytes.Repeat([]byte("x"), 3<<20)...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a_bundle.min.js"), big, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "z_after.js"), []byte("needle\n"), 0o644))

	out, err := runGrepBounded(t, grepInput{Pattern: "needle", Path: dir})

	require.NoError(t, err)
	assert.Contains(t, out.Content, "z_after.js\n1|needle")
}

// Reaching the match limit kills ripgrep mid-stream; the drain-and-reap path
// must still return instead of blocking on stderr or a second Wait.
func TestRunGrep_LimitReachedStopsCleanly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.js", "b.js", "c.js", "d.js"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("needle\n"), 0o644))
	}

	out, err := runGrepBounded(t, grepInput{Pattern: "needle", Path: dir, Limit: 2})

	require.NoError(t, err)
	assert.Equal(t, "2 matches", out.Detail)
	assert.Contains(t, out.Content, "2 matches limit reached")
}

func TestRunGrep_MergesContext(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte(
		"before\nneedle one\nneedle two\nafter\nskipped\nskipped\nbefore\nneedle three\nafter"), 0o644))
	t.Chdir(root)

	out, err := runGrepBounded(t, grepInput{Pattern: "needle", Context: 1})
	require.NoError(t, err)
	assert.Equal(
		t,
		"@file sample.txt\n1|before\n>2|needle one\n>3|needle two\n4|after\n--\n7|before\n>8|needle three\n9|after",
		out.Content,
	)
	assert.Equal(t, "3 matches", out.Detail)
}

func TestFormatGrepBlock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    []string
		matches []grepMatch
		context int
		want    []string
	}{
		{
			name: "context clipped to file boundaries",
			text: []string{"first", "last"}, matches: []grepMatch{{lineNumber: 1}, {lineNumber: 2}},
			context: 10, want: []string{">1|first", ">2|last"},
		},
		{
			name:    "unreadable file",
			matches: []grepMatch{{lineNumber: 2}},
			want:    []string{"2| (unable to read matched line; rerun grep)"},
		},
		{
			name: "file shortened after search",
			text: []string{"first"}, matches: []grepMatch{{lineNumber: 2}},
			want: []string{"2| (unable to read matched line; rerun grep)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines, truncated := formatGrepBlock(tc.text, tc.matches, tc.context)
			assert.Equal(t, tc.want, lines)
			assert.False(t, truncated)
		})
	}
}

func TestRunGrep_ExactLimitIsComplete(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("needle one\nneedle two"), 0o644))
	t.Chdir(root)

	out, err := runGrepBounded(t, grepInput{Pattern: "needle", Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, "@file sample.txt\n1|needle one\n2|needle two", out.Content)
	assert.Equal(t, "2 matches", out.Detail)
}

func TestRunGrep_LimitWithContext(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("needle one\nneedle two"), 0o644))
	t.Chdir(root)

	out, err := runGrepBounded(t, grepInput{Pattern: "needle", Limit: 1, Context: 1})
	require.NoError(t, err)
	assert.Contains(t, out.Content, "@file sample.txt\n>1|needle one\n2|needle two")
	assert.Contains(t, out.Content, "1 matches limit reached")
	assert.Equal(t, "1 matches", out.Detail)
}

func TestRunGrep_PreservesMatchesAndSupportsExclusion(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("// needle\nfunc needle() {}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main_test.go"), []byte("// needle in test"), 0o644))
	t.Chdir(root)

	out, err := runGrepBounded(t, grepInput{Pattern: "needle"})
	require.NoError(t, err)
	assert.Contains(t, out.Content, "@file main.go\n1|// needle\n2|func needle() {}")
	assert.Contains(t, out.Content, "@file main_test.go\n1|// needle in test")
	assert.Equal(t, "3 matches", out.Detail)

	out, err = runGrepBounded(t, grepInput{Pattern: "needle", Glob: "!*_test.go"})
	require.NoError(t, err)
	assert.Equal(t, "@file main.go\n1|// needle\n2|func needle() {}", out.Content)
	assert.Equal(t, "2 matches", out.Detail)
}

// The row already paints the tool name; the detail must not repeat it.
func TestGrepDetail(t *testing.T) {
	assert.Equal(t, `"Hello" in src`, grepDetail(grepInput{Pattern: "Hello", Path: "src"}))
	assert.Equal(t, `"Hello" in .`, grepDetail(grepInput{Pattern: " Hello "}))
	assert.Equal(t, "src", grepDetail(grepInput{Path: "src"}))
}

// runGrepBounded fails the test instead of hanging the suite.
func runGrepBounded(t *testing.T, in grepInput) (tooldef.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	type result struct {
		out tooldef.Result
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := runGrep(ctx, in)
		done <- result{out: out, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil && strings.Contains(got.err.Error(), "ripgrep") {
			t.Skip(got.err.Error())
		}
		return got.out, got.err
	case <-time.After(30 * time.Second):
		require.FailNow(t, "runGrep hung")
		return tooldef.Result{}, nil
	}
}
