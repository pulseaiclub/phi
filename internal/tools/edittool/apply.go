package edittool

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
)

// atomicityNotice leads every failure: the payload applies all or nothing.
const atomicityNotice = "No operations were applied — ops apply atomically; re-send the full corrected payload."

// applyError carries a message written for the model; it surfaces verbatim.
type applyError struct{ msg string }

func (e applyError) Error() string { return e.msg }

func failf(format string, args ...any) error { return applyError{fmt.Sprintf(format, args...)} }

// Result is the outcome of applying one file section.
type Result struct {
	Content string
	Notes   []string
}

// plannedEdit is one splice into the original content.
type plannedEdit struct {
	start, end  int
	replacement string
	number      int
}

// applier runs one section's operations against the original content.
type applier struct {
	content    string
	path       string
	ops        []sloppy.Operation
	standalone bool
	notes      []string
}

// Apply runs a section's operations against content. Operations anchor to the
// original text, so earlier edits never shift later anchors; any failure
// leaves the content untouched.
func Apply(content string, section sloppy.Section, standalone bool) (Result, error) {
	a := &applier{content: content, path: section.Path, ops: section.Ops, standalone: standalone}
	out, err := a.run()
	if err != nil {
		message := err.Error()
		if !strings.Contains(message, atomicityNotice) {
			message = atomicityNotice + "\n" + message
		}
		return Result{}, applyError{message}
	}
	return Result{Content: out, Notes: a.notes}, nil
}

func (a *applier) run() (string, error) {
	planned, err := a.plan()
	if err != nil {
		return "", err
	}
	if len(planned) == 0 {
		if len(a.notes) > 0 {
			return a.content, nil
		}
		return "", a.noOpError(0, 0, 0, "")
	}
	sort.SliceStable(planned, func(i, j int) bool {
		if planned[i].start != planned[j].start {
			return planned[i].start < planned[j].start
		}
		return planned[i].end < planned[j].end
	})
	ordered := make([]plannedEdit, 0, len(planned))
	for _, current := range planned {
		if len(ordered) == 0 {
			ordered = append(ordered, current)
			continue
		}
		previous := &ordered[len(ordered)-1]
		overlaps := current.start < previous.end ||
			(current.start == previous.start && current.end == current.start && previous.end == previous.start)
		if !overlaps {
			ordered = append(ordered, current)
			continue
		}
		merged, ok := reconcileOverlap(a.content, *previous, current)
		if ok {
			*previous = merged
			continue
		}
		if previous.number == current.number {
			continue
		}
		return "", a.overlapError(*previous, current)
	}

	result := a.content
	for _, edit := range slices.Backward(ordered) {
		result = result[:edit.start] + edit.replacement + result[edit.end:]
	}
	if result == a.content && len(a.notes) == 0 {
		return "", a.noOpError(0, 0, 0, "")
	}
	return result, nil
}

