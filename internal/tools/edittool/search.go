package edittool

import (
	"sort"
	"strings"
)

// search backtracks over the pattern's literals, choosing one occurrence of
// each in document order. Gaps capture whatever lies between the chosen
// literals; the pruning rules keep those captures sane.
type search struct {
	content      string
	nt           normText
	pat          *compiledPattern
	mode         matchMode
	litIdx       []int
	occurrences  map[int][]occurrence
	allowPunct   bool
	chosen       map[int]occurrence
	candidates   []candidate
	combinations int
	overflow     bool
}

// collectCandidates returns every way to anchor the pattern in content, plus
// whether the search gave up early.
func collectCandidates(
	content string,
	nt normText,
	pat *compiledPattern,
	mode matchMode,
	allowPunctuation bool,
) ([]candidate, bool) {
	s := &search{
		content:     content,
		nt:          nt,
		pat:         pat,
		mode:        mode,
		occurrences: make(map[int][]occurrence, len(pat.tokens)),
		allowPunct:  allowPunctuation,
		chosen:      make(map[int]occurrence, len(pat.tokens)),
	}
	for i, tok := range pat.tokens {
		if tok.kind == tokLiteral {
			s.litIdx = append(s.litIdx, i)
		}
	}
	for _, i := range s.litIdx {
		tok := pat.tokens[i]
		var occ []occurrence
		switch mode {
		case modeRaw:
			occ = exactOccurrences(content, tok.text)
		case modeNormalized:
			occ = exactOccurrences(nt.text, tok.norm)
		case modeFuzzy:
			occ = fuzzyOccurrences(nt.text, tok.norm, allowPunctuation)
		}
		if len(occ) == 0 {
			return nil, false
		}
		s.occurrences[i] = occ
	}
	if len(s.litIdx) == 0 {
		return nil, false
	}
	s.visit(0)
	return s.finish(), s.overflow
}

// sourceStart maps an offset in the searched text to the source byte where the
// matched text starts.
func (s *search) sourceStart(offset int) int {
	if s.mode == modeRaw {
		return offset
	}
	return s.nt.sourceStart(offset, len(s.content))
}

func (s *search) sourceEnd(offset int) int {
	if s.mode == modeRaw {
		return offset
	}
	return s.nt.sourceEnd(offset, len(s.content))
}

func (s *search) visit(position int) {
	if s.overflow {
		return
	}
	if len(s.candidates) >= maxCandidates || s.combinations >= maxCombinations {
		s.overflow = true
		return
	}
	if position == len(s.litIdx) {
		s.combinations++
		s.record()
		return
	}
	tokenIndex := s.litIdx[position]
	var previous *occurrence
	var between []int
	if position > 0 {
		prev := s.chosen[s.litIdx[position-1]]
		previous = &prev
		for i := s.litIdx[position-1] + 1; i < tokenIndex; i++ {
			if s.pat.tokens[i].kind == tokGap {
				between = append(between, i)
			}
		}
	}
	lineBounded := false
	for _, i := range between {
		lineBounded = lineBounded || s.pat.tokens[i].lineBounded
	}
	for _, occ := range s.occurrences[tokenIndex] {
		if previous != nil {
			if len(between) > 0 {
				if occ.start < previous.end {
					continue
				}
			} else if occ.start != previous.end {
				continue
			}
			if lineBounded && strings.Contains(s.betweenText(*previous, occ), "\n") {
				continue
			}
		}
		s.chosen[tokenIndex] = occ
		s.visit(position + 1)
		delete(s.chosen, tokenIndex)
	}
}

// betweenText is the source text a gap would capture between two occurrences.
func (s *search) betweenText(previous, next occurrence) string {
	start := s.sourceEnd(previous.end)
	end := s.sourceStart(next.start)
	if start > end || end > len(s.content) {
		return ""
	}
	return s.content[start:end]
}

// record turns a full assignment into a candidate.
func (s *search) record() {
	if s.allowPunct {
		edits := 0
		for _, i := range s.litIdx {
			edits += s.chosen[i].punctuationEdits
		}
		if edits > 1 {
			return
		}
	}
	var content *string
	if s.pat.lineInsertion {
		content = &s.content
	}
	kindStart, kindEnd := 0, 1
	if s.pat.insertion {
		kindStart, kindEnd = 2, 2
	}
	start := s.resolveBoundary(s.pat.selStart, kindStart, content)
	end := s.resolveBoundary(s.pat.selEnd, kindEnd, content)
	if start > end {
		return
	}

	captures := make([]string, 0, len(s.pat.tokens))
	for i, tok := range s.pat.tokens {
		if tok.kind != tokGap {
			continue
		}
		before, okBefore := s.precedingLiteral(i)
		after, okAfter := s.followingLiteral(i + 1)
		if !okBefore || !okAfter {
			return
		}
		captures = append(captures, s.betweenText(s.chosen[before], s.chosen[after]))
	}

	spans := make([][2]int, 0, len(s.pat.pairs))
	for _, pair := range s.pat.pairs {
		kindStart, kindEnd := 0, 1
		if pair.tokStart == pair.tokEnd {
			kindStart, kindEnd = 2, 2
		}
		var pairContent *string
		if pair.lineInsertion {
			pairContent = &s.content
		}
		spans = append(spans, [2]int{
			s.resolveBoundary(pair.tokStart, kindStart, pairContent),
			s.resolveBoundary(pair.tokEnd, kindEnd, pairContent),
		})
	}
	if !selectionSpansUsable(spans, start, end) {
		return
	}

	first := s.chosen[s.litIdx[0]]
	last := s.chosen[s.litIdx[len(s.litIdx)-1]]
	tuple := make([]int, 0, len(s.litIdx))
	for _, i := range s.litIdx {
		tuple = append(tuple, s.chosen[i].start)
	}
	next := candidate{
		start: start, end: end,
		matchStart: s.sourceStart(first.start), matchEnd: s.sourceEnd(last.end),
		captures: captures, selectionSpans: spans, tuple: tuple,
	}
	for i := range s.candidates {
		existing := &s.candidates[i]
		if existing.start != next.start || existing.end != next.end {
			continue
		}
		sameCaptures := true
		for _, index := range s.pat.selectedCaptures {
			if index < len(existing.captures) && index < len(next.captures) &&
				existing.captures[index] != next.captures[index] {
				sameCaptures = false
				break
			}
		}
		if sameCaptures {
			if next.matchEnd-next.matchStart < existing.matchEnd-existing.matchStart {
				*existing = next
			}
			return
		}
	}
	s.candidates = append(s.candidates, next)
}

