package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolve turns server ranges into snippet hits: positions come back in UTF-16
// code units, the file is read on disk, and the match is kept in view.
func TestResolveBuildsHits(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "a.go")
	require.NoError(t, os.WriteFile(abs, []byte("package main\n\n  héllo wörld\n"), 0o600))

	m := New(root, false)
	c := newClient(serverDef{}, root) // utf-16, the protocol default

	// "héllo wörld" on the third line: characters 8..13 hold "wörld".
	loc := lspLocation{
		URI:   pathToURI(abs),
		Range: lspRange{Start: lspPosition{Line: 2, Character: 8}, End: lspPosition{Line: 2, Character: 13}},
	}

	hits := m.resolve(c, []lspLocation{loc}, true)
	require.Len(t, hits, 1)
	assert.Equal(t, "a.go", hits[0].Path)
	assert.Equal(t, 3, hits[0].Line) // 1-based
	assert.Equal(t, "héllo ", hits[0].Pre)
	assert.Equal(t, "wörld", hits[0].Mid)
	assert.Empty(t, hits[0].Post)
	assert.True(t, hits[0].Def)
	assert.False(t, hits[0].Ext)
}

func TestResolveSortsAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "a.go")
	require.NoError(t, os.WriteFile(abs, []byte("one\ntwo\nthree\n"), 0o600))

	m := New(root, false)
	c := newClient(serverDef{}, root)
	at := func(line int) lspLocation {
		return lspLocation{
			URI:   pathToURI(abs),
			Range: lspRange{Start: lspPosition{Line: line}, End: lspPosition{Line: line, Character: 3}},
		}
	}
	// Out of order, with a repeat: one entry per line, ordered by position. The
	// range covers the first three columns of the line.
	hits := m.resolve(c, []lspLocation{at(2), at(0), at(2)}, true)
	require.Len(t, hits, 2)
	assert.Equal(t, 1, hits[0].Line)
	assert.Equal(t, "one", hits[0].Mid)
	assert.Equal(t, 3, hits[1].Line)
	assert.Equal(t, "thr", hits[1].Mid)
}

// A hit outside the root keeps its absolute path and says so, so a caller can
// show a jump into the standard library as leaving the workspace.
func TestResolveKeepsExternalPaths(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "stdlib.go")
	require.NoError(t, os.WriteFile(outside, []byte("func Sprintf() {}\n"), 0o600))

	m := New(root, false)
	c := newClient(serverDef{}, root)
	loc := lspLocation{
		URI:   pathToURI(outside),
		Range: lspRange{Start: lspPosition{Line: 0}, End: lspPosition{Line: 0, Character: 4}},
	}

	hits := m.resolve(c, []lspLocation{loc}, false)
	require.Len(t, hits, 1)
	assert.True(t, hits[0].Ext)
	assert.Equal(t, filepath.ToSlash(outside), hits[0].Path)
	assert.Equal(t, "func", hits[0].Mid)
	assert.False(t, hits[0].Def)
}

// A file that cannot be read still yields a usable hit instead of an error or a
// panic.
func TestResolveUnreadableFile(t *testing.T) {
	root := t.TempDir()
	m := New(root, false)
	c := newClient(serverDef{}, root)
	missing := filepath.Join(root, "gone.go")

	hits := m.resolve(c, []lspLocation{{
		URI:   pathToURI(missing),
		Range: lspRange{Start: lspPosition{Line: 12, Character: 3}},
	}}, true)
	require.Len(t, hits, 1)
	assert.Equal(t, "gone.go", hits[0].Path)
	assert.Equal(t, 13, hits[0].Line)
	assert.Empty(t, hits[0].Pre)
	assert.Empty(t, hits[0].Mid)
}

// Bad URIs are skipped rather than reported: navigation is best-effort.
func TestResolveSkipsNonFileURIs(t *testing.T) {
	m := New(t.TempDir(), false)
	c := newClient(serverDef{}, m.root)
	hits := m.resolve(c, []lspLocation{{URI: "https://example.com/x.go"}}, true)
	assert.Empty(t, hits)
}

func TestSnip(t *testing.T) {
	cases := []struct {
		name             string
		line, from, to   int
		text             string
		wantPre, wantMid string
		wantPost         string
	}{
		{
			name: "indentation goes away", text: "\t\tfoo.Bar()", from: 6, to: 9,
			wantPre: "foo.", wantMid: "Bar", wantPost: "()",
		},
		{
			name: "long lead-in is elided", text: strings.Repeat("x", 60) + "target rest",
			from: 60, to: 66,
			wantPre: "…" + strings.Repeat("x", 16), wantMid: "target", wantPost: " rest",
		},
		{
			name: "long match is capped", text: strings.Repeat("y", 300),
			from: 0, to: 300,
			wantPre: "", wantMid: strings.Repeat("y", snipMax) + "…", wantPost: "",
		},
		{
			name: "clamps out-of-range offsets", text: "abc", from: 99, to: 200,
			wantPre: "abc", wantMid: "", wantPost: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := snip([]byte(c.text), c.from, c.to)
			assert.Equal(t, c.wantPre, got.pre)
			assert.Equal(t, c.wantMid, got.mid)
			assert.Equal(t, c.wantPost, got.post)
		})
	}
}

// hoverMarkup builds the MarkupContent shape a server answers a hover with.
func hoverMarkup(t *testing.T, kind, value string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"kind": kind, "value": value})
	require.NoError(t, err)
	return raw
}

