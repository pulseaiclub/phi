package edittool

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
)

// Pattern markers the matching layer still sees. The "│" divider is not among
// them: sloppy scanSelection consumes it while splitting selections, so a
// compiled pattern only carries the already-separated old and new text.
const (
	gapMarker = "…"
	selOpen   = "⟪"
	selClose  = "⟫"
)

// Matching limits mirrored from the reference engine: the search gives up
// rather than combing through pathological patterns.
const (
	maxCandidates   = 200
	maxCombinations = 20_000
	fuzzyMinPattern = 6     // bytes; shorter patterns only match exactly
	fuzzyMaxContent = 50000 // bytes
	smallContent    = 10000 // bytes
)

// matchMode selects how literally pattern text must match the file. The ladder
// runs Raw, then Normalized, then Fuzzy; each step is more tolerant but only
// claims a match the looser rules still make unique.
type matchMode uint8

const (
	modeRaw matchMode = iota
	modeNormalized
	modeFuzzy
)

type tokKind uint8

const (
	tokLiteral tokKind = iota
	tokGap
)

// patToken is one piece of a compiled FIND body.
type patToken struct {
	kind        tokKind
	text        string // literal: the authored text
	norm        string // literal: the text in the matching space
	capture     int    // gap: capture index in token order
	lineBounded bool   // gap: must stay within one line
}

// selectionTokens compiles a selection's old text into literal and gap tokens,
// so "⟪old(…)│new(…)⟫" can capture and replay like the rest of the pattern.
// An empty old text yields no tokens: the selection is a bare insertion point.
func selectionTokens(body string, sel sloppy.Selection) []patToken {
	var out []patToken
	literal := -1
	flush := func(end int) {
		if literal < 0 {
			return
		}
		text := sel.Old[literal:end]
		out = append(out, patToken{kind: tokLiteral, text: text, norm: normalizeText(text).text})
		literal = -1
	}
	for i := 0; i < len(sel.Old); {
		if !strings.HasPrefix(sel.Old[i:], gapMarker) {
			if literal < 0 {
				literal = i
			}
			_, width := utf8.DecodeRuneInString(sel.Old[i:])
			i += width
			continue
		}
		flush(i)
		bounded := strings.TrimLeft(sel.Old[i+len(gapMarker):], " \t") != "" ||
			selectionGapBounded(body, sel)
		out = append(out, patToken{kind: tokGap, lineBounded: bounded})
		i += len(gapMarker)
	}
	flush(len(sel.Old))
	return out
}

// selPair is one "⟪old│new⟫" directive resolved to token boundaries.
type selPair struct {
	tokStart, tokEnd int // token boundaries the selection covers
	captureIndices   []int
	lineInsertion    bool // empty selection sitting alone on its line
	new              string
}

// edgeGaps records "…" elisions at FIND body edges. They match and capture
// nothing.
type edgeGaps struct {
	leading  bool
	trailing bool
}

// literalFallback is the FIND body read as one literal, "…" glyphs and all,
// for files that spell the gap out.
type literalFallback struct {
	norm           string
	selectionStart int
	selectionEnd   int
	insertion      bool
}

// compiledPattern is a FIND body prepared for matching: tokens in matching
// order, selections resolved to spans, and the span geometry the rewrite
// replaces.
type compiledPattern struct {
	tokens           []patToken
	edges            edgeGaps
	pairs            []selPair
	selStart, selEnd int // token boundaries of the rewrite span
	insertion        bool
	lineInsertion    bool
	selectedCaptures []int
	selectionRanges  [][2]int // per-selection spans, only when several exist
	fallback         *literalFallback
	body             string
}

// occurrence is one match of a literal in the searched text.
type occurrence struct {
	start, end       int
	distance         int
	punctuationEdits int
}

// candidate is one way to anchor the whole pattern in the file.
type candidate struct {
	start, end     int // source span the rewrite replaces
	matchStart     int // source start of the literals actually matched
	matchEnd       int // source end of the literals actually matched
	captures       []string
	selectionSpans [][2]int
	tuple          []int // chosen occurrence start per literal
	literalGaps    bool  // located through the literal fallback
}

