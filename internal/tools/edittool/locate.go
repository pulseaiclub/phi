package edittool

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
)

// locate anchors one operation's pattern in the file. It walks the matching
// ladder from literal to tolerant, claims only unambiguous anchors, and on
// failure explains where the pattern nearly matched.
func (a *applier) locate(op *sloppy.Operation, pat *compiledPattern, number int) ([]candidate, error) {
	nt := normalizeText(a.content)
	raw, overflow := collectCandidates(a.content, nt, pat, modeRaw, false)
	if overflow {
		return nil, a.tooBroadError(number)
	}

	if len(raw) == 0 && pat.fallback != nil {
		hits := exactOccurrences(nt.text, pat.fallback.norm)
		if len(hits) > 0 && (op.All || len(hits) == 1) {
			candidates := make([]candidate, 0, len(hits))
			for _, hit := range hits {
				candidates = append(candidates, fallbackCandidate(nt, pat, hit))
			}
			return candidates, nil
		}
	}

	result := raw
	if len(result) == 0 {
		var overflow bool
		result, overflow = collectCandidates(a.content, nt, pat, modeNormalized, false)
		if overflow {
			return nil, a.tooBroadError(number)
		}
	}
	if len(result) == 0 {
		var overflow bool
		result, overflow = collectCandidates(a.content, nt, pat, modeFuzzy, false)
		if overflow {
			return nil, a.tooBroadError(number)
		}
		if len(result) == 0 && !op.All {
			punctuation, punctuationOverflow := collectCandidates(a.content, nt, pat, modeFuzzy, true)
			if !punctuationOverflow && len(punctuation) == 1 {
				result = punctuation
			}
		}
	}

	if op.All && len(result) > 0 {
		return result, nil
	}
	if len(result) == 1 {
		return result, nil
	}
	if len(result) == 0 {
		return nil, a.noMatchError(op, pat, number)
	}
	if len(result) <= 4 && !op.Desired && op.Rewrite.Kind != sloppy.RewriteInsert &&
		identicalOutcomes(a.content, op, pat, result) {
		return result[:1], nil
	}
	return nil, a.ambiguityError(op, pat, number, result)
}

// fallbackCandidate builds an anchor from the literal fallback: the pattern
// was matched with its "…" glyphs spelled out, so a PUT "…" is literal too.
func fallbackCandidate(nt normText, pat *compiledPattern, hit occurrence) candidate {
	fb := pat.fallback
	matchStart := nt.sourceStart(hit.start, 0)
	matchEnd := nt.sourceEnd(hit.end, len(nt.text))
	start := matchEnd
	if fb.selectionStart != len(fb.norm) {
		start = nt.sourceStart(hit.start+fb.selectionStart, matchEnd)
	}
	end := matchEnd
	switch {
	case fb.selectionEnd == len(fb.norm):
	case fb.insertion:
		end = nt.sourceStart(hit.start+fb.selectionEnd, matchEnd)
	default:
		end = nt.sourceEnd(hit.start+fb.selectionEnd, matchEnd)
	}
	out := candidate{
		start: start, end: end,
		matchStart: matchStart, matchEnd: matchEnd,
		tuple: []int{hit.start}, literalGaps: true,
	}
	if len(pat.pairs) == 1 {
		out.selectionSpans = [][2]int{{start, end}}
	}
	return out
}

// identicalOutcomes reports whether every candidate would end up with the same
// file text. Whitespace-equivalent outcomes are unambiguous.
func identicalOutcomes(content string, op *sloppy.Operation, pat *compiledPattern, candidates []candidate) bool {
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		var outcome string
		switch op.Rewrite.Kind {
		case sloppy.RewriteInline:
			outcome = content
			spans := make([][2]int, len(candidate.selectionSpans))
			copy(spans, candidate.selectionSpans)
			for i, span := range slices.Backward(spans) {
				outcome = outcome[:span[0]] + pat.pairs[i].new + outcome[span[1]:]
			}
		default:
			outcome = content[:candidate.matchStart] + op.Rewrite.Text + content[candidate.matchEnd:]
		}
		seen[normalizeText(outcome).text] = true
	}
	return len(seen) == 1
}

