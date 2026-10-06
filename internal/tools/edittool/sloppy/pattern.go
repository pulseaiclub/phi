package sloppy

import (
	"fmt"
	"strings"
)

// Pattern markers. All begin with the same UTF-8 lead byte (0xE2), so a
// literal run can jump straight between candidate positions.
const (
	gapMarker    = "\u2026" // …
	selOpen      = "\u27ea" // ⟪
	selClose     = "\u27eb" // ⟫
	selDivider   = "\u2502" // │
	markerLead   = 0xe2
	gapMarkerLen = len(gapMarker)
)

// selectionSyntax is the shape a selection must have, for diagnostics.
const selectionSyntax = selOpen + "old" + selDivider + "new" + selClose

// patternError is a malformed FIND body: the failing byte offset within the
// body, the width of the span to underline, and the report to render once the
// parser has mapped the offset back to the payload line the model wrote.
type patternError struct {
	Offset int
	Width  int
	Code   string
	Msg    string
	Label  string
	Help   string
}

// Error implements the error interface.
func (e *patternError) Error() string { return e.Msg }

// scanPattern compiles a FIND body into literal and gap tokens plus inline
// selections. Tokens reference the body as substrings, so scanning allocates
// nothing beyond the token slices themselves.
func scanPattern(body string) (Pattern, error) {
	p := Pattern{Body: body, Tokens: make([]PatternToken, 0, 8)}
	lit := -1 // start of the pending literal run; -1 when none
	emitLiteral := func(end int) {
		if lit < 0 {
			return
		}
		p.Tokens = append(p.Tokens, PatternToken{
			Kind: PatternTokenLiteral, Text: body[lit:end], Start: lit, End: end,
		})
		lit = -1
	}
	for i := 0; i < len(body); {
		if body[i] != markerLead {
			if lit < 0 {
				lit = i
			}
			i = nextMarker(body, i)
			continue
		}
		switch {
		case strings.HasPrefix(body[i:], gapMarker):
			emitLiteral(i)
			p.Tokens = append(p.Tokens, PatternToken{
				Kind:        PatternTokenGap,
				LineBounded: gapIsLineBounded(body, i),
				Start:       i,
				End:         i + gapMarkerLen,
			})
			i += gapMarkerLen
		case strings.HasPrefix(body[i:], selOpen):
			emitLiteral(i)
			tok, sel, next, err := scanSelection(body, i)
			if err != nil {
				return Pattern{}, err
			}
			p.Tokens = append(p.Tokens, tok)
			p.Selections = append(p.Selections, sel)
			i = next
		case strings.HasPrefix(body[i:], selClose):
			return Pattern{}, &patternError{
				Offset: i,
				Width:  len(selClose),
				Code:   codePatternStray,
				Msg:    fmt.Sprintf("stray %s outside a selection", selClose),
				Label:  fmt.Sprintf("no %s opened a selection here", selOpen),
				Help: fmt.Sprintf("a %s closes %sold%snew%s; delete this marker, or type the whole selection.",
					selClose, selOpen, selDivider, selClose),
			}
		default:
			if lit < 0 {
				lit = i
			}
			i++
		}
	}
	emitLiteral(len(body))
	dropEdgeGaps(&p)
	renumberCaptures(&p)
	return p, nil
}

