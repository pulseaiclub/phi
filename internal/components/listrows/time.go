package listrows

import (
	"fmt"
	"time"
)

// FormatRelative renders a compact relative/absolute time for list rows.
// Column width is kept short so the id + preview stay readable.
func FormatRelative(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := max(now.Sub(t), 0)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		if t.Year() == now.Year() {
			return t.Format("Jan 2")
		}
		return t.Format("Jan 2006")
	}
}
