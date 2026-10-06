package edittool

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func toolContext(t *testing.T) (context.Context, string) {
	t.Helper()
	dir := t.TempDir()
	return tooldef.WithCwd(t.Context(), dir), dir
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
}

func readFiles(t *testing.T, dir string, names ...string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		out[name] = string(raw)
	}
	return out
}

func TestEditToolAppliesAPayload(t *testing.T) {
	ctx, dir := toolContext(t)
	writeFiles(t, dir, map[string]string{"a.ts": "const timeout = 1000;\nstart();\n"})

	result, err := runEdit(ctx, editInput{
		Payload: "*** SM:EDIT a.ts\n*** SM:FIND\nconst timeout = 1000;\n*** SM:PUT\nconst timeout = 5000;\n",
	})
	require.NoError(t, err)

	assert.Equal(t, "const timeout = 5000;\nstart();\n", readFiles(t, dir, "a.ts")["a.ts"])
	assert.Equal(t, "a.ts", result.Detail)
	assert.Contains(t, result.Content, "a.ts")
	assert.Contains(t, result.Content, "-const timeout = 1000;")
	assert.Contains(t, result.Content, "+const timeout = 5000;")
}

func TestEditToolEditsMultipleFilesAtomically(t *testing.T) {
	ctx, dir := toolContext(t)
	writeFiles(t, dir, map[string]string{
		"a.ts": "const endpoint = \"/v1\";\n",
		"b.ts": "router.use(\"/v1\", api);\n",
	})

	payload := "*** SM:EDIT a.ts\n*** SM:FIND\nconst endpoint = \"/v1\";\n*** SM:PUT\nconst endpoint = \"/v2\";\n" +
		"*** SM:EDIT b.ts\n*** SM:FIND\nrouter.use(\"/v1\", api);\n*** SM:PUT\nrouter.use(\"/v2\", api);\n"
	result, err := runEdit(ctx, editInput{Payload: payload})
	require.NoError(t, err)

	files := readFiles(t, dir, "a.ts", "b.ts")
	assert.Equal(t, "const endpoint = \"/v2\";\n", files["a.ts"])
	assert.Equal(t, "router.use(\"/v2\", api);\n", files["b.ts"])
	assert.Equal(t, "a.ts, b.ts", result.Detail)
}

func TestEditToolFailureInOneFileWritesNothing(t *testing.T) {
	ctx, dir := toolContext(t)
	writeFiles(t, dir, map[string]string{
		"a.ts": "const endpoint = \"/v1\";\n",
		"b.ts": "unrelated();\n",
	})

	payload := "*** SM:EDIT a.ts\n*** SM:FIND\nconst endpoint = \"/v1\";\n*** SM:PUT\nconst endpoint = \"/v2\";\n" +
		"*** SM:EDIT b.ts\n*** SM:FIND\nrouter.use(\"/v1\", api);\n*** SM:PUT\nrouter.use(\"/v2\", api);\n"
	_, err := runEdit(ctx, editInput{Payload: payload})
	require.Error(t, err)

	files := readFiles(t, dir, "a.ts", "b.ts")
	assert.Equal(t, "const endpoint = \"/v1\";\n", files["a.ts"])
	assert.Equal(t, "unrelated();\n", files["b.ts"])
	assert.Contains(t, err.Error(), "[b.ts]")
	assert.Contains(t, err.Error(), "No files were modified — sections apply atomically.")
	assert.Contains(t, err.Error(), atomicityNotice)
}

func TestEditToolKeepsCRLFFilesCRLF(t *testing.T) {
	ctx, dir := toolContext(t)
	writeFiles(t, dir, map[string]string{"a.ts": "const timeout = 1000;\r\nstart();\r\n"})

	_, err := runEdit(ctx, editInput{
		Payload: "*** SM:EDIT a.ts\n*** SM:FIND\nconst timeout = 1000;\n*** SM:PUT\nconst timeout = 5000;\n",
	})
	require.NoError(t, err)
	assert.Equal(t, "const timeout = 5000;\r\nstart();\r\n", readFiles(t, dir, "a.ts")["a.ts"])
}

func TestEditToolNotesReachTheModel(t *testing.T) {
	ctx, dir := toolContext(t)
	writeFiles(t, dir, map[string]string{"a.ts": "a = 1;\nrun();\nb = 2;\n"})

	result, err := runEdit(ctx, editInput{
		Payload: "*** SM:EDIT a.ts\n*** SM:FIND\nrun();\n*** SM:PUT\n",
	})
	require.NoError(t, err)
	assert.Contains(t, result.Content, "Note: operation 1 deleted")
}

