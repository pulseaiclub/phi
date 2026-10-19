package permission

import (
	"strings"

	"github.com/pulseaiclub/phi/internal/util"
)

// Summarize returns a short human-readable summary of the request for UI.
func Summarize(req Request) string {
	switch {
	case req.Command != "":
		return util.TruncateBytes(req.Command, 200, "…")
	case len(req.Paths) > 0:
		return strings.Join(req.Paths, ", ")
	case req.Kind == KindUnknown:
		return req.Tool
	default:
		return string(req.Kind)
	}
}
