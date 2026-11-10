package tree

import "strings"

// Style holds connector characters (defaults to the standard box-drawing set).
type Style struct {
	Vertical   string // ancestor continuation
	Tee        string // non-last child branch
	Elbow      string // last child branch
	Horizontal string
	Indent     int // columns per depth level (min 2)
}

// DefaultStyle returns │ / ├── / ╰── connectors with indent 4.
func DefaultStyle() Style {
	return Style{
		Vertical:   "│",
		Tee:        "├",
		Elbow:      "╰",
		Horizontal: "─",
		Indent:     4,
	}
}

func (s Style) normalized() Style {
	if s.Vertical == "" {
		s.Vertical = "│"
	}
	if s.Tee == "" {
		s.Tee = "├"
	}
	if s.Elbow == "" {
		s.Elbow = "╰"
	}
	if s.Horizontal == "" {
		s.Horizontal = "─"
	}
	if s.Indent < 2 {
		s.Indent = 4
	}
	return s
}

// Flat is one DFS-flattened node with depth and sibling position metadata.
type Flat[T any] struct {
	Item             T
	Depth            int
	IsLast           bool
	AncestorsAreLast []bool // len == Depth; true means that ancestor was last (no vertical)
}

// Prefix returns the box-drawing prefix for a flat row (e.g. "│   ╰── ").
func Prefix[T any](f Flat[T], style Style) string {
	style = style.normalized()
	var b []byte
	for _, last := range f.AncestorsAreLast {
		b = append(b, []byte(style.getAncestorPrefix(last))...)
	}
	branch := style.Tee
	if f.IsLast {
		branch = style.Elbow
	}
	b = append(b, []byte(style.getConnectorText(branch))...)
	return string(b)
}

// PrefixForSiblings builds prefixes for a flat sibling list (depth 0 under a parent).
func PrefixForSiblings(count, index int, style Style) string {
	if count <= 0 || index < 0 || index >= count {
		return ""
	}
	return Prefix(Flat[struct{}]{
		Depth:  0,
		IsLast: index == count-1,
	}, style)
}

func (s Style) getConnectorText(branch string) string {
	pad := s.Indent - 2
	pad = max(pad, 0)
	out := branch
	var b strings.Builder
	for i := 0; i < pad; i++ {
		b.WriteString(s.Horizontal)
	}
	return out + b.String() + " "
}

func (s Style) getAncestorPrefix(ancestorIsLast bool) string {
	if ancestorIsLast {
		return spaces(s.Indent)
	}
	return s.Vertical + spaces(s.Indent-1)
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