func TestEditToolRejectsEmptyAndTargetlessPayloads(t *testing.T) {
	ctx, _ := toolContext(t)

	_, err := runEdit(ctx, editInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "edit requires a payload")

	_, err = runEdit(ctx, editInput{Payload: "*** SM:FIND\nrun();\n*** SM:PUT\ngo();\n"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error[SM101]: payload does not open with a file target")
}

func TestEditDetailListsTargetPaths(t *testing.T) {
	assert.Equal(t, "a.ts, b.ts", editDetail(editInput{
		Payload: "*** SM:EDIT a.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n*** SM:EDIT b.ts\n*** SM:FIND\nx\n*** SM:PUT\ny\n",
	}))
	assert.Equal(t, "edit", editDetail(editInput{}))
}

func TestEditToolReportsNotesFromUnchangedFiles(t *testing.T) {
	ctx, dir := toolContext(t)
	writeFiles(t, dir, map[string]string{"a.ts": "const a = 1;\n"})

	result, err := runEdit(ctx, editInput{Payload: "*** SM:EDIT a.ts\n*** SM:PUT\nconst a = 1;\n"})
	require.NoError(t, err)
	assert.Equal(t, "const a = 1;\n", readFiles(t, dir, "a.ts")["a.ts"])
	assert.Contains(t, result.Content, "already matches the file")
}

// descriptionExamples extracts the payload blocks from the description's
// example section, dropping the prose line above each block.
func descriptionExamples() []string {
	start := strings.Index(editDescription, "<example>")
	end := strings.LastIndex(editDescription, "</example>")
	if start < 0 || end < 0 {
		return nil
	}
	var payloads []string
	for block := range strings.SplitSeq(editDescription[start+len("<example>"):end], "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "*** SM:EDIT") {
				payloads = append(payloads, strings.Join(lines[i:], "\n"))
				break
			}
		}
	}
	return payloads
}

// descriptionAntiPatterns extracts the WRONG/RIGHT payload pairs from the
// description's anti-pattern section. A RIGHT that only names another tool has
// no payload and comes back empty.
func descriptionAntiPatterns() (wrong, right []string) {
	start := strings.Index(editDescription, "<anti-patterns>")
	end := strings.LastIndex(editDescription, "</anti-patterns>")
	if start < 0 || end < 0 {
		return nil, nil
	}
	for block := range strings.SplitSeq(editDescription[start+len("<anti-patterns>"):end], "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		marker := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "RIGHT") })
		if marker < 0 {
			continue
		}
		wrong = append(wrong, payloadBlock(lines[:marker]))
		right = append(right, payloadBlock(lines[marker+1:]))
	}
	return wrong, right
}

// payloadBlock returns a block from its *** SM:EDIT header on, or "" when the
// block has no payload at all.
func payloadBlock(lines []string) string {
	start := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "*** SM:EDIT") })
	if start < 0 {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

// The description teaches by example; an example that fails to parse or apply
// would teach broken shapes, so every one runs through the engine.
func TestEditDescriptionExamples(t *testing.T) {
	payloads := descriptionExamples()
	require.Len(t, payloads, 2)

	t.Run("parses", func(t *testing.T) {
		for _, payload := range payloads {
			sections, err := sloppy.Parse(payload)
			require.NoError(t, err, "example payload:\n%s", payload)
			require.NotEmpty(t, sections)
		}
	})

	t.Run("applies", func(t *testing.T) {
		users, err := sloppy.Parse(payloads[0])
		require.NoError(t, err)
		// The unindented FIND must still anchor indented file text: the
		// matching space drops whitespace, and captures keep file indentation.
		result, err := Apply(
			"function load(id, opts) {\n\tconst raw = fetch(id);\n\treturn old(id, opts);\n}\n",
			users[0], false)
		require.NoError(t, err)
		assert.Equal(t,
			"function load(id, opts) {\n\tconst raw = fetch(id);\n\treturn fresh(id, opts);\n}\n",
			result.Content)

		app, err := sloppy.Parse(payloads[1])
		require.NoError(t, err)
		result, err = Apply(
			"total := a + b\nif debug {\n\tlog.Printf(\"total=%d\", total)\n}\n",
			app[0], false)
		require.NoError(t, err)
		assert.Equal(t,
			"total := sum(a, b)\nif verbose {\n\tlog.Printf(\"total=%d\", total)\n}\n",
			result.Content)
	})
}

// Both directions of the anti-pattern block stay executable: a WRONG shape the
// parser accepts would teach a broken rule, and a RIGHT shape that does not
// parse is worse than no counter-example at all.
func TestEditDescriptionAntiPatterns(t *testing.T) {
	wrong, right := descriptionAntiPatterns()
	require.Len(t, wrong, 3)
	require.Len(t, right, 3)

	for i, payload := range wrong {
		require.NotEmpty(t, payload, "anti-pattern %d has no payload", i+1)
		_, err := sloppy.Parse(payload)
		require.Error(t, err, "anti-pattern %d must stay rejected:\n%s", i+1, payload)
	}
	for i, payload := range right {
		if payload == "" {
			continue // the repair names another tool instead of a payload
		}
		sections, err := sloppy.Parse(payload)
		require.NoError(t, err, "repair %d must parse:\n%s", i+1, payload)
		require.NotEmpty(t, sections)
	}

	t.Run("repairs apply", func(t *testing.T) {
		require.NotEmpty(t, right[0])
		require.NotEmpty(t, right[2])
		for _, tc := range []struct{ payload, content, want string }{
			{right[0], "x\n", "y\nz\n"},
			{right[2], "const A = 1\n", "const A = 2\n"},
		} {
			sections, err := sloppy.Parse(tc.payload)
			require.NoError(t, err)
			result, err := Apply(tc.content, sections[0], false)
			require.NoError(t, err, "payload:\n%s", tc.payload)
			assert.Equal(t, tc.want, result.Content)
		}
	})
}

// The description is re-sent on every request, so its size is a budget rather
// than a style choice. Counter-examples earn their bytes; trim prose before
// raising this bound.
func TestEditDescriptionStaysLean(t *testing.T) {
	assert.Less(t, len(editDescription), 4096)
}
