package edittool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
)

// applyOne runs a single-file payload against content.
func applyOne(t *testing.T, content, payload string) (Result, error) {
	t.Helper()
	sections, err := sloppy.Parse(payload)
	require.NoError(t, err)
	require.Len(t, sections, 1)
	return Apply(content, sections[0], true)
}

func mustApply(t *testing.T, content, payload string) Result {
	t.Helper()
	result, err := applyOne(t, content, payload)
	require.NoError(t, err)
	return result
}

func TestAppliesFindPutPair(t *testing.T) {
	result := mustApply(t,
		"const timeout = 1000;\nstart();\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nconst timeout = 1000;\n*** SM:PUT\nconst timeout = 5000;\n")
	assert.Equal(t, "const timeout = 5000;\nstart();\n", result.Content)
}

func TestEmptyPutDeletesTheMatch(t *testing.T) {
	result := mustApply(t,
		"a = 1;\nrun();\nb = 2;\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nrun();\n*** SM:PUT\n")
	assert.Equal(t, "a = 1;\nb = 2;\n", result.Content)
	assert.Len(t, result.Notes, 1)
	assert.Contains(t, result.Notes[0], "deleted")
}

func TestGapsCaptureAndReplayThroughPut(t *testing.T) {
	result := mustApply(t,
		"function legacy(a) {\n  stage(a);\n  commit(a);\n}\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nfunction legacy(a) {\n…\n}\n*** SM:PUT\nfunction modern(a) {\n…\n}\n")
	assert.Equal(t, "function modern(a) {\n  stage(a);\n  commit(a);\n}\n", result.Content)

	sparse := mustApply(t,
		"function loadUser(id){\n\tconst user = legacyStore.read(id);\n\treturn user;\n}\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nfunction loadUser(…){\n\tconst user = legacyStore.read(…);\n…\n"+
			"*** SM:PUT\nfunction loadUser(…){\n\tconst user = await database.users.read(…);\n"+
			"\tif (!user) throw new MissingUserError(id);\n…\n")
	assert.Equal(t,
		"function loadUser(id){\n\tconst user = await database.users.read(id);\n"+
			"\tif (!user) throw new MissingUserError(id);\n\treturn user;\n}\n",
		sparse.Content)
}

func TestMidLineGapStaysLineBounded(t *testing.T) {
	_, err := applyOne(t, "call(first,\nsecond);\nother();\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\ncall(…second);\n*** SM:PUT\ncall(…third);\n")
	require.Error(t, err, "a mid-line gap must not swallow the line break")

	result := mustApply(t,
		"call(first, second);\nother();\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\ncall(…second);\n*** SM:PUT\ncall(…third);\n")
	assert.Equal(t, "call(first, third);\nother();\n", result.Content)
}

func TestEdgeGapsReplayAsNothingAroundAnInnerCapture(t *testing.T) {
	result := mustApply(t,
		"before();\nfunction legacy(a) {\n  stage(a);\n}\nafter();\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\n…\nfunction legacy(a) {\n…\n}\n…\n"+
			"*** SM:PUT\n…\nfunction modern(a) {\n…\n}\n…\n")
	assert.Equal(t, "before();\nfunction modern(a) {\n  stage(a);\n}\nafter();\n", result.Content)
}

func TestWholeLineEdgeGapKeepsItsJoiningNewline(t *testing.T) {
	result := mustApply(t,
		"before();\nconst value = old;\nafter();\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nconst value = old;\n…\n*** SM:PUT\nconst value = new;\n")
	assert.Equal(t, "before();\nconst value = new;\nafter();\n", result.Content)
}

func TestAllRewritesEveryMatch(t *testing.T) {
	result := mustApply(t,
		"logger.debug(\nlogger.debug(\nkeep();\n",
		"*** SM:EDIT a.ts all\n*** SM:FIND\nlogger.debug(\n*** SM:PUT\nlogger.trace(\n")
	assert.Equal(t, "logger.trace(\nlogger.trace(\nkeep();\n", result.Content)
}