// plan locates every operation and turns it into splices against the original
// content.
func (a *applier) plan() ([]plannedEdit, error) {
	var (
		planned   []plannedEdit
		lastMatch int
	)
	for index := range a.ops {
		op := &a.ops[index]
		number := index + 1
		if op.Desired {
			edits, err := a.planDesired(op, number)
			if err != nil {
				return nil, err
			}
			planned = append(planned, edits...)
			continue
		}

		pat, err := compilePattern(op.Pattern, number)
		if err != nil {
			return nil, err
		}
		candidates, err := a.locate(op, pat, number)
		if err != nil {
			return nil, err
		}
		if op.All {
			sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].start > candidates[j].start })
		}

		switch op.Rewrite.Kind {
		case sloppy.RewriteInsert:
			if op.Rewrite.Text == "" {
				return nil, failf(
					"Operation %d *** %s body is empty; it inserts nothing. List the new lines after "+
						"*** %s, or drop the operation.", number, sloppy.RewriteInsert, sloppy.RewriteInsert)
			}
			for _, candidate := range candidates {
				offset := insertionOffset(a.content, candidate.matchEnd)
				replacement := "\n" + strings.TrimSuffix(op.Rewrite.Text, "\n")
				planned = append(
					planned,
					plannedEdit{start: offset, end: offset, replacement: replacement, number: number},
				)
				lastMatch = candidate.matchStart
			}

		case sloppy.RewriteInline:
			changes := 0
			for _, candidate := range candidates {
				order := make([]int, 0, len(candidate.selectionSpans))
				for i := range candidate.selectionSpans {
					order = append(order, i)
				}
				sort.SliceStable(order, func(i, j int) bool {
					return candidate.selectionSpans[order[i]][0] > candidate.selectionSpans[order[j]][0]
				})
				for _, selection := range order {
					edit, changed, err := prepareInline(
						a.content, candidate, candidate.selectionSpans[selection], &pat.pairs[selection], pat, number)
					if err != nil {
						return nil, err
					}
					if !changed {
						continue
					}
					planned = append(planned, edit)
					changes++
				}
				lastMatch = candidate.matchStart
			}
			if changes == 0 {
				hint := "The stated text equals the current text and never changes the file. " +
					"Restate the edit with the actual change; do not drop the operation."
				return nil, a.noOpError(number, lastMatch, matchCount(op, candidates), hint)
			}

		default:
			changes := 0
			for _, candidate := range candidates {
				span := [2]int{candidate.matchStart, candidate.matchEnd}
				replacement, err := renderRewrite(op.Rewrite.Text, pat, candidate, number)
				if err != nil {
					return nil, err
				}
				if replacement == "" {
					deleted := strings.Count(a.content[span[0]:span[1]], "\n") + 1
					a.notes = append(a.notes, fmt.Sprintf(
						"Note: operation %d deleted %d line(s); an empty *** SM:PUT means deletion — "+
							"resend with the final text if you meant to replace.", number, deleted))
					span = expandFullLineDeletion(a.content, span)
				}
				if a.content[span[0]:span[1]] == replacement {
					if op.All {
						continue
					}
					return nil, a.noOpError(number, candidate.matchStart, 0, "")
				}
				planned = append(
					planned,
					plannedEdit{start: span[0], end: span[1], replacement: replacement, number: number},
				)
				changes++
				lastMatch = candidate.matchStart
			}
			if changes == 0 {
				return nil, a.noOpError(number, lastMatch, matchCount(op, candidates), "")
			}
		}
	}
	return planned, nil
}

// matchCount reports how many matches an all-match operation worked on.
func matchCount(op *sloppy.Operation, candidates []candidate) int {
	if op.All {
		return len(candidates)
	}
	return 0
}

// planDesired applies an anchor-less PUT: the body states text the file should
// hold. An already-satisfied assertion is a note; a near-matching block is
// upgraded; anything else fails closed.
func (a *applier) planDesired(op *sloppy.Operation, number int) ([]plannedEdit, error) {
	desired := op.Rewrite.Text
	if normalizedContains(a.content, desired) {
		a.notes = append(a.notes, fmt.Sprintf(
			"Note: operation %d already matches the file; no change was needed there.", number))
		return nil, nil
	}
	span, ok := closestDesiredSpan(a.content, desired)
	if !ok {
		return nil, failf(
			"Operation %d states desired text, but no block in %s resembles it. "+
				"Add *** SM:FIND with the current text and *** SM:PUT with the final text.", number, a.path)
	}
	a.notes = append(a.notes, fmt.Sprintf(
		"Note: operation %d stated desired text without markers; the closest matching block was "+
			"replaced with it. State the current text in *** SM:FIND and the new text in *** SM:PUT.", number))
	return []plannedEdit{{start: span[0], end: span[1], replacement: desired, number: number}}, nil
}

// insertionOffset lands a line insertion before the newline that ends the
// anchored line, keeping the file's end-of-file convention.
func insertionOffset(content string, end int) int {
	if end > 0 && end <= len(content) && content[end-1] == '\n' {
		return end - 1
	}
	if end < len(content) {
		if newline := strings.IndexByte(content[end:], '\n'); newline >= 0 {
			return end + newline
		}
	}
	return len(content)
}

