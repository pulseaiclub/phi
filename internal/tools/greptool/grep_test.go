package greptool

import (
	"bytes"
	"context"
	"encoding/json"
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

	raw, err := json.Marshal(grepInput{Pattern: "Hello", Path: "src"})
	require.NoError(t, err)
	out, err := runGrep(t.Context(), raw)
	if err != nil && strings.Contains(err.Error(), "ripgrep") {
		t.Skip(err.Error())
	}
	require.NoError(t, err)
	assert.Contains(t, out.Content, "@file src/main.go#")
	assert.Contains(t, out.Content, "src/main.go:>>")
	assert.NotContains(t, out.Content, "@file main.go#")
}

func TestRunGrep_DefaultPathUsesCwdRelative(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\nfunc Hello() {}\n"), 0o644))
	t.Chdir(root)

	raw, err := json.Marshal(grepInput{Pattern: "Hello"})
	require.NoError(t, err)
	out, err := runGrep(t.Context(), raw)
	if err != nil && strings.Contains(err.Error(), "ripgrep") {
		t.Skip(err.Error())
	}
	require.NoError(t, err)
	assert.Contains(t, out.Content, "@file src/main.go#")
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
	require.Contains(t, out.Content, "small.js:>>2")
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
	assert.Contains(t, out.Content, "z_after.js:>>1")
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

// runGrepBounded fails the test instead of hanging the suite.
func runGrepBounded(t *testing.T, in grepInput) (tooldef.Result, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	type result struct {
		out tooldef.Result
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := runGrep(ctx, raw)
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