// scanSelection compiles one "⟪old│new⟫" directive at body[i]. It returns the
// literal token for the old text, the selection record, and the offset just
// past the directive.
func scanSelection(body string, i int) (PatternToken, Selection, int, error) {
	inner := body[i+len(selOpen):]
	closeAt := strings.Index(inner, selClose)
	if closeAt < 0 {
		return PatternToken{}, Selection{}, 0, &patternError{
			Offset: i,
			Width:  len(selOpen),
			Code:   codePatternOpen,
			Msg:    fmt.Sprintf("unterminated %s selection", selOpen),
			Label:  fmt.Sprintf("this %s never gets its closing %s", selOpen, selClose),
			Help: fmt.Sprintf("a selection reads %sold%snew%s: the old text, one %s divider, then the new text.",
				selOpen, selDivider, selClose, selDivider),
		}
	}
	openAt := strings.Index(inner, selOpen)
	if openAt >= 0 && openAt < closeAt {
		return PatternToken{}, Selection{}, 0, &patternError{
			Offset: i + len(selOpen) + openAt,
			Width:  len(selOpen),
			Code:   codePatternNest,
			Msg:    fmt.Sprintf("nested %s inside a selection", selOpen),
			Label:  "selections do not nest",
			Help:   fmt.Sprintf("close the open selection with %s before opening another %s.", selClose, selOpen),
		}
	}
	text := inner[:closeAt]
	divAt := strings.Index(text, selDivider)
	if divAt < 0 {
		return PatternToken{}, Selection{}, 0, selectionShapeError(codePatternDiv,
			i, len(selOpen)+closeAt+len(selClose),
			fmt.Sprintf("selection has no %s divider", selectionSyntax),
			fmt.Sprintf("expected one %s between the current and the new text", selDivider))
	}
	if strings.Contains(text[divAt+len(selDivider):], selDivider) {
		return PatternToken{}, Selection{}, 0, selectionShapeError(codePatternMulti,
			i, len(selOpen)+closeAt+len(selClose),
			fmt.Sprintf("selection has multiple %s dividers", selectionSyntax),
			fmt.Sprintf("keep exactly one %s per selection", selDivider))
	}
	oldStart := i + len(selOpen)
	oldEnd := oldStart + divAt
	end := oldEnd + len(selDivider) + len(text[divAt+len(selDivider):]) + len(selClose)
	tok := PatternToken{
		Kind: PatternTokenLiteral, Text: body[oldStart:oldEnd], Start: oldStart, End: oldEnd,
	}
	sel := Selection{
		Old:   body[oldStart:oldEnd],
		New:   body[oldEnd+len(selDivider) : end-len(selClose)],
		Start: i,
		End:   end,
	}
	return tok, sel, end, nil
}

// selectionShapeError reports a selection whose marker pair is in place but
// whose body is not "old│new".
func selectionShapeError(code string, offset, width int, msg, label string) *patternError {
	return &patternError{
		Offset: offset,
		Width:  width,
		Code:   code,
		Msg:    msg,
		Label:  label,
		Help: fmt.Sprintf("write %s; to state whole replacement lines — or to keep a literal %s — "+
			"use a *** SM:PUT block instead.", selectionSyntax, selDivider),
	}
}

// nextMarker returns the offset of the next marker lead byte at or after i,
// or the body length when none remains.
func nextMarker(body string, i int) int {
	if j := strings.IndexByte(body[i+1:], markerLead); j >= 0 {
		return i + 1 + j
	}
	return len(body)
}

// gapIsLineBounded reports whether a gap at body[i] has content after it on
// its line. A gap at line end is free to span lines.
func gapIsLineBounded(body string, i int) bool {
	rest := body[i+gapMarkerLen:]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimLeft(rest, " \t") != ""
}

// dropEdgeGaps removes "…" elisions at the pattern edges; they capture
// nothing. Each dropped gap also takes the newline that joined it to its
// neighbor with it.
func dropEdgeGaps(p *Pattern) {
	if len(p.Tokens) > 0 {
		if t := p.Tokens[0]; t.Kind == PatternTokenGap && strings.TrimSpace(p.Body[:t.Start]) == "" {
			p.EdgeGaps.Leading = true
			p.Tokens = p.Tokens[1:]
			if len(p.Tokens) > 0 && strings.HasPrefix(p.Tokens[0].Text, "\n") {
				p.Tokens[0].Text = p.Tokens[0].Text[1:]
				p.Tokens[0].Start++
			}
		}
	}
	if n := len(p.Tokens); n > 0 {
		if t := p.Tokens[n-1]; t.Kind == PatternTokenGap && strings.TrimSpace(p.Body[t.End:]) == "" {
			p.EdgeGaps.Trailing = true
			p.Tokens = p.Tokens[:n-1]
			if n > 1 && strings.HasSuffix(p.Tokens[n-2].Text, "\n") {
				p.Tokens[n-2].Text = p.Tokens[n-2].Text[:len(p.Tokens[n-2].Text)-1]
				p.Tokens[n-2].End--
			}
		}
	}
}

// renumberCaptures assigns dense capture indices to the surviving gaps.
func renumberCaptures(p *Pattern) {
	capture := 0
	for i := range p.Tokens {
		if p.Tokens[i].Kind == PatternTokenGap {
			p.Tokens[i].Capture = capture
			capture++
		}
	}
}