// compilePattern turns the parser's FIND body into the matching form.
func compilePattern(pat sloppy.Pattern, number int) (*compiledPattern, error) {
	if strings.TrimSpace(pat.Body) == "" {
		return nil, failf("Operation %d has an empty pattern.", number)
	}
	tokOfSel, err := selectionTokenIndices(pat)
	if err != nil {
		return nil, failf("Operation %d %s", number, err)
	}

	c := &compiledPattern{
		body:  pat.Body,
		edges: edgeGaps{leading: pat.EdgeGaps.Leading, trailing: pat.EdgeGaps.Trailing},
	}
	selectionAt := make(map[int][]patToken, len(pat.Selections))
	for i, sel := range pat.Selections {
		selectionAt[tokOfSel[i]] = selectionTokens(pat.Body, sel)
	}
	remap := make([]int, len(pat.Tokens)+1)
	for i, tok := range pat.Tokens {
		remap[i] = len(c.tokens)
		if expanded, ok := selectionAt[i]; ok {
			c.tokens = append(c.tokens, expanded...)
			continue
		}
		if tok.Kind == sloppy.PatternTokenGap {
			c.tokens = append(c.tokens, patToken{kind: tokGap, lineBounded: tok.LineBounded})
			continue
		}
		if tok.Text == "" {
			continue // an edge gap ate this literal's joining newline; the neighbors abut
		}
		c.tokens = append(c.tokens, patToken{
			kind: tokLiteral, text: tok.Text, norm: normalizeText(tok.Text).text,
		})
	}
	remap[len(pat.Tokens)] = len(c.tokens)
	renumberCaptures(c)

	for i := 1; i < len(c.tokens); i++ {
		if c.tokens[i].kind == tokGap && c.tokens[i-1].kind == tokGap {
			return nil, failf("Operation %d has adjacent %s; use one ellipsis.", number, gapMarker)
		}
	}
	if !hasVisibleText(c) {
		return nil, failf("Operation %d pattern is too generic; include a distinctive name or statement.", number)
	}

	for i, sel := range pat.Selections {
		start := remap[tokOfSel[i]]
		end := start + len(selectionAt[tokOfSel[i]])
		pair := selPair{
			tokStart:      start,
			tokEnd:        end,
			lineInsertion: start == end && markerLineAlone(pat.Body, sel.Start, sel.End),
			new:           sel.New,
		}
		for k := start; k < end; k++ {
			if c.tokens[k].kind == tokGap {
				pair.captureIndices = append(pair.captureIndices, c.tokens[k].capture)
			}
		}
		c.pairs = append(c.pairs, pair)
	}

	boundaries := make([]int, 0, 2*len(c.pairs))
	for _, pair := range c.pairs {
		boundaries = append(boundaries, pair.tokStart, pair.tokEnd)
	}
	c.insertion = len(boundaries) == 1 || (len(boundaries) == 2 && boundaries[0] == boundaries[1])
	explicitSingle := len(boundaries) == 2 && boundaries[0] != boundaries[1]
	switch {
	case c.insertion || explicitSingle:
		c.selStart = boundaries[0]
	default:
		c.selStart = 0
	}
	switch {
	case c.insertion:
		c.selEnd = c.selStart
	case explicitSingle:
		c.selEnd = boundaries[1]
	default:
		c.selEnd = len(c.tokens)
	}
	c.lineInsertion = c.insertion && len(c.pairs) > 0 && c.pairs[0].lineInsertion

	if len(c.pairs) > 1 {
		for _, pair := range c.pairs {
			c.selectionRanges = append(c.selectionRanges, [2]int{pair.tokStart, pair.tokEnd})
		}
	}
	for i := c.selStart; i < c.selEnd && i < len(c.tokens); i++ {
		if c.tokens[i].kind == tokGap {
			c.selectedCaptures = append(c.selectedCaptures, c.tokens[i].capture)
		}
	}

	if len(c.selectionRanges) == 0 && strings.Contains(pat.Body, gapMarker) {
		c.fallback = buildLiteralFallback(pat, c.insertion)
	}
	return c, nil
}