func TestParseHoverContents(t *testing.T) {
	t.Run("markdown markup with a fence", func(t *testing.T) {
		raw := hoverMarkup(t, "markdown", "```go\nfunc Foo() error\n```\n\nSays hello.\n")
		code, prose := parseHoverContents(raw)
		assert.Equal(t, "func Foo() error", code)
		assert.Equal(t, "Says hello.", prose)
	})

	t.Run("plaintext promotes the declaration", func(t *testing.T) {
		code, prose := parseHoverContents([]byte(`"func Foo() error\nSays hello."`))
		assert.Equal(t, "func Foo() error", code)
		assert.Equal(t, "Says hello.", prose)
	})

	t.Run("marked strings", func(t *testing.T) {
		code, prose := parseHoverContents(
			[]byte(`[{"language":"go","value":"func Foo()"},{"language":"","value":"Doc"}]`),
		)
		assert.Equal(t, "func Foo()", code)
		assert.Equal(t, "Doc", prose)
	})

	t.Run("prose cap", func(t *testing.T) {
		_, prose := parseHoverContents(hoverMarkup(t, "plaintext", strings.Repeat("word ", 400)))
		assert.LessOrEqual(t, len(prose), maxProse+len("…"))
		assert.True(t, strings.HasSuffix(prose, "…"))
	})

	t.Run("nothing to say", func(t *testing.T) {
		code, prose := parseHoverContents([]byte(`null`))
		assert.Empty(t, code)
		assert.Empty(t, prose)
	})
}

func TestSplitMarkdownKeepsProseMarkdown(t *testing.T) {
	code, prose := splitMarkdown("```go\nx := 1\n```\n\nSee [docs](https://x) for `y`.\n")
	assert.Equal(t, "x := 1", code)
	// Documentation stays markdown: the caller renders it.
	assert.Equal(t, "See [docs](https://x) for `y`.", prose)
}

// A terminal prints the signature, so the markup clangd and friends send has to
// go, without eating arithmetic that only looks like a tag.
func TestPlainSignatureStripsTags(t *testing.T) {
	got := plainSignature(`<span class="k">func</span> Foo&lt;T&gt;(x < b >)`)
	assert.Equal(t, "func Foo<T>(x < b >)", got)

	assert.Empty(t, plainSignature(""))

	// Long signatures are cut before they flood the terminal.
	long := strings.Repeat("line\n", maxSignatureLines+5)
	assert.Equal(t, maxSignatureLines, strings.Count(plainSignature(long), "\n"))
	assert.True(t, strings.HasSuffix(plainSignature(long), "…"))
}

func TestSymbolKindName(t *testing.T) {
	assert.Equal(t, "func", symbolKindName(12))
	assert.Equal(t, "struct", symbolKindName(23))
	assert.Equal(t, "sym", symbolKindName(999)) // unknown kinds stay printable
}

// Symbol kinds and indentation mirror what the server sent, flattened.
func TestSymbolsWalkIsFlattened(t *testing.T) {
	raw := []lspDocumentSymbol{{
		Name: "Outer", Kind: 23, SelectionRange: lspRange{Start: lspPosition{Line: 4}},
		Children: []lspDocumentSymbol{{
			Name: "inner", Kind: 6, SelectionRange: lspRange{Start: lspPosition{Line: 6}},
		}},
	}}
	out := flattenSymbols(raw)

	require.Len(t, out, 2)
	assert.Equal(t, Symbol{Name: "Outer", Kind: "struct", Line: 5}, out[0])
	assert.Equal(t, Symbol{Name: "inner", Kind: "method", Line: 7, Indent: 2}, out[1])
}

// symbolInformation arrives flat, with the range under Location.
func TestFlattenSymbolsAcceptsFlatForm(t *testing.T) {
	raw := []lspDocumentSymbol{
		{Name: "b", Kind: 12, Location: &lspLocation{Range: lspRange{Start: lspPosition{Line: 9}}}},
		{Name: "a", Kind: 12, Location: &lspLocation{Range: lspRange{Start: lspPosition{Line: 1}}}},
	}
	out := flattenSymbols(raw)
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Name, "symbolInformation is sorted by line")
	assert.Equal(t, 2, out[0].Line)
	assert.Equal(t, 0, out[0].Indent, "flat form has no nesting")
	assert.Equal(t, "b", out[1].Name)
}

// A request for a file nobody can read must fail rather than ask the server
// about character 0 of an empty line.
func TestPositionTextErrorsOnMissingFile(t *testing.T) {
	_, err := positionText(filepath.Join(t.TempDir(), "absent.go"), 1)
	require.Error(t, err)
}

func TestPositionTextReadsLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\r\ntwo\nthree\n"), 0o600))
	text, err := positionText(path, 2)
	require.NoError(t, err)
	assert.Equal(t, "two", text)
}

func TestReadLinesNormalisesCRLF(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "a.go")
	require.NoError(t, os.WriteFile(abs, []byte("one\r\ntwo\r\n"), 0o600))

	lines := readLines(abs)
	assert.Equal(t, []string{"one", "two", ""}, lines)
	assert.Equal(t, "two", lineAt(lines, 2))
	assert.Empty(t, lineAt(lines, 9))
	assert.Nil(t, readLines(filepath.Join(t.TempDir(), "missing.go")))
}