// sameRewriteForAll reports whether one rewrite covers every candidate, so the
// retry can offer "apply to all".
func sameRewriteForAll(op *sloppy.Operation, pat *compiledPattern, candidates []candidate) bool {
	switch op.Rewrite.Kind {
	case sloppy.RewriteInsert:
		return true
	case sloppy.RewriteInline:
		for i, pair := range pat.pairs {
			if i >= len(candidates[0].selectionSpans) {
				return false
			}
			for _, candidate := range candidates[1:] {
				if i >= len(candidate.selectionSpans) ||
					!sameAt(candidates[0], candidate, pair.captureIndices) {
					return false
				}
			}
		}
		return true
	default:
		gaps := strings.Count(op.Rewrite.Text, gapMarker)
		compared := 0
		for i := range pat.tokens {
			if pat.tokens[i].kind != tokGap || compared >= gaps {
				continue
			}
			for _, candidate := range candidates[1:] {
				if !sameAt(candidates[0], candidate, []int{pat.tokens[i].capture}) {
					return false
				}
			}
			compared++
		}
		return true
	}
}

func sameAt(left, right candidate, indices []int) bool {
	for _, index := range indices {
		if index >= len(left.captures) || index >= len(right.captures) ||
			left.captures[index] != right.captures[index] {
			return false
		}
	}
	return true
}

func (*applier) tooBroadError(number int) error {
	return failf("Operation %d pattern is too broad; add another distinctive %s fragment.", number, gapMarker)
}

// noMatchError grounds the miss: the failed fragment, the file region it
// nearly matches, and a copy-ready retry when one exists.
func (a *applier) noMatchError(op *sloppy.Operation, pat *compiledPattern, number int) error {
	nt := normalizeText(a.content)
	literal, needle := firstLiteral(pat)
	occurrences := exactOccurrences(nt.text, needle)
	closest, closestOffset, closestScore := closestFragment(a.content, needle)

	reason := fmt.Sprintf("Failed fragment: %s could not align.", displayFragment(literal))
	offset := closestOffset
	if len(occurrences) == 0 {
		reason = fmt.Sprintf("Failed fragment: %s has 0 occurrences.", displayFragment(literal))
	} else {
		offset = nt.sourceStart(occurrences[0].start, 0)
	}

	first := fmt.Sprintf("Operation %d did not match %s. %s", number, a.path, reason)
	if op.All {
		first = fmt.Sprintf("Operation %d *** SM:EDIT all found 0 matches in %s. %s", number, a.path, reason)
	}

	var correction string
	switch {
	case len(occurrences) == 0 && closestScore < 0.35 && closest != "" && a.standalone:
		corrected := strings.Replace(pat.body, literal, closest, 1)
		correction = "Copy-ready corrected operation:\n" + operationPayload(*op, a.path, op.All, corrected)
	case a.standalone:
		correction = "No copy-ready correction — the closest current text is only a fuzzy match. " +
			"Re-read the region above and rebuild *** SM:FIND from the exact current text."
	default:
		correction = "No copy-ready correction — retrying this operation alone would drop sibling " +
			"operations. Rebuild it inside the full payload."
	}

	return failf("%s\nCurrent file content near the closest match (no re-read needed):\n%s\n%s",
		first, numberedPreview(a.content, offset), correction)
}

