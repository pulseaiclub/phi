// Package archive extracts release archives with the platform tooling the
// installers already rely on.
package archive

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Extract unpacks archive into dst. Supported formats are "tar.gz" and "zip".
// The external commands inherit context cancellation and their combined output
// is reported on failure.
func Extract(ctx context.Context, archive, format, dst string) error {
	switch format {
	case "tar.gz":
		cmd := exec.CommandContext(ctx, "tar", "-xzf", archive, "-C", dst)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("tar: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "zip":
		ps := fmt.Sprintf("Expand-Archive -LiteralPath %q -DestinationPath %q -Force", archive, dst)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", ps)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("powershell Expand-Archive: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	default:
		return fmt.Errorf("unknown archive format: %s", format)
	}
}