func TestAllWithZeroMatchesFails(t *testing.T) {
	_, err := applyOne(t, "keep();\n",
		"*** SM:EDIT a.ts all\n*** SM:FIND\nlogger.debug(\n*** SM:PUT\nlogger.trace(\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "found 0 matches")
	assert.Contains(t, err.Error(), atomicityNotice)
}

func TestAmbiguousMatchFailsWithCopyReadyRetries(t *testing.T) {
	_, err := applyOne(t, "item\nitem\n",
		"*** SM:EDIT a.txt\n*** SM:FIND\nitem\n*** SM:AFTER\nitem\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is ambiguous")
	assert.Contains(t, err.Error(), "*** SM:EDIT a.txt all")
}

func TestWhitespaceEquivalentOutcomesAreUnambiguous(t *testing.T) {
	result := mustApply(t, "open() {\n  work(unit);\n\n  work(unit);\n}\n",
		"*** SM:EDIT a.ts\n⟪  work(unit);\n│⟫\n")
	assert.Equal(t, "open() {\n  work(unit);\n}\n", result.Content)
}

func TestNormalizedMatchToleratesIndentationDrift(t *testing.T) {
	result := mustApply(t, "function run() {\n\tsecond();\t\n}\n",
		"*** SM:EDIT a.txt\n*** SM:FIND\nfunction run() {\n    second();\n*** SM:AFTER\n\tthird();\n")
	assert.Equal(t, "function run() {\n\tsecond();\t\n\tthird();\n}\n", result.Content)
}

func TestFuzzyMatchAcceptsASmallTypo(t *testing.T) {
	result := mustApply(t, "const runner = computeValue(1);\nkeep();\n",
		"*** SM:EDIT a.txt\n*** SM:FIND\nconst runner = computeValue(1);\n*** SM:PUT\nconst runner = compute(2);\n")
	assert.Equal(t, "const runner = compute(2);\nkeep();\n", result.Content)
}

func TestNeverFuzzyMatchesDifferentOperatorSequences(t *testing.T) {
	_, err := applyOne(t, "const shouldRun = left || right;\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nleft && right\n*** SM:PUT\nleft ?? right\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not match a.ts")
}

func TestNoMatchErrorGroundsTheMiss(t *testing.T) {
	_, err := applyOne(t, "const RUNNER = compute(1);\nkeep();\n",
		"*** SM:EDIT a.txt\n*** SM:FIND\nconst RUNNER = gone(9);\n*** SM:PUT\nconst RUNNER = compute(2);\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not match a.txt")
	assert.Contains(t, err.Error(), "Current file content near the closest match")
	assert.Contains(t, err.Error(), "Copy-ready corrected operation:")
	assert.Contains(t, err.Error(), "const RUNNER = compute(1);")
}

func TestAfterInsertsAndKeepsEndOfFileConvention(t *testing.T) {
	cases := []struct {
		name     string
		before   string
		expected string
	}{
		{name: "trailing newline", before: "anchor\nnext\n", expected: "anchor\nadded\nnext\n"},
		{name: "no trailing newline", before: "anchor", expected: "anchor\nadded"},
		{name: "last line", before: "anchor\n", expected: "anchor\nadded\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := mustApply(t, tc.before,
				"*** SM:EDIT a.txt\n*** SM:FIND\nanchor\n*** SM:AFTER\nadded\n")
			assert.Equal(t, tc.expected, result.Content)
		})
	}
}

func TestAfterBodyIsLiteral(t *testing.T) {
	result := mustApply(t, "anchor\n\nnext\n",
		"*** SM:EDIT a.txt\n*** SM:FIND\nanchor\n*** SM:AFTER\n\n\t+literal <a> & \"b\"\n…\n\n")
	assert.Equal(t, "anchor\n\n\t+literal <a> & \"b\"\n…\n\nnext\n", result.Content)
}

func TestSelectionWithGapsCapturesAndReplays(t *testing.T) {
	result := mustApply(t, "const value = oldCall(options);\nreport(value);\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nconst value = ⟪oldCall(…)│newCall(…) ?? fallback⟫;\nreport(value)\n")
	assert.Equal(t, "const value = newCall(options) ?? fallback;\nreport(value);\n", result.Content)
}

func TestInlineSelectionsRewriteOnlyTheSelectedText(t *testing.T) {
	result := mustApply(t,
		"const timeout = readConfig().timeout ?? 1000;\nrun(timeout);\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\ntimeout = …⟪1000│5000⟫…\nrun(timeout)\n")
	assert.Equal(t, "const timeout = readConfig().timeout ?? 5000;\nrun(timeout);\n", result.Content)
}

func TestInlineSelectionsApplyToEachNamedSelection(t *testing.T) {
	result := mustApply(t,
		"const timeout = 1000;\nconst retries = 3;\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nconst timeout = ⟪1000│5000⟫;\nconst retries = ⟪3│5⟫;\n")
	assert.Equal(t, "const timeout = 5000;\nconst retries = 5;\n", result.Content)
}

