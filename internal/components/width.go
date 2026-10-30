package components

import (
	"strings"

	"github.com/pulseaiclub/xui"
)

// TruncateToWidth returns the longest prefix of s that fits within max columns.
func TruncateToWidth(s string, max int, method xui.WidthMethod) string {
	if max <= 0 {
		return ""
	}
	if xui.StringWidth(s, method) <= max {
		return s
	}
	var b strings.Builder
	w := 0
	rest := s
	for rest != "" {
		cluster, cw, next := xui.FirstGrapheme(rest, method)
		rest = next
		if cw < 1 {
			cw = 1
		}
		if w+cw > max {
			break
		}
		b.WriteString(cluster)
		w += cw
	}
	return b.String()
}