// precedingLiteral returns the nearest literal token before boundary that has
// a chosen occurrence.
func (s *search) precedingLiteral(boundary int) (int, bool) {
	for i := boundary - 1; i >= 0; i-- {
		if s.pat.tokens[i].kind == tokLiteral {
			if _, ok := s.chosen[i]; ok {
				return i, true
			}
		}
	}
	return 0, false
}

func (s *search) followingLiteral(boundary int) (int, bool) {
	for i := boundary; i < len(s.pat.tokens); i++ {
		if s.pat.tokens[i].kind == tokLiteral {
			if _, ok := s.chosen[i]; ok {
				return i, true
			}
		}
	}
	return 0, false
}

// resolveBoundary maps a token boundary to a source byte offset: a boundary
// that a literal borders lands on that literal, one that only follows a gap
// lands at the end of the captured text.
func (s *search) resolveBoundary(boundary, kind int, content *string) int {
	previousIndex, hasPrevious := s.precedingLiteral(boundary)
	nextIndex, hasNext := s.followingLiteral(boundary)
	var previous, next *occurrence
	if hasPrevious {
		entry := s.chosen[previousIndex]
		previous = &entry
	}
	if hasNext {
		entry := s.chosen[nextIndex]
		next = &entry
	}
	immediatePrevious := boundary > 0 && s.pat.tokens[boundary-1].kind == tokLiteral
	immediateNext := boundary < len(s.pat.tokens) && s.pat.tokens[boundary].kind == tokLiteral

	startAt := s.sourceStart
	endAt := s.sourceEnd
	if kind == 2 {
		if next != nil {
			return startAt(next.start)
		}
		if previous != nil {
			offset := endAt(previous.end)
			if content != nil && offset > 0 && offset <= len(*content) &&
				(*content)[offset-1] != '\n' {
				if newline := strings.IndexByte((*content)[offset:], '\n'); newline >= 0 {
					return offset + newline + 1
				}
			}
			return offset
		}
	}
	if kind == 0 {
		if immediateNext && next != nil {
			return startAt(next.start)
		}
		if previous != nil {
			return endAt(previous.end)
		}
		if next != nil {
			return startAt(next.start)
		}
	}
	if immediatePrevious && previous != nil {
		return endAt(previous.end)
	}
	if next != nil {
		return startAt(next.start)
	}
	if previous != nil {
		return endAt(previous.end)
	}
	return 0
}

// selectionSpansUsable rejects spans that would overlap or fall outside the
// rewrite range: garbled selection markers surface as a miss, never as a
// corrupted splice.
func selectionSpansUsable(spans [][2]int, start, end int) bool {
	ordered := append([][2]int(nil), spans...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i][0] != ordered[j][0] {
			return ordered[i][0] < ordered[j][0]
		}
		return ordered[i][1] < ordered[j][1]
	})
	cursor := start
	previousStart := -1
	for _, span := range ordered {
		spanStart, spanEnd := span[0], span[1]
		if spanStart < cursor || spanStart > spanEnd || spanEnd > end || spanStart == previousStart {
			return false
		}
		previousStart = spanStart
		cursor = spanEnd
	}
	return true
}

// finish drops candidates shadowed by a shorter match at the same start and
// orders the survivors.
func (s *search) finish() []candidate {
	all := append([]candidate(nil), s.candidates...)
	out := all[:0]
	for _, candidate := range s.candidates {
		shadowed := false
		for _, other := range all {
			if other.matchStart == candidate.matchStart && other.matchEnd < candidate.matchEnd {
				shadowed = true
				break
			}
		}
		if !shadowed {
			out = append(out, candidate)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if left.start != right.start {
			return left.start < right.start
		}
		if left.matchStart != right.matchStart {
			return left.matchStart < right.matchStart
		}
		if left.matchEnd != right.matchEnd {
			return left.matchEnd < right.matchEnd
		}
		return compareTuples(left.tuple, right.tuple) < 0
	})
	return out
}

func compareTuples(left, right []int) int {
	for i := range left {
		if i >= len(right) {
			return 1
		}
		if left[i] != right[i] {
			if left[i] < right[i] {
				return -1
			}
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	return 0
}