// prepareInline turns one selection into a splice: the selection's new text,
// with gap replay, replacing just the selected span.
func prepareInline(
	content string,
	candidate candidate,
	span [2]int,
	pair *selPair,
	pat *compiledPattern,
	number int,
) (plannedEdit, bool, error) {
	start, end := span[0], span[1]
	desired := pair.new
	blankSeparated := false
	if start == end && strings.HasPrefix(desired, "\n") && start > 0 && content[start-1] == '\n' {
		desired = strings.TrimPrefix(desired, "\n")
		blankSeparated = start >= 2 && content[start-2] == '\n'
	}
	framed := desired
	if pair.lineInsertion {
		framed = frameLineInsertion(content, start, desired, blankSeparated)
	}
	replacement, err := renderPair(framed, pair, candidate, pat, number)
	if err != nil {
		return plannedEdit{}, false, err
	}
	if replacement == "" && start != end {
		return plannedEdit{
			start:  expandFullLineDeletion(content, [2]int{start, end})[0],
			end:    expandFullLineDeletion(content, [2]int{start, end})[1],
			number: number,
		}, true, nil
	}
	if content[start:end] == replacement {
		return plannedEdit{}, false, nil
	}
	return plannedEdit{start: start, end: end, replacement: replacement, number: number}, true, nil
}

// renderPair replays captures inside one selection's replacement.
func renderPair(text string, pair *selPair, candidate candidate, pat *compiledPattern, number int) (string, error) {
	edges := edgeGaps{}
	if !candidate.literalGaps {
		edges.leading = pat.edges.leading && pair.tokStart == 0
		edges.trailing = pat.edges.trailing && pair.tokEnd == len(pat.tokens)
	}
	return render(text, pair.captureIndices, candidate.captures, edges, number)
}

// renderRewrite replays FIND captures through a PUT body covering the match.
func renderRewrite(text string, pat *compiledPattern, candidate candidate, number int) (string, error) {
	if strings.ContainsAny(text, selOpen+selClose) {
		return "", failf(
			"Operation %d has selection markers in *** SM:PUT; *** SM:FIND is current text, "+
				"*** SM:PUT is final text.", number)
	}
	base := text
	if strings.TrimSpace(base) == "" {
		base = ""
	}
	indices := make([]int, 0, len(pat.tokens))
	for _, tok := range pat.tokens {
		if tok.kind == tokGap {
			indices = append(indices, tok.capture)
		}
	}
	edges := pat.edges
	if candidate.literalGaps {
		edges = edgeGaps{}
		indices = nil
	}
	return render(base, indices, candidate.captures, edges, number)
}

// render expands a rewrite body: "…" re-emits the next FIND capture in order,
// staying literal when no capture remains or when the capture would smuggle
// newlines into a mid-line gap.
func render(text string, indices []int, captures []string, edges edgeGaps, number int) (string, error) {
	text = stripEdgeGaps(text, edges, len(indices))
	var (
		out    strings.Builder
		marker int
	)
	for i := 0; i < len(text); {
		if !strings.HasPrefix(text[i:], gapMarker) {
			_, width := utf8.DecodeRuneInString(text[i:])
			out.WriteString(text[i : i+width])
			i += width
			continue
		}
		lineStart := strings.LastIndexByte(text[:i], '\n') + 1
		lineEnd := len(text)
		if j := strings.IndexByte(text[i+len(gapMarker):], '\n'); j >= 0 {
			lineEnd = i + len(gapMarker) + j
		}
		line := text[lineStart:lineEnd]
		if marker >= len(indices) {
			if strings.TrimSpace(line) == gapMarker {
				return "", failf(
					"Operation %d *** SM:PUT has a whole-line %s with no *** SM:FIND gap to re-emit. "+
						"*** SM:PUT is final text written verbatim: type the elided lines out, or add a "+
						"matching %s gap to *** SM:FIND. To write a literal %s line, use the write tool.",
					number, gapMarker, gapMarker, gapMarker)
			}
			out.WriteString(gapMarker)
			i += len(gapMarker)
			continue
		}
		capture := ""
		if indices[marker] < len(captures) {
			capture = captures[indices[marker]]
		}
		openEnded := strings.TrimSpace(line) == gapMarker ||
			strings.TrimSpace(text[i+len(gapMarker):lineEnd]) == ""
		if strings.Contains(capture, "\n") && !openEnded {
			out.WriteString(gapMarker)
		} else {
			out.WriteString(capture)
			marker++
		}
		i += len(gapMarker)
	}
	return out.String(), nil
}