// selectionTokenIndices maps every selection to the literal token the parser
// emitted for its old text.
func selectionTokenIndices(pat sloppy.Pattern) ([]int, error) {
	out := make([]int, len(pat.Selections))
	cursor := 0
	for i, sel := range pat.Selections {
		oldStart := sel.Start + len(selOpen)
		found := -1
		for j := cursor; j < len(pat.Tokens); j++ {
			tok := pat.Tokens[j]
			if tok.Kind == sloppy.PatternTokenLiteral && tok.Start == oldStart && tok.End == oldStart+len(sel.Old) {
				found = j
				cursor = j + 1
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("selection %d does not align with its pattern tokens", i+1)
		}
		out[i] = found
	}
	return out, nil
}

// selectionGapBounded reports whether a "…" wrapped in a selection has content
// after the directive on its line.
func selectionGapBounded(body string, sel sloppy.Selection) bool {
	rest := body[sel.End:]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimLeft(rest, " \t") != ""
}

// markerLineAlone reports whether the selection directive is the only content
// on its line.
func markerLineAlone(body string, start, end int) bool {
	lineStart := strings.LastIndexByte(body[:start], '\n') + 1
	lineEnd := len(body)
	if j := strings.IndexByte(body[end:], '\n'); j >= 0 {
		lineEnd = end + j
	}
	return strings.TrimSpace(body[lineStart:start]+body[end:lineEnd]) == ""
}

// renumberCaptures assigns dense capture indices in token order, so a PUT "…"
// replays them in the order they appear in the FIND body.
func renumberCaptures(c *compiledPattern) {
	capture := 0
	for i := range c.tokens {
		if c.tokens[i].kind == tokGap {
			c.tokens[i].capture = capture
			capture++
		}
	}
}

// hasVisibleText rejects patterns made only of punctuation, which would match
// anywhere.
func hasVisibleText(c *compiledPattern) bool {
	if len(c.tokens) == 0 {
		return false
	}
	for _, tok := range c.tokens {
		if tok.kind != tokLiteral {
			continue
		}
		for _, r := range tok.text {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' {
				return true
			}
		}
	}
	return false
}

// buildLiteralFallback reads the FIND body as one literal with "…" spelled out,
// tracking where the selection sits inside it.
func buildLiteralFallback(pat sloppy.Pattern, insertion bool) *literalFallback {
	var (
		b              strings.Builder
		pos            int
		selectionStart int
		selectionEnd   int
	)
	for i, sel := range pat.Selections {
		b.WriteString(pat.Body[pos:sel.Start])
		if i == 0 {
			selectionStart = len(normalizeText(b.String()).text)
		}
		b.WriteString(sel.Old)
		if i == 0 {
			selectionEnd = len(normalizeText(b.String()).text)
		}
		pos = sel.End
	}
	b.WriteString(pat.Body[pos:])
	return &literalFallback{
		norm:           normalizeText(b.String()).text,
		selectionStart: selectionStart,
		selectionEnd:   selectionEnd,
		insertion:      insertion,
	}
}

// exactOccurrences reports every place pattern occurs in content, including
// overlapping ones. An empty pattern matches nothing.
func exactOccurrences(content, pattern string) []occurrence {
	if pattern == "" {
		return nil
	}
	var out []occurrence
	for i := 0; i+len(pattern) <= len(content); {
		if strings.HasPrefix(content[i:], pattern) {
			out = append(out, occurrence{start: i, end: i + len(pattern)})
		}
		_, width := utf8.DecodeRuneInString(content[i:])
		i += width
	}
	return out
}

// operatorSignature is the text with names stripped, so two runs of operators
// can only match when they are the same run.
func operatorSignature(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '$' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// differsByOnePunctuationInsertion reports whether the two strings differ by
// exactly one inserted punctuation character. Bracket insertions never count:
// they change the code's structure.
func differsByOnePunctuationInsertion(left, right string) bool {
	a, b := []rune(left), []rune(right)
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) != 1 {
		return false
	}
	at := 0
	for at < len(a) && a[at] == b[at] {
		at++
	}
	if strings.ContainsRune("{}()[]", b[at]) {
		return false
	}
	for i := at; i < len(a); i++ {
		if a[i] != b[i+1] {
			return false
		}
	}
	return true
}