func TestInlineInsertionAndDeletion(t *testing.T) {
	inserted := mustApply(t, "run(x);\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nrun(⟪│y⟫x);\n")
	assert.Equal(t, "run(yx);\n", inserted.Content)

	deleted := mustApply(t, "run(x);\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nrun(⟪x│⟫);\n")
	assert.Equal(t, "run();\n", deleted.Content)
}

func TestGapOnlySelectionReplacesTheCapturedRegion(t *testing.T) {
	result := mustApply(t, "loadUser( id , opts );\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nloadUser(⟪…│id⟫);\n")
	assert.Equal(t, "loadUser(id);\n", result.Content)
}

func TestLineInsertionLandsOnItsOwnLine(t *testing.T) {
	result := mustApply(t, "function displayName(user) {\n  return user.name;\n}\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\n  return user.name;\n⟪│  return user?.name ?? fallback;⟫\n")
	assert.Equal(t,
		"function displayName(user) {\n  return user.name;\n  return user?.name ?? fallback;\n}\n",
		result.Content)
}

func TestBatchAnchorsResolveAgainstOriginalContent(t *testing.T) {
	result := mustApply(t, "one\ntwo\nthree\n",
		"*** SM:EDIT a.txt\n*** SM:FIND\none\n*** SM:PUT\none changed\n"+
			"*** SM:FIND\ntwo\n*** SM:AFTER\ninserted\n"+
			"*** SM:FIND\nthree\n*** SM:PUT\nthree changed\n")
	assert.Equal(t, "one changed\ntwo\ninserted\nthree changed\n", result.Content)
}

func TestOverlappingEditsFailAtomically(t *testing.T) {
	_, err := applyOne(t, "alpha beta\n",
		"*** SM:EDIT a.txt\n*** SM:FIND\nalpha\n*** SM:PUT\nfirst\n*** SM:FIND\nalpha beta\n*** SM:PUT\nsecond\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlapping original spans")
}

func TestNoOpErrorNamesTheOperation(t *testing.T) {
	_, err := applyOne(t, "run();\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nrun();\n*** SM:PUT\nrun();\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Operation 1 makes no change to a.ts.")
}

func TestDesiredTextIsANoOpWhenItAlreadyHolds(t *testing.T) {
	result := mustApply(t, "const a = 1;\n",
		"*** SM:EDIT a.ts\n*** SM:PUT\nconst a = 1;\n")
	assert.Equal(t, "const a = 1;\n", result.Content)
	assert.Len(t, result.Notes, 1)
	assert.Contains(t, result.Notes[0], "already matches the file")
}

func TestDesiredTextUpgradesTheClosestBlock(t *testing.T) {
	result := mustApply(t,
		"function helper() {\n  return 1;\n}\nkeep();\n",
		"*** SM:EDIT a.ts\n*** SM:PUT\nfunction helper() {\n  return 2;\n}\n")
	assert.Equal(t, "function helper() {\n  return 2;\n}\nkeep();\n", result.Content)
	assert.Len(t, result.Notes, 1)
	assert.Contains(t, result.Notes[0], "closest matching block")
}

func TestDesiredTextWithoutResemblingBlockFails(t *testing.T) {
	_, err := applyOne(t, "unrelated();\n",
		"*** SM:EDIT a.ts\n*** SM:PUT\nfunction helper() {\n  return 2;\n}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no block in a.ts resembles it")
}

func TestPutWithSelectionMarkersIsRejected(t *testing.T) {
	_, err := applyOne(t, "run();\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nrun();\n*** SM:PUT\n⟪run│go⟫();\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has selection markers in *** SM:PUT")
}

func TestWholeLineGapWithoutCaptureIsRejected(t *testing.T) {
	_, err := applyOne(t, "run();\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\nrun();\n*** SM:PUT\n…\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whole-line … with no *** SM:FIND gap")
}

func TestTooGenericPatternIsRejected(t *testing.T) {
	_, err := applyOne(t, "if (ready) { run(); }\n",
		"*** SM:EDIT a.ts\n*** SM:FIND\n⟪};│⟫\n*** SM:PUT\n}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too generic")
}

func TestFailureLeavesContentUntouched(t *testing.T) {
	_, err := applyOne(t, "alpha\nalpha\n",
		"*** SM:EDIT a.txt\n*** SM:FIND\nalpha\n*** SM:PUT\nbeta\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), atomicityNotice)
}