// stripEdgeGaps drops the "…" that re-emits an edge gap: the edge captured
// nothing, so replaying it writes nothing. A whole-line edge "…" takes its
// joining newline with it.
func stripEdgeGaps(text string, edges edgeGaps, inner int) string {
	if edges.leading {
		at := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
		if strings.HasPrefix(text[at:], gapMarker) {
			after := at + len(gapMarker)
			lineEnd := len(text)
			if j := strings.IndexByte(text[after:], '\n'); j >= 0 {
				lineEnd = after + j
			}
			stripped := text[:at] + text[after:]
			if strings.TrimSpace(text[after:lineEnd]) == "" {
				lineStart := strings.LastIndexByte(text[:at], '\n') + 1
				stripped = text[:lineStart] + text[min(lineEnd+1, len(text)):]
			}
			if strings.TrimSpace(stripped) != "" {
				text = stripped
			}
		}
	}
	if edges.trailing && strings.Count(text, gapMarker) > inner {
		end := len(strings.TrimRightFunc(text, unicode.IsSpace))
		if strings.HasSuffix(text[:end], gapMarker) {
			at := end - len(gapMarker)
			lineStart := strings.LastIndexByte(text[:at], '\n') + 1
			cut := at
			if strings.TrimSpace(text[lineStart:at]) == "" {
				prefix := strings.TrimSuffix(strings.TrimSuffix(text[:lineStart], "\n"), "\r")
				cut = len(prefix)
			}
			if stripped := text[:cut]; strings.TrimSpace(stripped) != "" {
				text = stripped
			}
		}
	}
	return text
}

// frameLineInsertion gives an inserted block the line shape it needs at its
// landing spot.
func frameLineInsertion(content string, offset int, desired string, blankSeparated bool) string {
	if desired == "" {
		return ""
	}
	if offset == len(content) && offset > 0 && content[offset-1] != '\n' {
		if strings.HasPrefix(desired, "\n") {
			return desired
		}
		return "\n" + desired
	}
	framed := desired
	if !strings.HasSuffix(framed, "\n") {
		framed += "\n"
	}
	atEnd := offset >= len(content)
	if blankSeparated && (atEnd || content[offset] != '\n') && !strings.HasSuffix(framed, "\n\n") {
		framed += "\n"
	}
	return framed
}

// expandFullLineDeletion widens a deletion that would leave only indentation
// behind to whole lines, and swallows a neighboring blank line at the seam.
func expandFullLineDeletion(content string, span [2]int) [2]int {
	if span[0] == span[1] {
		return span
	}
	lineStart := strings.LastIndexByte(content[:span[0]], '\n') + 1
	newline := strings.IndexByte(content[span[1]:], '\n')
	lineEnd := len(content)
	if newline >= 0 {
		lineEnd = span[1] + newline
	}
	if !onlySpaces(content[lineStart:span[0]]) || !onlySpaces(content[span[1]:lineEnd]) {
		return span
	}
	out := [2]int{lineStart, lineEnd}
	if newline >= 0 {
		out[1] = lineEnd + 1
	}
	if lineStart == 0 || out[1] >= len(content) {
		if lineStart > 0 {
			previousEnd := lineStart - 1
			previousStart := strings.LastIndexByte(content[:previousEnd], '\n') + 1
			if strings.TrimSpace(content[previousStart:previousEnd]) == "" {
				out[0] = previousStart
			}
		}
		return out
	}

	previousEnd := lineStart - 1
	previousStart := strings.LastIndexByte(content[:previousEnd], '\n') + 1
	nextEnd := len(content)
	if j := strings.IndexByte(content[out[1]:], '\n'); j >= 0 {
		nextEnd = out[1] + j
	}
	previousText := content[previousStart:previousEnd]
	nextText := content[out[1]:nextEnd]
	previousBlank := strings.TrimSpace(previousText) == ""
	nextBlank := strings.TrimSpace(nextText) == ""
	switch {
	case previousBlank && nextBlank:
		if nextEnd < len(content) {
			nextEnd++
		}
		out[1] = nextEnd
	case previousBlank && startsWithClose(nextText):
		out[0] = previousStart
	case nextBlank && endsWithOpen(previousText):
		if nextEnd < len(content) {
			nextEnd++
		}
		out[1] = nextEnd
	}
	return out
}

