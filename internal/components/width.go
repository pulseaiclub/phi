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

// TruncateToWidthSuffix fits s within max columns, appending suffix to the
// kept prefix when anything was cut. The suffix width counts against max; a
// suffix too wide to fit is dropped in favor of a hard cut.
func TruncateToWidthSuffix(s string, max int, suffix string, method xui.WidthMethod) string {
	if max <= 0 {
		return ""
	}
	if xui.StringWidth(s, method) <= max {
		return s
	}
	suffixW := xui.StringWidth(suffix, method)
	if suffixW >= max {
		return TruncateToWidth(s, max, method)
	}
	return TruncateToWidth(s, max-suffixW, method) + suffix
}