// ambiguityError lists the competing anchors with copy-ready retries.
func (a *applier) ambiguityError(op *sloppy.Operation, pat *compiledPattern, number int, candidates []candidate) error {
	retries := make([]string, 0, 2)
	for _, candidate := range candidates[:min(2, len(candidates))] {
		retries = append(retries, fmt.Sprintf("Near line %d:\n%s",
			lineNumberAt(a.content, candidate.start), operationPayload(*op, a.path, false, pat.body)))
	}
	allRetry := ""
	if sameRewriteForAll(op, pat, candidates) {
		allRetry = "\n\nAll candidates receive the same rewrite; retry every match:\n" + operationPayload(
			*op,
			a.path,
			true,
			pat.body,
		)
	}
	return failf(
		"Operation %d is ambiguous: %d ordered tuples match.\n\nAdd context that only the intended "+
			"match has — one of these:\n\n%s%s", number, len(candidates), strings.Join(retries, "\n\n"), allRetry)
}

// overlapError reports two operations fighting over the same original text.
func (a *applier) overlapError(previous, current plannedEdit) error {
	firstLine := lineNumberAt(a.content, previous.start)
	secondLine := lineNumberAt(a.content, current.start)
	return failf(
		"Operations %d and %d target overlapping original spans near lines %d and %d.\n\n"+
			"Conflicting candidates:\n\nOperation %d near line %d:\n%s\n\nOperation %d near line %d:\n%s\n\n"+
			"Keep whichever states the intended final text and drop the other.",
		previous.number,
		current.number,
		firstLine,
		secondLine,
		previous.number,
		firstLine,
		operationPayload(a.ops[previous.number-1], a.path, false, a.ops[previous.number-1].Pattern.Body),
		current.number,
		secondLine,
		operationPayload(a.ops[current.number-1], a.path, false, a.ops[current.number-1].Pattern.Body),
	)
}

// noOpError explains a rewrite that would not change the file, grounded at the
// text it collided with. A zero operation number blames the whole payload.
func (a *applier) noOpError(number, offset, matches int, hint string) error {
	base := fmt.Sprintf("Edits to %s made no change.", a.path)
	switch {
	case number > 0 && matches > 0:
		base = fmt.Sprintf("Operation %d *** SM:EDIT all matched %d occurrences but all make no change to %s.",
			number, matches, a.path)
	case number > 0:
		base = fmt.Sprintf("Operation %d makes no change to %s.", number, a.path)
	}
	grounding := ""
	if number > 0 {
		grounding = "\nYour rewrite normalized to text identical to these lines. Indentation-only " +
			"changes are applied verbatim; adjust the authored *** SM:PUT if another whitespace change " +
			"was intended.\nCurrent file content near the closest match (no re-read needed):\n" +
			numberedPreview(a.content, offset)
	}
	if hint != "" {
		hint = "\n" + hint
	}
	return failf("%s%s%s", base, grounding, hint)
}

// firstLiteral returns the pattern's first literal, authored and normalized.
func firstLiteral(pat *compiledPattern) (string, string) {
	for _, tok := range pat.tokens {
		if tok.kind == tokLiteral {
			return tok.text, tok.norm
		}
	}
	return pat.body, normalizeText(pat.body).text
}

// operationPayload renders one operation as a copy-ready payload fragment.
func operationPayload(op sloppy.Operation, path string, all bool, pattern string) string {
	var b strings.Builder
	b.WriteString(editHeader(path, all))
	if op.Desired {
		b.WriteString("\n*** SM:PUT\n")
		b.WriteString(op.Rewrite.Text)
		return b.String()
	}
	b.WriteString("\n*** SM:FIND\n")
	b.WriteString(pattern)
	b.WriteString("\n")
	switch op.Rewrite.Kind {
	case sloppy.RewriteReplace:
		b.WriteString("*** SM:PUT\n")
		b.WriteString(op.Rewrite.Text)
	case sloppy.RewriteInsert:
		b.WriteString("*** SM:AFTER\n")
		b.WriteString(op.Rewrite.Text)
	}
	return b.String()
}