func onlySpaces(text string) bool { return strings.TrimLeft(text, " \t") == "" }

func startsWithClose(text string) bool {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	if trimmed == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(trimmed)
	return strings.ContainsRune(")]}", r)
}

func endsWithOpen(text string) bool {
	trimmed := strings.TrimRightFunc(text, unicode.IsSpace)
	if trimmed == "" {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(trimmed)
	return strings.ContainsRune("({[", r)
}

// reconcileOverlap merges two edits that touch the same text when one reading
// subsumes the other.
func reconcileOverlap(content string, left, right plannedEdit) (plannedEdit, bool) {
	start := min(left.start, right.start)
	end := max(left.end, right.end)
	container := func(outer, inner plannedEdit) (plannedEdit, bool) {
		if outer.replacement != "" || inner.replacement == "" ||
			inner.start < outer.start || inner.end > outer.end {
			return plannedEdit{}, false
		}
		replacement := inner.replacement
		if outer.end <= len(content) && outer.end > 0 && content[outer.end-1] == '\n' &&
			!strings.HasSuffix(replacement, "\n") {
			replacement += "\n"
		}
		return plannedEdit{start: outer.start, end: outer.end, replacement: replacement, number: inner.number}, true
	}
	if merged, ok := container(left, right); ok {
		return merged, true
	}
	if merged, ok := container(right, left); ok {
		return merged, true
	}
	project := func(edit plannedEdit) string {
		return content[start:edit.start] + edit.replacement + content[edit.end:end]
	}
	if project(left) == project(right) {
		return plannedEdit{start: start, end: end, replacement: project(left), number: left.number}, true
	}
	return plannedEdit{}, false
}

// normalizedContains reports whether content already holds stated text,
// ignoring whitespace and typographic differences.
func normalizedContains(content, stated string) bool {
	needle := normalizeText(stated).text
	return needle != "" && strings.Contains(normalizeText(content).text, needle)
}

// closestDesiredSpan slides a window of the stated text's line count over the
// file and returns the unique block that plausibly is a garbled copy of it.
func closestDesiredSpan(content, stated string) ([2]int, bool) {
	statedNorm := normalizeText(stated).text
	if len(statedNorm) < 12 || len(statedNorm) > 1000 {
		return [2]int{}, false
	}
	count := strings.Count(stated, "\n") + 1
	lines := strings.Split(content, "\n")
	if len(lines) < count {
		return [2]int{}, false
	}
	type scored struct {
		index int
		score float64
	}
	scores := make([]scored, 0, len(lines)-count+1)
	for index := 0; index+count <= len(lines); index++ {
		current := normalizeText(strings.Join(lines[index:index+count], "\n")).text
		denominator := max(len(statedNorm), len(current), 1)
		affix := strings.HasPrefix(statedNorm, current) || strings.HasPrefix(current, statedNorm) ||
			strings.HasSuffix(statedNorm, current) || strings.HasSuffix(current, statedNorm)
		score := 1.0
		if current != "" && !affix && float64(abs(len(statedNorm)-len(current)))/float64(denominator) <= 0.35 {
			score = float64(levenshteinDistance(statedNorm, current)) / float64(denominator)
		}
		scores = append(scores, scored{index: index, score: score})
	}
	best := scores[0]
	for _, entry := range scores[1:] {
		if entry.score < best.score {
			best = entry
		}
	}
	if best.score == 0 || best.score > 0.35 {
		return [2]int{}, false
	}
	for _, entry := range scores {
		if abs(entry.index-best.index) >= count && entry.score-best.score < 0.1 {
			return [2]int{}, false
		}
	}
	start := 0
	for i := 0; i < best.index; i++ {
		start += len(lines[i]) + 1
	}
	end := start
	for i := best.index; i < best.index+count; i++ {
		end += len(lines[i])
		if i < best.index+count-1 {
			end++
		}
	}
	return [2]int{start, min(end, len(content))}, true
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
