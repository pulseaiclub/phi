package transcript

import (
	"slices"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// MessageList is a bottom-anchored scrollable transcript with windowed
// (virtualized) drawing: only entries that intersect the viewport are Draw()'n
// each frame. Off-screen heights are cached so scroll extent stays correct.
type MessageList struct {
	Entries []components.Widget
	// ScrollFromBottom is how many rows above the natural bottom pin.
	// 0 means stick to bottom (follow mode).
	ScrollFromBottom int
	PaddingX         int
	ItemSpacing      int // default 1
	Theme            components.Theme
	// Selected is the highlighted entry index for block copy; -1 = none.
	Selected int

	// totalH is last measured content height (for scroll clamping).
	totalH int
	viewH  int

	// height cache (virtualization)
	heights []int
	cacheW  int

	// last layout for hit-testing (viewport-visible items, list-local Y).
	lastPad    int
	lastOrigin int
	lastItems  []listItemGeom

	// lastChildCtx and lastWidth replay the last Draw's geometry so
	// SelectionText can re-render entries the viewport virtualized away.
	lastChildCtx components.DrawContext
	lastWidth    int
}

type listItemGeom struct {
	index int
	y, h  int // list-local (screen within the list surface)
}

func (m *MessageList) spacing() int {
	if m.ItemSpacing <= 0 {
		return 1
	}
	return m.ItemSpacing
}

func (m *MessageList) padX() int {
	if m.PaddingX < 0 {
		return 0
	}
	if m.PaddingX == 0 {
		return 1
	}
	return m.PaddingX
}

// InvalidateHeights drops cached row heights (call after an entry's size changes
// in a way Draw won't see until remount — e.g. external expand). Visible rows
// remasure every frame already.
func (m *MessageList) InvalidateHeights() {
	m.heights = nil
	m.cacheW = 0
}

// InvalidateHeight marks a single row for remasure on the next Draw.
func (m *MessageList) InvalidateHeight(i int) {
	if i >= 0 && i < len(m.heights) {
		m.heights[i] = 0
	}
}

// InvalidateHeightsAt marks the given row indices for remasure.
func (m *MessageList) InvalidateHeightsAt(indices ...int) {
	for _, i := range indices {
		m.InvalidateHeight(i)
	}
}

// ReindexHeights rebuilds the height cache so each newIDs[i] keeps the height
// previously cached for the same id. Unknown ids stay 0 (remeasured next Draw).
// Call before InvalidateHeightsAt when the entry id sequence may have shifted.
func (m *MessageList) ReindexHeights(oldIDs, newIDs []string) {
	if len(m.heights) == 0 || m.cacheW == 0 {
		return
	}
	byID := make(map[string]int, len(oldIDs))
	for i, id := range oldIDs {
		if id == "" || i >= len(m.heights) {
			continue
		}
		if h := m.heights[i]; h > 0 {
			byID[id] = h
		}
	}
	next := make([]int, len(newIDs))
	for i, id := range newIDs {
		if h, ok := byID[id]; ok {
			next[i] = h
		}
	}
	m.heights = next
}

// CachedHeight returns the cached height for index i, or 0 if missing/invalidated.
func (m *MessageList) CachedHeight(i int) int {
	if i < 0 || i >= len(m.heights) {
		return 0
	}
	return m.heights[i]
}

func (m *MessageList) syncHeightCache(n, innerW int) {
	if m.cacheW != innerW {
		m.heights = make([]int, n)
		m.cacheW = innerW
		return
	}
	if len(m.heights) < n {
		m.heights = append(m.heights, make([]int, n-len(m.heights))...)
	} else if len(m.heights) > n {
		m.heights = m.heights[:n]
	}
}

// ensureHeights lays the height cache out for n entries at childCtx's width,
// measuring only entries that are new or invalidated.
func (m *MessageList) ensureHeights(n int, childCtx components.DrawContext) {
	m.syncHeightCache(n, childCtx.Max.Width)
	for i := range n {
		if m.heights[i] < 1 {
			m.heights[i] = m.measure(i, childCtx)
		}
	}
}

func (m *MessageList) measure(i int, childCtx components.DrawContext) int {
	if i < 0 || i >= len(m.Entries) || m.Entries[i] == nil {
		return 1
	}
	surf := m.Entries[i].Draw(childCtx)
	h := surf.Size.Height
	h = max(h, 1)
	return h
}

func (m *MessageList) contentOffsets(n, gap int) (tops []int, total int) {
	tops = make([]int, n)
	y := 0
	for i := range n {
		if i > 0 {
			y += gap
		}
		tops[i] = y
		h := 1
		if i < len(m.heights) && m.heights[i] > 0 {
			h = m.heights[i]
		}
		y += h
	}
	return tops, y
}

// Handle scrolls on PageUp/PageDown and mouse wheel, and forwards events
// to visible entries (last drawn first).
func (m *MessageList) Handle(ctx *components.EventContext, ev xui.Event) {
	switch e := ev.(type) {
	case xui.KeyEvent:
		switch e.Code {
		case xui.KeyPageUp:
			m.ScrollFromBottom += m.viewH
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyPageDown:
			m.ScrollFromBottom -= m.viewH
			if m.ScrollFromBottom < 0 {
				m.ScrollFromBottom = 0
			}
			ctx.ConsumeAndRedraw()
			return
		}
	case xui.MouseEvent:
		wheel := e.Wheel
		wheel = max(wheel, 1)
		if e.Button == xui.MouseWheelUp {
			m.ScrollFromBottom += 3 * wheel
			ctx.ConsumeAndRedraw()
			return
		}
		if e.Button == xui.MouseWheelDown {
			m.ScrollFromBottom -= 3 * wheel
			if m.ScrollFromBottom < 0 {
				m.ScrollFromBottom = 0
			}
			ctx.ConsumeAndRedraw()
			return
		}
	}
	// Prefer last-visible entries; fall back to all if layout not ready.
	if len(m.lastItems) > 0 {
		for i := range slices.Backward(m.lastItems) {
			idx := m.lastItems[i].index
			if idx < 0 || idx >= len(m.Entries) {
				continue
			}
			m.Entries[idx].Handle(ctx, ev)
			if ctx.Consume {
				return
			}
		}
		return
	}
	for i := range slices.Backward(m.Entries) {
		m.Entries[i].Handle(ctx, ev)
		if ctx.Consume {
			return
		}
	}
}

// Draw renders the bottom-anchored windowed entries, measuring heights on
// demand and clamping ScrollFromBottom to the content extent.
func (m *MessageList) Draw(ctx components.DrawContext) components.Surface {
	w := ctx.Max.Width
	h := ctx.Max.Height
	if w <= 0 {
		w = 40
	}
	if h <= 0 {
		h = 10
	}
	m.viewH = h
	pad := m.padX()
	innerW := w - pad*2
	innerW = max(innerW, 1)
	gap := m.spacing()
	n := len(m.Entries)
	childCtx := ctx.WithConstraints(components.Size{}, components.Size{Width: innerW, Height: 10000})
	m.lastChildCtx = childCtx
	m.lastWidth = w

	m.ensureHeights(n, childCtx)

	var root components.Surface
	for pass := range 2 {
		tops, totalH := m.contentOffsets(n, gap)
		m.totalH = totalH
		maxScroll := m.totalH - h
		maxScroll = max(maxScroll, 0)
		if m.ScrollFromBottom > maxScroll {
			m.ScrollFromBottom = maxScroll
		}
		if m.ScrollFromBottom < 0 {
			m.ScrollFromBottom = 0
		}
		originY := h - m.totalH + m.ScrollFromBottom
		m.lastPad = pad
		m.lastOrigin = originY

		root = components.Surface{Size: components.Size{Width: w, Height: h}, Widget: m}
		m.lastItems = m.lastItems[:0]
		heightChanged := false

		for i := range n {
			itemH := m.heights[i]
			itemH = max(itemH, 1)
			top := originY + tops[i]
			bot := top + itemH
			if bot <= 0 || top >= h {
				continue // outside viewport — skip Draw
			}
			surf := m.Entries[i].Draw(childCtx)
			nh := surf.Size.Height
			nh = max(nh, 1)
			if nh != m.heights[i] {
				m.heights[i] = nh
				heightChanged = true
			}
			if i == m.Selected && m.Selected >= 0 {
				hl := xui.Style{Bg: xui.RGBColor(0x2a, 0x2e, 0x24)}
				components.ApplyBlockHighlight(&surf, hl)
			}
			root.Children = append(root.Children, components.SubSurface{
				Origin:  components.Point{X: pad, Y: top},
				Surface: surf,
			})
			m.lastItems = append(m.lastItems, listItemGeom{index: i, y: top, h: nh})
		}

		if !heightChanged || pass == 1 {
			break
		}
		// Visible row height changed (expand/collapse) — relayout once.
	}
	return root
}

// StickToBottom resets follow mode.
func (m *MessageList) StickToBottom() {
	m.ScrollFromBottom = 0
}

// SelectionText returns the text covered by a drag selection given in content
// space (rows relative to the content top, columns list-local). Draw realizes
// only the entries the viewport shows, so the selected rows are rendered again
// here: a selection spanning more than one screenful would otherwise copy just
// the page that happened to be on screen.
func (m *MessageList) SelectionText(ax, ay, ex, ey int) string {
	n := len(m.Entries)
	if n == 0 || m.lastWidth <= 0 {
		return "" // nothing drawn yet — no geometry to select against
	}
	childCtx := m.lastChildCtx
	m.ensureHeights(n, childCtx)
	tops, total := m.contentOffsets(n, m.spacing())

	// Rows above and below the content (short list, bottom-anchored) hold no
	// text; clamping keeps the copied block free of blank edge lines.
	x0, y0, x1, y1 := components.NormalizeSelectionOrder(ax, ay, ex, ey)
	y0, y1 = max(y0, 0), min(y1, total-1)
	if y1 < y0 {
		return ""
	}

	// Row 0 of the surface is content row y0, and it keeps the list's full
	// width: an interior row of a drag is grabbed whole, so clipping the
	// surface to the selection's columns would truncate every row between the
	// two endpoints.
	root := components.Surface{Size: components.Size{Width: m.lastWidth, Height: y1 - y0 + 1}}
	pad := m.padX()
	for i := range n {
		top := tops[i]
		if top+m.heights[i] <= y0 || top > y1 {
			continue
		}
		root.Children = append(root.Children, components.SubSurface{
			Origin:  components.Point{X: pad, Y: top - y0},
			Surface: m.Entries[i].Draw(childCtx),
		})
	}
	return components.ExtractSurfaceText(root, x0, 0, x1, root.Size.Height-1)
}

// ContentOrigin is the list-local Y of content row 0 after the last Draw.
// Viewport Y → content Y: y - ContentOrigin(); reverse: y + ContentOrigin().
func (m *MessageList) ContentOrigin() int {
	return m.lastOrigin
}

// IndexAtPoint returns the entry index under list-local (x,y), or -1.
func (m *MessageList) IndexAtPoint(x, y int) int {
	_ = x
	for _, it := range m.lastItems {
		if y >= it.y && y < it.y+it.h {
			return it.index
		}
	}
	return -1
}

// SelectedCopyText returns the copyable text of the selected entry.
func (m *MessageList) SelectedCopyText() string {
	if m.Selected < 0 || m.Selected >= len(m.Entries) {
		return ""
	}
	return components.EntryCopyText(m.Entries[m.Selected])
}

// LastCopyText returns copyable text from the last entry that supports it.
func (m *MessageList) LastCopyText() string {
	for i := range slices.Backward(m.Entries) {
		if t := components.EntryCopyText(m.Entries[i]); t != "" {
			return t
		}
	}
	return ""
}

// VisibleRange returns the inclusive [first, last] entry indices realized in
// the last Draw, or (-1, -1) if none.
func (m *MessageList) VisibleRange() (first, last int) {
	if len(m.lastItems) == 0 {
		return -1, -1
	}
	return m.lastItems[0].index, m.lastItems[len(m.lastItems)-1].index
}