// editHeader renders the file header, JSON-quoting paths that would otherwise
// be read as flags.
func editHeader(path string, all bool) string {
	needsQuoting := path != strings.TrimSpace(path) || strings.HasPrefix(path, "\"") ||
		strings.ContainsAny(path, "\n") || strings.EqualFold(path, "all") ||
		(strings.HasSuffix(path, " all") && len(path) > 4)
	rendered := path
	if needsQuoting {
		if quoted, err := json.Marshal(path); err == nil {
			rendered = string(quoted)
		}
	}
	if all {
		return "*** SM:EDIT " + rendered + " all"
	}
	return "*** SM:EDIT " + rendered
}

// displayFragment quotes a pattern fragment for a diagnostic: short multi-line
// fragments stay readable, everything else collapses to one quoted line.
func displayFragment(text string) string {
	if strings.Contains(text, "\n") && strings.Count(text, "\n") < 8 {
		return "\n" + text
	}
	compact := strings.Join(strings.Fields(text), " ")
	if runes := []rune(compact); len(runes) > 80 {
		compact = string(runes[:77]) + "…"
	}
	quoted, err := json.Marshal(compact)
	if err != nil {
		return compact
	}
	return string(quoted)
}

// numberedPreview shows ten numbered lines of the file around an offset.
func numberedPreview(content string, offset int) string {
	lines := strings.Split(content, "\n")
	anchor := lineNumberAt(content, offset) - 1
	start := max(anchor-4, 0)
	if len(lines)-start < 10 {
		start = max(len(lines)-10, 0)
	}
	out := make([]string, 0, 10)
	for i := start; i < len(lines) && i < start+10; i++ {
		out = append(out, fmt.Sprintf("%d: %s", i+1, lines[i]))
	}
	return strings.Join(out, "\n")
}

// lineNumberAt is the 1-based line containing an offset.
func lineNumberAt(content string, offset int) int {
	offset = min(offset, len(content))
	return strings.Count(content[:offset], "\n") + 1
}

// closestFragment finds the text in the file that most resembles pattern: the
// best matching line, then the best window inside it.
func closestFragment(content, pattern string) (string, int, float64) {
	type rankedLine struct {
		text   string
		offset int
		norm   string
		score  float64
	}
	var ranked []rankedLine
	offset := 0
	for line := range strings.SplitSeq(content, "\n") {
		norm := normalizeText(line).text
		if norm != "" {
			denominator := max(len(pattern), len(norm), 1)
			score := float64(levenshteinDistance(pattern, norm)) / float64(denominator)
			ranked = append(ranked, rankedLine{text: line, offset: offset, norm: norm, score: score})
			sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score < ranked[j].score })
			if len(ranked) > 3 {
				ranked = ranked[:3]
			}
		}
		offset += len(line) + 1
	}
	if len(ranked) == 0 {
		return pattern, 0, 1.0
	}
	first := ranked[0]
	best := rankedLine{text: first.text, offset: first.offset, score: first.score}
	if len(pattern) <= 160 {
		for _, line := range ranked {
			width := min(len(pattern), len(line.norm))
			if width == 0 {
				continue
			}
			for start := 0; start+width <= len(line.norm); start++ {
				if !isCharBoundary(line.norm, start) || !isCharBoundary(line.norm, start+width) {
					continue
				}
				candidate := line.norm[start : start+width]
				denominator := max(len(pattern), len(candidate), 1)
				score := float64(levenshteinDistance(pattern, candidate)) / float64(denominator)
				if score >= best.score {
					continue
				}
				lineNorm := normalizeText(line.text)
				rawStart := lineNorm.sourceStart(start, 0)
				rawEnd := lineNorm.sourceEnd(start+width, len(line.text))
				if rawStart > rawEnd {
					continue
				}
				best = rankedLine{
					text:   line.text[rawStart:rawEnd],
					offset: line.offset + rawStart,
					score:  score,
				}
			}
		}
	}
	return best.text, best.offset, best.score
}

func isCharBoundary(text string, at int) bool {
	if at <= 0 || at >= len(text) {
		return true
	}
	return text[at]&0xC0 != 0x80
}
