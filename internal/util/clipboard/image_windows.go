//go:build windows

package clipboard

import (
	"os"
	"strings"

	"github.com/pulseaiclub/phi/internal/util"
)

// readClipboardImagePlatform asks PowerShell for the image. A cheap format
// probe cannot stand in for this: Clipboard.GetImage reads registered image
// formats (JFIF, GIF, TIFF, EXIF, PNG) as well as the raw bitmaps, so an
// allowlist would silently miss whatever the copy source happened to use.
func readClipboardImagePlatform() (Image, error) {
	tmpFile, err := os.CreateTemp("", "phi-clip-*.png")
	if err != nil {
		return Image{}, ErrUnavailable
	}
	path := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(path)

	psQuoted := util.ReplaceAll(path, "'", "''")
	psScript := "Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; " +
		"$path = '" + psQuoted + "'; $img = [System.Windows.Forms.Clipboard]::GetImage(); " +
		"if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }"
	out, err := runCommandTimeout(5*defaultReadTimeout, "powershell", "-NoProfile", "-Command", psScript)
	if err != nil {
		return Image{}, ErrUnavailable
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return Image{}, ErrUnavailable
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return Image{}, ErrUnavailable
	}
	return Image{Data: data, MimeType: "image/png"}, nil
}