// fuzzyOccurrences finds near matches of pattern in matching-space text: the
// same operator run, at most a few character edits away. Short patterns only
// match exactly, and oversized content is never fuzzed.
func fuzzyOccurrences(content, pattern string, allowPunctuation bool) []occurrence {
	if content == "" || len(content) > fuzzyMaxContent {
		return nil
	}
	if len(pattern) < fuzzyMinPattern {
		return exactOccurrences(content, pattern)
	}
	limit := min(max(len(pattern)/8, 1), 3) //nolint:mnd // ~0.12 of the pattern length, capped at 3
	seedLen := min(max(len(pattern)-limit, 3), 5)

	contentRunes, contentAt := runesWithOffsets(content)
	patternRunes := []rune(pattern)
	starts := fuzzyStarts(contentRunes, patternRunes, seedLen, limit)
	if len(starts) == 0 && len(content) <= smallContent {
		starts = make([]int, len(contentRunes))
		for i := range starts {
			starts[i] = i
		}
	}

	var out []occurrence
	for _, start := range starts {
		best := occurrence{}
		found := false
		for length := len(patternRunes) - limit; length <= len(patternRunes)+limit; length++ {
			if length <= 0 || start+length > len(contentRunes) {
				continue
			}
			text := content[contentAt[start]:contentAt[start+length]]
			signatureMatch := operatorSignature(text) == operatorSignature(pattern)
			if !signatureMatch && (!allowPunctuation || !differsByOnePunctuationInsertion(pattern, text)) {
				continue
			}
			distance := levenshteinDistance(pattern, text)
			if distance > limit {
				continue
			}
			if !found || distance < best.distance {
				best = occurrence{
					start: contentAt[start], end: contentAt[start+length],
					distance: distance, punctuationEdits: boolInt(!signatureMatch),
				}
				found = true
			}
		}
		if found {
			out = append(out, best)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].distance != out[j].distance {
			return out[i].distance < out[j].distance
		}
		return out[i].start < out[j].start
	})
	kept := out[:0]
	for _, occ := range out {
		overlap := false
		for _, prev := range kept {
			if occ.start < prev.end && prev.start < occ.end {
				overlap = true
				break
			}
		}
		if !overlap {
			kept = append(kept, occ)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].start < kept[j].start })
	return kept
}

// fuzzyStarts returns rune-indexed candidate starts: every seed hit shifted by
// the pattern offset it came from, widened by the edit limit on both sides.
func fuzzyStarts(contentRunes, patternRunes []rune, seedLen, limit int) []int {
	seen := make(map[int]bool)
	var starts []int
	for _, seedAt := range []int{0, (len(patternRunes) - seedLen) / 2, len(patternRunes) - seedLen} {
		if seedAt < 0 || seedAt+seedLen > len(patternRunes) {
			continue
		}
		seed := string(patternRunes[seedAt : seedAt+seedLen])
		for at := 0; at+seedLen <= len(contentRunes); at++ {
			if string(contentRunes[at:at+seedLen]) != seed {
				continue
			}
			for delta := -limit; delta <= limit; delta++ {
				start := at - seedAt + delta
				if start < 0 || start >= len(contentRunes) || seen[start] {
					continue
				}
				seen[start] = true
				starts = append(starts, start)
			}
		}
	}
	sort.Ints(starts)
	return starts
}

// runesWithOffsets splits text into runes with the byte offset of each, plus
// the end offset.
func runesWithOffsets(text string) ([]rune, []int) {
	runes := make([]rune, 0, len(text))
	at := make([]int, 0, len(text)+1)
	for offset := 0; offset < len(text); {
		r, width := utf8.DecodeRuneInString(text[offset:])
		runes = append(runes, r)
		at = append(at, offset)
		offset += width
	}
	at = append(at, len(text))
	return runes, at
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
