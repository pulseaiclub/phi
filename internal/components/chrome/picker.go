package chrome

import (
	"strconv"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// PickerSize reserves borders, a filter line, and a separator above the rows.
func PickerSize(width, maxItems, count, maxW, maxH int) (boxW, boxH, visible int) {
	boxW = width
	if boxW <= 0 {
		boxW = min(maxW*9/10, 112)
		if boxW < 56 {
			boxW = min(maxW, 72)
		}
	}
	if boxW > maxW-2 {
		boxW = maxW - 2
		if boxW < 20 {
			boxW = maxW
		}
	}
	if maxItems < 1 {
		maxItems = 12
	}
	visible = min(max(count, 1), maxItems, max(maxH-6, 3))
	boxH = visible + 4
	if boxH > maxH-2 {
		boxH = maxH - 2
		visible = boxH - 4
		if visible < 1 {
			visible = 1
			boxH = 5
		}
	}
	if maxH < 5 {
		boxH = maxH
		visible = max(boxH-3, 0)
	}
	return boxW, boxH, visible
}

// DrawPickerTitle includes the result count so filtering has visible feedback.
func DrawPickerTitle(
	panel *components.Surface,
	label string,
	filtered, total int,
	st xui.Style,
	method xui.WidthMethod,
) {
	count := strconv.Itoa(total)
	if filtered != total {
		count = strconv.Itoa(filtered) + "/" + count
	}
	title := components.TruncateToWidth(" "+label+Sep+count+" ", max(panel.Size.Width-2, 0), method)
	x := max((panel.Size.Width-xui.StringWidth(title, method))/2, 1)
	panel.Print(x, 0, title, st, method)
}

// PickerListY drops the separator when the viewport only fits one result row.
func PickerListY(height int) int {
	if height < 5 {
		return 2
	}
	return 3
}

// DrawPickerSeparator separates the editable filter from the selectable rows.
func DrawPickerSeparator(panel *components.Surface, st xui.Style) {
	if PickerListY(panel.Size.Height) == 2 {
		return
	}
	for x := 1; x < panel.Size.Width-1; x++ {
		panel.SetCell(x, 2, xui.Cell{Char: "─", Width: 1, Style: st})
	}
}

// DrawPickerHint keeps instructions on the bottom border, with a compact fallback.
func DrawPickerHint(panel *components.Surface, hint, short string, st xui.Style, method xui.WidthMethod) {
	width := max(panel.Size.Width-2, 0)
	if xui.StringWidth(hint, method) > width {
		hint = short
	}
	hint = components.TruncateToWidth(hint, width, method)
	x := max((panel.Size.Width-xui.StringWidth(hint, method))/2, 1)
	panel.Print(x, panel.Size.Height-1, hint, st, method)
}
